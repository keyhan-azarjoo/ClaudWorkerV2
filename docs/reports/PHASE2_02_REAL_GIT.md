# Phase 2 — Integration #2: Real Git Adapter

Architecture frozen. This iteration replaces **exactly one** more edge — the simulated Git — with a
real adapter over the deterministic `internal/git` toolbelt. The Orchestrator, Assignment Engine, and
Policy Engine are unchanged (one minimal, documented exception below).

## Architecture impact

| Added | Role |
|---|---|
| `internal/adapters/git` (`gitadapter`) | Real Git edge: per-assignment worktree lifecycle, branch lifecycle, commit, `--no-ff` merge, rebase, pull ff-only, cleanup. Implements `orchestrator.Developer`, `orchestrator.Merger`, and the new `orchestrator.Workspace` ports. |
| `internal/git.Pull` | **Minimal additive** method (fetch + `merge --ff-only`) — a required Phase-2.2 capability the toolbelt lacked. New method, no change to existing behaviour. |
| `web/ops-console` Git page | Shows real worktrees/branches/status from the Control Plane. |

### Required change to another subsystem (documented, minimal)

`orchestrator.Workspace` — a new **optional, nil-safe** cleanup port, plus a `Cleaner` field and a
single call in `finish()`. **Why:** a real Git edge must clean worktrees/branches on completion **and
failure** (safety), which no existing port covered; the Merger only runs on success. **Minimised:** it
is optional (simulation leaves it nil → unchanged behaviour, all prior tests pass), touches only the
orchestrator's terminal path, and changes neither the Assignment Engine nor the Policy Engine.

### How it plugs in (Orchestrator unchanged)

The orchestrator still calls `Developer`, `Merger`, and (optionally) `Cleaner`. Only the **wiring**
changes:
- `gitadapter.Developer` = **real** `EnsureWorkspace` (branch + disposable worktree off `development`)
  → run the inner worker (still simulated — the reasoning is Phase 2.3) → materialise + **commit** its
  changes into the assignment branch.
- `gitadapter.Merger` = **real** `--no-ff` merge of the assignment branch into the integration branch,
  best-effort push.
- `gitadapter.Adapter` = **real** `Cleanup` (remove worktree + delete branch) on every terminal state.

## Safety

- All per-assignment work happens in **disposable worktrees** named deterministically from the issue
  key (`agent/<issue>` ⇢ `<worktrees>/<issue>`). The main/human working tree is never touched; the
  integration merge runs on the engine's own dedicated clone's `development` branch.
- **Conflicts auto-abort** (the toolbelt runs `merge --abort` / `rebase --abort`), leaving the tree
  clean and restart-safe; the orchestrator then fails the assignment and the `Cleaner` removes the
  workspace.
- Worktree add/remove and branch delete are **idempotent**, so cleanup after crash/cancel and on
  restart is safe and needs no human step.

## Conflict handling

| Case | Result |
|---|---|
| Merge conflict | `Merge` returns `merged=false`, merge auto-aborted, clone stays clean (`TestMergeConflictAbortsCleanly`). |
| Rebase conflict | `Rebase` returns `rebased=false` + conflicting paths, rebase auto-aborted, worktree clean (`TestRebaseConflictAbortsCleanly`). |

## Cleanup validation

- Completion + failure cleanup: `Cleaner.Cleanup` removes worktree + branch on any terminal state.
- After failure / cancellation: idempotent `Cleanup` leaves no orphan (`TestCleanupAfterFailureAndRestart`).
- Restart recovery: a **brand-new adapter over the same clone** sees the orphaned worktree and cleans
  it (`TestCleanupAfterFailureAndRestart`) — restart-safe, no human step.

## Restart validation

- Worktrees + branches are recomputed deterministically from the issue key and are **not persisted**;
  Git itself is the source of truth. A fresh adapter re-discovers them via `Worktrees`.
- Live mode persists assignment/lease/knowledge state in durable file stores; `Orchestrator.Recover`
  resumes unfinished assignments (`EnsureWorkspace` is idempotent → the worktree is reused) and never
  restarts completed work.

## Production validation — real end-to-end

`TestProductionFlowRealGit` runs the whole loop with **real Git**:

```
Jira → Assignment → REAL Git (branch + worktree + commit) → (sim) Worker → Verify → Improve
     → REAL --no-ff Merge → Jira Done → worktree/branch cleanup
```

Asserts: assignment reaches **Done**, the worker's file is really **on `development`** in the
integration clone (real merge), **Jira → Done** recorded, and the **worktree + branch are cleaned**.

## Operations Console

Live Git state wired into the Control Plane: `git.worktrees` (active worktrees + branch + HEAD) and
`git.status` (branch, clean?, conflicts, worktree count). A new **Git** page renders them and reacts
to `MergeCompleted` / `WorkspaceCleaned` events. No fake data remains for the Git surface in live mode.

## Performance (real Git vs simulation)

| Operation | Simulation | Real Git |
|---|---|---|
| Worktree create | ~µs (in-memory) | git subprocess (~tens of ms) |
| Commit | ~µs | git add+commit (~tens of ms) |
| Merge (`--no-ff`) | ~µs (returns true) | git fetch+merge (~tens of ms) |
| Rebase | n/a | git rebase (~tens of ms) |
| Cleanup | ~µs | worktree remove + branch delete (~tens of ms) |

A full real-Git assignment cycle (workspace → develop → verify → improve → merge → cleanup) incl. repo
setup measured ≈ **440 ms** end-to-end; steady-state per-op cost is git-subprocess-bound, as expected.
Startup, memory, and CPU are unchanged from Phase 2.1 (no long-lived state added). This is the normal
real-vs-simulation trade-off; simulation remains the fast regression path.

## Complexity review

- **No new subsystem, no ACP.** The Developer/Merger ports pre-existed; only one **optional** port was
  added (documented above).
- **No new external dependency** (module still: `gopkg.in/yaml.v3`).
- The adapter reuses the existing `internal/git` toolbelt (conflict-abort, idempotent worktrees,
  nothing-to-commit handling already there) — it adds orchestration, not duplication. Net complexity:
  **equal** (one edge, real + sim implementations behind the same ports).

## Regression review

- `gofmt` / `go vet` clean; **`go test -race ./...` — 21/21 packages PASS.**
- Simulation Mode unchanged (`Cleaner` nil) — every pre-existing test still green, including the S11
  orchestrator suite and Phase-2.1 real-Jira suite.
- Ops-console JS validated (`node --check`).

## Remaining simulated after this phase

Worker Runtime · Verification Drivers · Resource Discovery. **Merge is now real** (via the Git
adapter). Everything else is real.

## Stop

Iteration #2 complete. **Next (do not start yet): #3 Real Worker Runtime (Claude Code).** Stopping for
review.
