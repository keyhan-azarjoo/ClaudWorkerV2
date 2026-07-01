# 04 — Project Brain (Knowledge Brain + Execution State)

> **No AI session is the source of truth. The Project Brain is.** Workers are disposable; knowledge
> is permanent (P4).

The Project Brain is split into **two independent stores** with different lifetimes, so persistent
knowledge is never at risk from transient execution churn:

1. **Knowledge Brain** — *persistent knowledge only*: architecture, patterns, standards, decisions,
   rules, and the derived-but-durable indexes/maps. Survives forever; backed up; git-diffable.
2. **Execution State** — *temporary execution state only*: assignments, progress, retries, metrics,
   locks, deferral tracking, event log, Jira cache. Rebuildable and disposable; wiping it loses no
   knowledge.

This separation is a hard rule: **the two never mix.** You can delete the entire Execution State and
restart with the Knowledge Brain intact; you can back up the Knowledge Brain without dragging along
volatile run data.

## Why split them

- **Safety:** a corrupt/rebuilt Execution State can never damage decisions, standards, or learned
  patterns.
- **Backup/versioning:** the Knowledge Brain (small, high-value, slow-changing) is exported/committed;
  the Execution State (large, fast-changing, throwaway) is not.
- **Clarity:** every datum has one home. "Is this knowledge or execution?" has a definite answer,
  which keeps prompts small (only Knowledge Brain feeds prompts) and the model out of execution
  bookkeeping.

## What each store holds

### Knowledge Brain (persistent)
| Content | Form | Notes |
|---|---|---|
| **Architecture** summary | `knowledge/architecture.md` | short (≤2 KB), the "Architecture Summary" prompt slice (P9) |
| **Patterns** (learned failure patterns, idioms) | `knowledge.db` | known-failure signatures + resolutions; reusable code idioms |
| **Standards / conventions** | `knowledge/conventions.md` | coding standards confirmed for this project |
| **Decisions / ADRs** | `knowledge/decisions/NNNN-*.md` (+ index in `knowledge.db`) | append-only; "recent decisions" prompt slice |
| **Rules** | `knowledge/rules.md` | project-specific hard rules (e.g. owner gates), referenced by config |
| **File & symbol index** | `knowledge.db` | derived from repo; enables relevant-files selection |
| **Dependency graph** | `knowledge.db` | derived; expands relevant-files minimally |
| **QA maps & goldens** | `knowledge.db` + artifacts | navigation maps + baseline screenshots |
| **Glossary** | `knowledge/glossary.md` | domain terms |

All Knowledge-Brain content is either **authored** (decisions, rules, conventions, architecture) or
**derived-and-durable** (index, graph, qa-maps). Derived parts are rebuildable from the repo; authored
parts are the irreplaceable core and are stored as git-diffable markdown + an index row.

### Execution State (temporary)
| Content | Form | Notes |
|---|---|---|
| **Assignments** | `state.db` (`assignments`) | one row per issue execution ([16_WorkerStateMachine](16_WorkerStateMachine.md)) |
| **Progress** | `state.db` (`assignments`, `events`) | current state, live progress, evidence refs |
| **Retries** | `state.db` (`assignments.attempt`) | attempt counters, stuck-detector signals ([20_DecisionEngine](20_DecisionEngine.md)) |
| **Metrics** | `state.db` (`assignments`, `metrics`) | tokens, cost, durations, gate pass-ratios |
| **Locks** | `state.db` (`locks`) | issue/device/merge leases ([15_LockManager](15_LockManager.md)) |
| **Deferral tracking** | `state.db` (`deferrals`) | open deferrals + follow-up keys (the *how-to* text lives in Knowledge Brain) |
| **Jira cache** | `state.db` (`issues_cache`) | last-known Jira snapshot + `dirty_writes` |

Full schema in [12_Database](12_Database.md).

## Storage layout (per project, on the SSD — C-6)

```
<engine-home>/projects/<project>/
  knowledge.db            # Knowledge Brain: index, graph, patterns, qa-maps, ADR index
  knowledge/              # Knowledge Brain (human-readable, git-diffable, backed up)
    architecture.md
    conventions.md
    rules.md
    glossary.md
    decisions/            # NNNN-title.md ADRs
  state.db                # Execution State: assignments, progress, retries, metrics, locks, deferrals, jira cache
  worktrees/<ISSUE-KEY>/  # per-assignment git worktrees
  artifacts/              # screenshots, renders, evidence (referenced by both stores)
```

`<engine-home>` defaults to `/Volumes/Extreme SSD/cwv2-home/`.

## How prompts are assembled — from the Knowledge Brain only (P9)

For a given issue and stage, the Orchestrator builds the prompt from **only** these slices, all drawn
from the **Knowledge Brain** (never from Execution State):

1. **Task** — Jira issue summary + description (from the Jira cache, but that is Task *content*, not
   execution bookkeeping).
2. **Acceptance Criteria** — parsed / plugin-generated / Manager-refined.
3. **Relevant Files** — selected via the file index + dependency graph, scoped to the plan.
4. **Architecture Summary** — `knowledge/architecture.md`.
5. **Recent Decisions** — top-K relevant ADRs.
6. **Current Failures** — the *structured failure text* from the last attempt (the failure's content),
   matched against known-failure **patterns** in the Knowledge Brain. (The attempt *count* is
   Execution State and is used by the Decision Engine, not the prompt.)

No whole-repo dumps, no unrelated history, no execution bookkeeping in prompts. All selection is
deterministic Go → assembling context costs **zero** model tokens.

## Maintenance

- **Knowledge Brain (deterministic, scheduled):** re-index changed files, update the dependency graph,
  refresh the architecture summary's mechanical parts; after each merge, incrementally re-index and
  append any new ADRs / learned patterns.
- **Execution State (deterministic, continuous):** the Orchestrator writes assignment/progress/metric
  rows as it runs; the reaper prunes closed assignments and stale locks; the whole store can be
  dropped and rebuilt from Jira + Git + Knowledge Brain (NFR-7).

## Writing to the Brain (workers)

Workers never write DB rows directly. They call deterministic tools; the Engine routes each write to
the correct store:
- `knowledge.add_decision(context, decision, consequences)` → Knowledge Brain ADR.
- `knowledge.note_pattern(signature, detail, resolution?)` → Knowledge Brain learned pattern.
- `knowledge.update_architecture(section, text)` → Knowledge Brain (validated size/format).
- `defer.add(kind, reason, howto, followup_key)` → Execution State deferral row **+** the reusable
  *how-to* stored in Knowledge Brain rules/patterns.

The Engine validates and persists; the worker's session can then vanish with nothing lost (P4).

## Rebuildability & backup (NFR-7)

- **Execution State** is fully rebuildable from Jira (work truth) + Git (code truth) + Knowledge Brain
  and is therefore never backed up — losing it costs only in-flight run history.
- **Knowledge Brain**: derived parts (index/graph/qa-maps) rebuild from the repo; authored parts
  (decisions/rules/conventions/architecture) are exported as markdown and periodically committed to a
  knowledge repo (aligns with the owner's memory-backup practice), so an SSD loss never loses a
  decision.

## Portability

Both stores are project-agnostic. Onboarding a new project creates a fresh `projects/<project>/`; the
**migration phase** ([22_Migration](22_Migration.md)) populates the initial Knowledge Brain
(architecture, frameworks, standards, structure, technologies, plugins, summary) and an empty
Execution State — no engine changes (P10).
