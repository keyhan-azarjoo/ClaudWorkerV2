# 08 — Jira

Jira is the single source of truth for **work** (P2). There is no local backlog, no second task
system (FR-2). This document defines how the Engine reads work from Jira, maps it to the workflow,
and writes results back — all via a deterministic `jira.*` toolbelt (zero tokens).

## Principles

- **Read work only from Jira.** The Engine never invents tasks. If it isn't a Jira issue, it isn't
  work.
- **Write outcomes back to Jira.** Status, a concise result comment, worklog, labels, and links are
  the durable record. The local DB only *caches* Jira and *coordinates* execution (FR-2); losing the
  DB loses no work knowledge.
- **Deterministic.** All Jira interaction is Go over the Jira REST API. Workers never call Jira
  directly; they emit structured results and the Engine writes them.

## What the Engine reads

- A configured **JQL query** defines the work queue (config, [13_Config](13_Config.md)), e.g.
  issues in a "Ready" status, in the project, assigned to the engine user or a queue, ordered by
  priority then rank. The exact JQL is project config, not code (P10).
- For each candidate issue the Engine caches: key, summary, description, acceptance criteria,
  priority, labels, linked issues, and any repo/component hints.

## Status mapping (workflow ↔ Jira)

The internal state machine ([03_Workflow](03_Workflow.md)) maps to Jira statuses via **config**
(status names differ per board). Default mapping:

| Internal state | Jira status (configurable) |
|---|---|
| eligible to start | `Ready` / `Selected for Development` |
| CLAIMED → PLANNED → DEVELOPING → BUILT → QA | `In Progress` |
| MERGING | `In Review` (optional) |
| CLOSED | `Done` |
| NEEDS_HUMAN | stays `In Progress` + label `needs-human` |
| deferred follow-up | new issue in `Ready` (or backlog) |

Transitions are performed by the `jira.transition` tool using the board's transition ids, resolved
once and cached. If a transition isn't available, the Engine logs it and proceeds (status is a
reflection, never a blocker to real work).

## Acceptance criteria

- The Engine extracts AC from a configured field or from the description (a labeled "Acceptance
  Criteria" section). If absent, the Manager worker proposes AC from the description, which the
  Engine writes back as a comment for owner visibility.
- AC drives QA ([06_QA](06_QA.md)); every AC item needs evidence to be marked met.

## What the Engine writes

- **On claim:** transition to In Progress, assign to the engine user, add a short "picked up" comment
  with the branch name.
- **On progress:** worklog entries and (optionally) brief status comments; the dashboard is the
  primary live view, so Jira comments stay concise (avoid noise).
- **On close:** transition to Done + a **result comment**: what changed, which checks ran (with
  evidence refs), deferrals + their follow-up keys, and the merge commit/branch. Log time in
  worklog.
- **On defer:** create a linked follow-up issue ("Deferred QA: …") describing the check, why it
  couldn't run, exactly how to run it, and the environment needed; link it to the original.
- **On block:** add `needs-human` label + a comment with the specific blocker and what's needed; the
  owner is notified (dashboard/Telegram) without blocking other issues.

## Claiming protocol (no conflicts — P8)

- The Engine claims an issue by (a) acquiring the internal issue lock and (b) assigning + transitioning
  it in Jira. The internal lock is the real mutual-exclusion mechanism; the Jira assignment makes
  ownership visible to humans.
- Because the owner may also be working, the Engine respects issues already assigned to a human or
  labeled `owner-working`/`needs-human` and does not claim them.
- Priority ordering is deterministic (priority, rank/age, key) so "what next" needs no AI.

## Field & label conventions (config-mapped)

All of these are **configurable** so the engine stays portable (P10):

- Work queue JQL.
- Status names for ready / in-progress / review / done.
- AC field/section location.
- Labels: `needs-human`, `owner-working`, `deferred`, `engine`.
- Optional component→repo/plugin hints (which repo & project-type a component maps to).

## Idempotency & resilience

- Every write is idempotent where possible (don't double-comment, don't re-transition if already in
  target state).
- Jira API errors are retried with backoff; a hard Jira outage pauses **new** claims but does not
  corrupt in-flight local work — results are re-synced when Jira returns (the DB remembers what still
  needs writing).
- The cached Jira snapshot lets the dashboard render even when Jira is briefly unreachable, clearly
  marked as cached.

## Integration with Confluence (optional, project-config)

Some projects (MyOTGO) also expect Confluence updates. This is an **optional deterministic
publisher** driven by config (which pages, what to post), not a worker responsibility — keeping the
core project-agnostic (P10). If not configured, it's simply off.

## Why not a local task list

A local backlog would create a second source of truth that drifts from Jira, cause double-booking
with the owner and other tools, and violate P2. The DB caches Jira for speed and stores *execution*
state (locks, runs, attempts) — never the *existence* of work.
