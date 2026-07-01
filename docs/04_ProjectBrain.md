# 04 — Project Brain

> **No AI session is the source of truth. The Project Brain is.** Workers are disposable; knowledge
> is permanent (P4).

The Brain is the Engine's durable, deterministically-maintained knowledge store for **one project**.
It exists so that (a) prompts stay small and relevant (P9), and (b) nothing important dies with a
worker session.

## What the Brain is (and is not)

- **Is:** a local store of *knowledge about the project* — architecture summary, decisions, a
  file/symbol index, dependency graph, per-issue history, known failures, deferrals, and QA maps.
- **Is not:** the source of truth for *work* (that's Jira) or *code* (that's Git). The Brain never
  contradicts them; it *summarizes and indexes* them and adds knowledge that isn't otherwise
  recorded (why decisions were made, what failed, what's deferred).
- **Rebuildable:** the mechanical parts (indexes, graphs, summaries) can be regenerated from the
  repo + Jira at any time. Only the *authored* knowledge (decisions, failure notes, deferrals) is
  irreplaceable, and that is stored as append-only, git-diffable records.

## Storage

Two layers, both under the engine home on the SSD, per project:

```
<engine-home>/projects/<project>/
  brain.db            # SQLite: indexes, graphs, issue history, failures, deferrals, embeddings-lite
  knowledge/          # human-readable markdown, git-diffable
    architecture.md   # the maintained architecture summary (short)
    decisions/        # one ADR per file: NNNN-title.md
    conventions.md    # coding conventions extracted/confirmed for this project
    glossary.md       # domain terms
```

- **SQLite** for queryable, structured, high-churn data (file index, symbols, dependency edges,
  issue runs, failures). Schema in [12_Database](12_Database.md).
- **Markdown** for authored, low-churn, human-valuable knowledge (architecture summary, ADRs,
  conventions). Kept small and skimmable.

## Contents

### 1. Architecture summary (`knowledge/architecture.md`)
A **short** (target ≤ 2 KB) description of the project's real architecture: main modules, entry
points, key boundaries, how to build/run/test. Maintained deterministically where possible (from
the file index and build config) and refined by the Manager worker when it learns something durable.
This is the "Architecture Summary" injected into every prompt (P9).

### 2. Decisions / ADRs (`knowledge/decisions/NNNN-*.md`)
Append-only architecture decision records: context, decision, consequences, links to the issue that
produced it. Workers propose ADRs via a tool; the Engine writes them. The **recent decisions** slice
(last K relevant ADRs) is injected into prompts so workers don't re-litigate settled choices.

### 3. File & symbol index (SQLite)
Deterministic Go indexer walks the repo and records files, languages, sizes, top-level symbols
(functions/classes/exports), and per-file summaries. Enables **relevant-files selection** without an
AI reading the whole tree. Refreshed on schedule and after each merge.

### 4. Dependency graph (SQLite)
Deterministic import/reference graph (per language/plugin). Used to expand "relevant files": given
the files a plan touches, include their close neighbors. Keeps context minimal but sufficient.

### 5. Per-issue history (SQLite)
For each Jira issue the Engine works: the plan, branches, attempts, checks run, QA verdicts,
failures, final outcome, tokens/cost. Feeds the dashboard and lets a re-opened issue resume with
context instead of from zero.

### 6. Known failures (SQLite)
Structured records of failures seen (build error signatures, flaky tests, environment gotchas) with
their resolution if known. Injected as **current failures** context when relevant, so a Developer
worker doesn't repeat a known dead-end.

### 7. Deferrals (SQLite)
Every deferred check (FR-18): what, why, how to run later, follow-up Jira key, environment needed.
Surfaced on the dashboard; swept periodically to see if the environment is now available.

### 8. QA maps (SQLite + artifacts)
App/screen maps, navigation graphs, and **golden screenshots** (baseline images) per screen/state,
used by human-like QA to navigate and to image-diff (see [06_QA](06_QA.md)). Artifacts (images)
stored on disk, referenced by the DB.

## How prompts are assembled from the Brain (P9)

For a given issue and stage, the Orchestrator builds the prompt from **only** these slices:

1. **Task** — the Jira issue summary + description (from Jira, cached).
2. **Acceptance Criteria** — parsed from the issue / produced by the Manager.
3. **Relevant Files** — selected via the file index + dependency graph, scoped to the plan; file
   contents included only for the few files in scope, truncated intelligently.
4. **Architecture Summary** — `knowledge/architecture.md` (short).
5. **Recent Decisions** — top-K relevant ADRs.
6. **Current Failures** — structured failures from the last attempt(s) for this issue + matching
   known-failure records.

Nothing else. No whole-repo dumps, no unrelated history (P9). All selection is deterministic Go, so
assembling context costs **zero model tokens**.

## Maintenance (deterministic, scheduled)

- On startup and on interval: re-index changed files, update the dependency graph, refresh the
  architecture summary's mechanical parts.
- After every merge: incrementally re-index the merged files; append the issue's history record.
- Nightly (or on demand): compact the DB, prune stale cache rows, verify Brain is rebuildable.

## Writing to the Brain (workers)

Workers never write DB rows directly. They call deterministic tools:
- `brain.add_decision(context, decision, consequences)` → writes an ADR.
- `brain.note_failure(signature, detail, resolution?)` → records a known failure.
- `brain.add_deferral(kind, reason, howto, followup_key)` → records a deferral.
- `brain.update_architecture(section, text)` → proposes an architecture-summary edit (Engine
  validates size/format).

The Engine validates and persists; the worker's session can then vanish with nothing lost (P4).

## Portability

The Brain schema and tools are project-agnostic. Onboarding a new project creates a fresh
`projects/<project>/` and the deterministic indexers populate it from that project's repo — no
engine changes (P10, [13_Config](13_Config.md)).
