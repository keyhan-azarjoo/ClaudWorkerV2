# S2 — Assignment Engine: Completion Report

Subsystem **S2 (Assignment Engine, walking skeleton)** per [21_ImplementationRoadmap](../21_ImplementationRoadmap.md)
(spec v2.1.0, [ACP-0001](../acp/ACP-0001-assignment-engine-before-database.md)). Scope: support exactly
**one complete Assignment lifecycle** end-to-end; nothing more.

## 1. Architecture compliance

| Requirement | Status | Evidence |
|---|---|---|
| Engine is 100% deterministic; **no AI reasoning inside it** (Law 18) | ✅ | `internal/assignment/engine.go` contains no model calls; AI only behind the `Worker` port |
| AI receives only Assignment + AC + Relevant Files + Knowledge context (never exec/git/jira/lock state) | ✅ | `WorkerInput` (worker.go) carries exactly those fields; `buildPrompt` renders only them |
| Workers own nothing; disposable (Law 4) | ✅ | `Worker` is a stateless port; all durable state is in `Assignment`/`Store` |
| Assignment owns lifecycle, retries, progress, timing, issue ownership | ✅ | `Assignment` struct fields; `Store` enforces one active record per issue |
| Issue claimed via Jira; deterministic tools coordinated | ✅ | `claim()` uses `jira.TransitionTo`+`AddComment`, `git` branch/worktree |
| One disposable AI worker spawned; result received; state updated | ✅ | `develop()` → `Worker.Run` → apply files → `git.Commit` |
| Restart/resume; never redo completed work (Law 19) | ✅ | `Resume()` re-drives only `Unfinished()`; `ClaimAndRun` skips issues with any existing Assignment |
| Hand over to QA / Merge | ✅ | `handOffToQA` (boundary; QA subsystem is S8) → `handOffToMerge` (deterministic `--no-ff`) |
| Minimum persistence only; DB emerges later (S3) | ✅ | file-backed JSON `Store`; no `state.db`/`knowledge.db` built |
| Tiny branches, `--no-ff` merge, branch+worktree deleted (docs/07) | ✅ | `handOffToMerge` merges then `RemoveWorktree`+`DeleteBranch` |
| Commit author = keyhanazarjoo (C-2) | ✅ | git client constructed `WithIdentity`; enforced in S1 |
| No new architecture; no framework; no speculative abstraction | ✅ | one package, one interface (Worker, 2 impls), concrete git/jira deps |

## 2. Performance report

Deterministic, zero-token (workers faked in CI). Measured on the SSD:

| Benchmark | Result | Note |
|---|---|---|
| `BenchmarkStoreSave` | ~3.85 ms/op, 1732 B, 15 allocs | atomic write + **fsync** (durability cost); a lifecycle persists ~5–6 times → ~20 ms total persistence per Assignment |
| `BenchmarkCurrentRevision` (S1) | ~5 ms/op | git subprocess (unchanged) |

Persistence cost is dominated by `fsync` (correct for restart-safety). No hot loop; each Assignment
does a small, bounded number of transitions. No performance concern for the KPI (completed issues).

## 3. Restart recovery validation (Law 19)

`TestRestartResume` (engine_test.go):
- Drives an Assignment to the **QA** checkpoint, then discards the engine (simulated crash).
- A **brand-new** `Engine` sharing the same `Store` calls `Resume(ctx)` → the Assignment continues
  QA → Merging → **Done**.
- **Asserts the worker is NOT re-invoked** on resume (`w.calls` unchanged) — completed development is
  not redone.
- A second `Resume` finds **0** unfinished (terminal work never re-entered).
- Atomic `Save` (temp + `fsync` + rename) guarantees no half-written record after a crash.

## 4. Assignment lifecycle validation

| Test | Proves |
|---|---|
| `TestFullLifecycleToDone` | Claim → Developing → QA → Merging → **Done**; file merged into `development`; branch+worktree removed; `MergeSHA` set; persisted Done; worker called exactly once |
| `TestIssueLockPreventsReclaim` | An issue with an existing Assignment is **not** re-claimed (issue lock + no redo); worker not re-invoked |
| `TestRetryThenSucceed` | Worker fails once then succeeds → completes Done at `attempt=1` |
| `TestFailAfterMaxAttempts` | Worker always fails → **Failed** at `attempt=maxAttempts` |
| `TestRestartResume` | Resume from last stable state; completed work not redone |
| `store_test.go` (4 tests) | Save/Load/List/Unfinished/atomic-write/missing-load |

