# 03 — Workflow

The issue lifecycle state machine. This is the spine of the Orchestrator (deterministic Go). One
Jira issue flows through it at a time per worker; many issues flow in parallel across workers.

> **Two views, one process.** This doc describes the lifecycle in **Jira/issue** terms. Its
> fine-grained **execution** counterpart — the worker-slot states (Idle, Waiting, Repair, Cancelled,
> Failed) and every transition's entry/exit/timeout/recovery — is
> [16_WorkerStateMachine](16_WorkerStateMachine.md), which includes a state-mapping table. The
> Developer↔QA retry is the universal [17_RepairLoop](17_RepairLoop.md); locking is
> [15_LockManager](15_LockManager.md).

## States

```
        (Jira: Ready)
             │  Scheduler admits (usage + concurrency + priority)
             ▼
        ┌─────────┐
        │ CLAIMED │  lock acquired, own worktree from newest development
        └────┬────┘
             ▼
        ┌─────────┐   Manager worker (reasoning)
        │ PLANNED │   → plan: acceptance criteria, relevant files, approach, deferral flags
        └────┬────┘
             ▼
        ┌───────────┐  Developer worker (reasoning) + deterministic tools
        │ DEVELOPING│  → code changes, self-build, unit checks; commit on branch
        └────┬──────┘
             ▼
        ┌─────────┐   Deterministic gate: build + lint + fast tests
        │  BUILT  │   fail → back to DEVELOPING (attempt++)
        └────┬────┘
             ▼
        ┌─────────┐   QA worker (human-like/visual) + deterministic tools
        │   QA    │   → verify against AC; produce PASS / FAIL(reasons) / DEFER(reasons)
        └────┬────┘
     PASS │   │ FAIL           │ DEFER (test impossible now)
          │   ▼                ▼
          │  back to        record deferral + follow-up issue,
          │  DEVELOPING     treat blocked checks as non-blocking
          │  (attempt++)         │
          ▼                      ▼
        ┌──────────┐   Integrator (mostly deterministic; worker only if conflict/judgment)
        │ MERGING  │   refresh development → --no-ff merge → delete branch
        └────┬─────┘
             ▼
        ┌──────────┐   Deterministic: transition Jira, post result comment, worklog
        │  CLOSED  │
        └──────────┘

    Any state ──error/attempts exhausted──▶ NEEDS_HUMAN (flag issue, notify, free resources)
```

## State definitions

| State | Owner | Reasoning? | Exit condition |
|---|---|---|---|
| CLAIMED | Orchestrator + Lock mgr | no | worktree ready on fresh `development` |
| PLANNED | Manager worker | yes | valid plan JSON stored in Brain |
| DEVELOPING | Developer worker | yes | changes committed on branch |
| BUILT | Deterministic gate | no | build+lint+fast tests pass |
| QA | QA worker | yes | verdict PASS / FAIL / DEFER |
| MERGING | Integrator | mostly no | branch merged `--no-ff`, deleted |
| CLOSED | Orchestrator | no | Jira transitioned + commented |
| NEEDS_HUMAN | Orchestrator | no | human resolves / re-queues |

## The core loop (one issue)

1. **Admit & Claim** *(Go)* — Scheduler picks the top eligible Jira issue (FR-1), Lock mgr grants
   ownership, Git tool fetches + fast-forwards `development` and creates a tiny branch
   `agent/<KEY>-<slug>` in a **fresh worktree** (FR-5, FR-3).
2. **Plan** *(Manager)* — assemble a small prompt (task, AC, relevant files from Brain, arch
   summary, recent decisions). Manager returns a structured plan: restated AC, files it expects to
   touch, approach, risk/deferral flags, and whether QA can be visual. Stored in Brain.
3. **Develop** *(Developer)* — small prompt (plan + those files + arch summary + recent decisions +
   current failures). Developer edits code and calls deterministic tools to build/format/test
   locally. Commits on its branch. Returns a structured change summary.
4. **Build gate** *(Go)* — run build + lint + fast unit tests via toolbelt. Fail → return structured
   failure to a new Developer step (attempt++). This gate is zero-token.
5. **QA** *(QA worker)* — human-like verification against AC (see [06_QA](06_QA.md)). Uses
   deterministic tools (launch, navigate, screenshot, imgdiff, log parse). Returns:
   - **PASS** → proceed to Merge.
   - **FAIL(reasons)** → structured, actionable → new Developer step (attempt++).
   - **DEFER(reasons)** → test genuinely impossible now (no hardware/device/human) → record
     deferral, create follow-up Jira issue, treat as non-blocking (FR-18), proceed to Merge.
6. **Merge** *(Integrator)* — refresh `development`; if the branch still applies cleanly, `merge
   --no-ff` and delete the branch + worktree (FR-6). If it conflicts, refresh & re-verify; only if a
   real semantic conflict needs judgment is a worker consulted. Merges are serialized (FR-8).
7. **Close** *(Go)* — transition the Jira issue to the configured done state, post a concise result
   comment (what changed, checks run, deferrals + follow-up keys), log worklog, release the lock.

## Retry & escalation policy (deterministic)

- Developer↔QA loop is bounded by `maxAttempts` (config, default 3).
- On each failure, the **structured failure** is added to *current failures* for the next prompt
  (P9) — the worker sees exactly what went wrong, not the whole history.
- Attempts exhausted, or an unrecoverable error, or a conflict that can't be auto-resolved →
  **NEEDS_HUMAN**: flag the Jira issue (`needs-human` label), post the failure detail, notify the
  owner, release locks/worktree so other issues keep flowing (FR-25, P8).

## Deferred tests (never block — P7 / FR-18)

A check is *deferrable* when it cannot be executed now for an environmental reason, not because the
code is wrong. Categories: **hardware unavailable**, **no test device connected**, **visual QA
impossible**, **human-only QA**. When QA returns DEFER:

- Record the deferral in the Brain (what, why, how to run it later).
- Create a follow-up Jira issue linked to the original ("Deferred QA: …").
- Do **not** hold the branch open. Merge if all *runnable* checks pass.
- The dashboard surfaces open deferrals so the owner can clear them when the environment is ready.

## Owner-commit synchronization (FR-7)

The Scheduler periodically fetches. If `development` advanced (owner or another merge) while an
issue is in flight, the Orchestrator refreshes that issue's worktree (rebase/refresh onto the new
`development`) **before** the Build gate and again before Merge. Owner work is never overwritten;
worst case the issue re-runs its Build/QA gates on the refreshed base.

## Concurrency & ordering

- Stages within an issue are sequential; issues run in parallel up to `maxConcurrent`.
- Merge is the only globally-serialized stage (single Integrator lock), keeping `development` linear
  and conflict-free.
- Priority ordering for admission is deterministic: Jira priority, then age, then issue key — no AI
  needed to decide "what next".

## Invariants (must always hold)

- **I-1** At most one active worker per issue.
- **I-2** At most one worktree per branch; no shared checkout is ever mutated.
- **I-3** `development` only advances via `--no-ff` merge of a verified branch (never a direct
  commit; C-3).
- **I-4** A branch is deleted immediately after its merge; no long-lived branches.
- **I-5** Every terminal outcome (CLOSED, NEEDS_HUMAN, DEFER-merge) is reflected in Jira.
- **I-6** No state transition happens without the corresponding lock held.