All pass with `-race`.

## 5. Code metrics

| Package | src LOC | test LOC | coverage |
|---|---|---|---|
| internal/assignment | 527 | ~340 | 67.6% |
| internal/git (S1) | 504 | 286 | 77.4% |
| internal/jira (S1) | 580 | 243 | 68.1% |
| internal/config (S0) | 282 | 151 | 79.5% |
| internal/doctor (S0) | 195 | 106 | 76.1% |
| internal/enginehome (S0) | 96 | 53 | 85.7% |
| internal/secrets (S0) | 100 | 56 | 60.0% |
| internal/logging (S0) | 41 | 30 | 75.0% |
| cmd/cwv2 | 594 | ~270 | 50.0% |

Gates: `go vet` clean · `gofmt` clean · `go test -race ./...` all pass · benchmarks run · no TODO
placeholders · no `panic`-driven flow.

## 6. Package dependency graph

```
cmd/cwv2
  ├── internal/assignment ──► internal/git
  │                       └─► internal/jira
  ├── internal/doctor ─► internal/config, internal/enginehome, internal/secrets
  ├── internal/config
  ├── internal/enginehome
  ├── internal/git
  ├── internal/jira
  ├── internal/logging
  └── internal/secrets
```

- **No cycles.** `internal/assignment` depends only on `git` and `jira` (the deterministic toolbelt).
- Only **one interface** in the whole subsystem: `assignment.Worker` (impls: `ClaudeWorker`,
  test `fakeWorker`). git/jira are concrete (tested with a temp repo + httptest mock).

## 7. Simplicity review (answered before merge)

1. **Can this subsystem be made smaller?** — Trimmed: Planning+Coding collapsed into one
   `StateDeveloping` (one worker in the skeleton); removed the unused `ActiveForIssue` store method
   (the engine's lock is "an assignment record exists").
2. **Can any package be removed?** — No new package beyond `internal/assignment`; it justifies itself.
3. **Can any interface be eliminated?** — Only `Worker` remains, with two real implementations
   (production `claude -p` + test fake) → required, not speculative. No interface-per-struct.
4. **Can deterministic code replace AI?** — The engine, QA handoff, and merge are all deterministic Go;
   AI is confined to the single `develop` step behind the port.
5. **Does every package justify its existence?** — Yes (see dependency graph).

## 8. Simplifications discovered during implementation (adopted)

- **The persisted Assignment *is* the issue lock.** A separate Lock component isn't needed in the
  skeleton — "a non-terminal Assignment exists for this issue" is the lock. (Full Lock Manager with
  device/merge scopes is S5.)
- **No Decision Engine object yet.** The skeleton's transitions are linear + a bounded retry, so
  deterministic inline logic suffices; a pure `Decide()` component is deferred to S6 where the full
  rule set/precedence justifies it.
- **File-backed JSON store, not SQLite.** Minimum persistence for ownership/retry/restart; `state.db`
  emerges in S3 behind the same `Store` seam.
- **One `Developing` state, not Planning+Coding.** The skeleton spawns one worker; the Manager/Developer
  split lands with the full Worker Runner (S7).

## 9. Notes / honest limitations

- `handOffToQA` is a deterministic PASS **boundary** — the real QA subsystem is S8. This is the
  walking-skeleton seam, clearly marked, not a fake green (it advances state, it does not claim a QA
  result).
- `ClaudeWorker` (real `claude -p`) is intentionally thin; strict output-schema validation and small
  prompts (Knowledge Brain) harden it in S4/S7. CI never invokes it (fakes) → zero tokens.
- Live end-to-end against the real SCRUM board needs Jira auth (deferred; the atlassian integration
  requires interactive authorization). The full lifecycle is proven with a temp git repo + httptest
  Jira mock + fake worker.

## 10. Verdict

S2 is complete against its stated goal: **one complete Assignment lifecycle, deterministic, restart-safe,
minimum persistence, zero-token CI**, all gates green. Recommend stop-and-review before S3, per the
roadmap. Do **not** proceed to S3 without approval.
