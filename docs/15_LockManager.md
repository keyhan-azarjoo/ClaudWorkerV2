# 15 — Lock Manager

The Lock Manager is the deterministic Go subsystem that guarantees **zero conflicts, zero duplicated
work, tiny merge conflicts, and immediate ownership** (P8). It is the concrete implementation of the
"Lock / Ownership manager" box in [02_Architecture](02_Architecture.md), and it extends the git rules
in [07_Git](07_Git.md) and the `locks` table in [12_Database](12_Database.md).

> **The Lock Manager is 100% deterministic. Claude never decides a lock.** Workers *request* and
> *release* locks through tools; granting, ordering, timeout, stealing, and recovery are pure Go over
> SQLite (`state.db`). A model is never in the critical section (Law 18).

## V1 scope (frozen)

**V1 has exactly three lock scopes, all hard (mandatory):**

| # | Scope | Locks what | Mode | Prevents |
|---|---|---|---|---|
| 1 | `device` | a physical/virtual device or board (ESP32, phone, sim) | exclusive | two Assignments driving one board |
| 2 | `issue` | one Jira issue end-to-end | exclusive | duplicated work on an issue |
| 3 | `merge` | the merge slot for one repo's `development` | exclusive (global per repo) | concurrent advances of `development` |

That is the entire V1 model. Finer-grained **advisory** locks (repo/module/folder/file) are **not
part of V1** — they are specified as **Future expansion (§Future)** and must not be built until the
measured merge-conflict rate justifies them (a deliberate simplification, Law 17). V1 keeps
merge conflicts tiny *without* advisory locks by relying on: one worktree per Assignment, tiny
branches, refresh-before-merge, and immediate serialized merge ([07_Git](07_Git.md)).

## 1. Why the Lock Manager exists

- **Zero conflicts (P8):** two Assignments must never mutate the same working tree, the same branch's
  merge, or the same device/board.
- **Zero duplicated work:** two Assignments must never pick up the same Jira issue.
- **Immediate ownership:** claiming is atomic; the instant an issue is admitted, ownership is
  unambiguous and visible (`state.db` + Jira assignment, [08_Jira](08_Jira.md)).
- **Tiny merge conflicts:** the merge lock serializes integration so `development` advances one
  verified `--no-ff` merge at a time; combined with tiny branches, conflicts stay small.

## 2. Lock lifecycle

```
   request ──▶ [try acquire in canonical order]
                     │ granted            │ contended
                     ▼                    ▼
                  HELD ──heartbeat──▶ HELD        WAIT (bounded) ──timeout──▶ DENIED
                     │                                   │
             release │                     steal (if holder stale) ──▶ granted
                     ▼
                  RELEASED
                     │
             (crash) └─ heartbeat stops ─▶ STALE ─(reaper/steal)─▶ reclaimable
```

States: `HELD`, `WAIT`, `RELEASED`, `STALE`. A lock row in `state.db.locks`
([12_Database](12_Database.md)) always encodes exactly one live holder or is absent.

- **Acquire** — atomic `INSERT`-if-absent-or-stale in a single SQLite transaction. Success returns a
  **lease** (`lock_id`, `owner`, `ttl`, `fence` token — §7).
- **Heartbeat** — the holder updates `heartbeat_at` on an interval (< ttl/3). Missing heartbeats make
  the lock `STALE`.
- **Release** — explicit delete of the row by the holder (verified by `owner` + `fence`).
- **Reap** — the deterministic reaper deletes `STALE` rows and triggers cleanup of the associated
  worktree/Assignment ([12_Database](12_Database.md), NFR-8).

## 3. Issue locks

- **Hard** exclusive lock, `id = issue:<KEY>`. Acquired at CLAIM *before* any Jira transition, so two
  Assignments can never both claim `SCRUM-123` (I-1, [08_Jira](08_Jira.md)).
- The engine also respects human ownership and the eligibility field: an issue assigned to a human,
  labeled `owner-working`/`needs-human`, or whose **Automation** field is not `Enabled`
  ([22_Migration](22_Migration.md)) is **not** lockable by the engine (NFR-11).
- Held for the entire issue journey ([16_WorkerStateMachine](16_WorkerStateMachine.md)); released at
  Done / cancel / failure.

## 4. Device locks

- **Hard** exclusive lock over a device/board (phone via adb, iOS sim/real device, ESP32 board),
  `id = device:<name>`.
- Prevents two QA/repair runs driving the same board (a real V1 hazard). Integrates with the dashboard
  **Devices** view ([09_Dashboard](09_Dashboard.md)) and the hardware access model
  ([10_Hardware](10_Hardware.md)).
- If the device isn't present, no lock is taken and the check **defers** ([06_QA](06_QA.md)); the Lock
  Manager never blocks waiting for absent hardware.

## 5. Merge locks

- **Hard**, global **per repo**: `id = merge:<repo>`. The Integrator must hold it to advance
  `development` (I-3, FR-8, [07_Git](07_Git.md)).
- Serializes all merges into one repo's `development` so it advances by exactly one verified `--no-ff`
  merge at a time. Held only for the short refresh→verify→merge→delete window, then released.
- A multi-repo issue acquires each touched repo's merge lock **in canonical repo order** (alpha by
  repo name) to prevent cross-repo deadlock (§6).

## 6. Deadlock prevention

Deterministic, no detection heuristics needed:

1. **Canonical acquisition order:** every Assignment acquires in scope order `device < issue < merge`,
   and within a scope by canonical key order (lexicographic id). A cycle is impossible when everyone
   locks in the same total order.
2. **Bounded waits:** every wait has a timeout (§7); on timeout the request is DENIED and the issue
   re-queued, never parked forever.
3. **Merge lock is leaf-level and short:** acquired last, held briefly, released promptly.

The wait-for graph is a DAG by construction → no deadlock (verified by test).

## 7. Lock timeout & stealing (fencing)

- **TTL + heartbeat.** Each lock has `ttl_seconds`; the holder heartbeats. `heartbeat_at + ttl < now`
  ⇒ `STALE`.
- **Stealing.** A new requester may **steal** a `STALE` lock atomically (same transaction reassigns
  `owner` and **increments a monotonic `fence` token**). A previous (dead) holder that ever wakes
  presents an **old fence** and is rejected by every mutating tool — its writes cannot land (prevents
  a zombie worker from corrupting a stolen resource).
- **Live locks are never stolen.** Only `STALE` locks are reclaimable. A slow-but-alive holder keeps
  heartbeating and keeps its lease.
- **Timeouts are per scope** (config): device (long — a flash may be slow), issue (long — the full
  journey, kept alive by heartbeat, not a single ttl window), merge (short).

## 8. Recovery after crash (worker/process)

- A crashed worker stops heartbeating → its locks go `STALE` → reaper (or the next requester via
  steal) reclaims them, with a fence bump so the dead worker can't write.
- The associated `assignments` row is returned to the last **stable** stage
  ([16_WorkerStateMachine](16_WorkerStateMachine.md)); its worktree is cleaned; attempt counter
  incremented; if attempts exhausted → NEEDS_HUMAN.
- No half-state persists: because `development` only advances under a held `merge` lock via one atomic
  `--no-ff`, a crash can never leave a half-merge (NFR-8, I-3).

## 9. Recovery after reboot (whole machine)

- On `cwv2 serve` startup, the **reconciler** runs before admitting new work:
  1. Load all `locks`. Any whose holder Assignment is gone and whose heartbeat is stale ⇒ reap.
  2. Detect orphan worktrees/branches not backed by a live Assignment ⇒ clean (a branch with an
     unmerged commit that maps to an issue is kept; otherwise removed).
  3. Re-fetch Jira truth and rebuild `issues_cache`; reconcile in-flight `assignments` against it.
  4. Only then resume the Scheduler.
- Because all lock state is **persisted+fsynced on the SSD** (§11), a reboot loses nothing and cannot
  double-grant.

## 10. Worker cancellation

- **Stop-issue** ([09_Dashboard](09_Dashboard.md)) is a cooperative cancel: signal the worker, bump
  the fence (so any in-flight tool write is rejected), kill the process, clean the worktree, release
  **all** locks the Assignment held (reverse canonical order), and return the Jira issue to its prior
  stable status. Idempotent and safe to call repeatedly.

## 11. Lock persistence & SSD storage

- All locks live in SQLite (`state.db.locks`) per project, under the engine home on the **external
  SSD** (C-6): `/Volumes/Extreme SSD/cwv2-home/projects/<project>/state.db`.
- SQLite **WAL** mode + a single writer (the engine) makes acquire/steal/release atomic; the dashboard
  reads concurrently. `fsync` on the acquire/steal transaction guarantees a lease survives power loss.
- No lock state is kept only in memory; memory is a cache of the DB, reloaded on startup.

## 12. Database schema (V1)

The `state.db.locks` table ([12_Database](12_Database.md)):

| col | type | notes |
|---|---|---|
| id | text pk | `<scope>:<key>`, e.g. `issue:SCRUM-123`, `merge:app`, `device:esp32-s3-a` |
| scope | text | V1: `device` \| `issue` \| `merge` |
| repo | text | repo the lock belongs to (null for device/issue) |
| owner | text | Assignment id holding the lease |
| fence | int | monotonic token, bumped on every (re)grant/steal |
| acquired_at | text | |
| heartbeat_at | text | holder updates; stale ⇒ reclaimable |
| ttl_seconds | int | per-scope default from config |
| advisory | bool | **always false in V1** (reserved for Future expansion) |
| meta | json | canonical order key, etc. |

Indexes: unique on `id`, `(scope, repo)`, `(heartbeat_at)` for the reaper.

## 13. APIs (deterministic tools + internal)

**Tool surface (least-privilege, [05_Workers](05_Workers.md)):**
- `lock.acquire(scope, key, ttl?) -> {granted, fence} | {denied, reason}`
- `lock.release(scope, key, fence)`
- `lock.heartbeat(scope, key, fence)`
- `lock.status(scope?, key?) -> [leases]` (read-only; powers the dashboard)

**Internal Orchestrator API (Go):**
- `AcquireSet(assignmentID, []Request) -> Lease | Deferred` — canonical order (§6).
- `ReleaseAll(assignmentID)` — reverse order, at Done/cancel/crash-cleanup.
- `Steal(scope, key) -> Lease` — only if STALE; bumps fence.
- `Reap()` — scheduled sweep of STALE locks + cleanup hooks.
- `Reconcile()` — startup recovery (§9).

**Fencing contract:** every mutating tool (git write, file write, device drive, merge) takes the
current `fence` and the tool layer rejects any call whose fence is behind the DB — so a stale holder
physically cannot mutate a resource it lost.

## 14. Sequence diagrams

### 14.1 Claim an issue (immediate ownership)

```
Scheduler        LockMgr(state.db)     Jira
   │  AcquireSet(issue:K)                 │
   ├────────────────────▶│ INSERT issue:K (fence=1)
   │◀──── Lease ─────────┤                │
   │  assign + transition ─────────────── ▶│ (In Progress, assignee=engine)
   │  create worktree from newest development
   ▼  (Planning …)
```

### 14.2 Serialized merge

```
Integrator(A1)      LockMgr(state.db)   Git
  │ Acquire(merge:app)     │             │
  ├───────────────────────▶│ held(fence) │
  │ refresh dev; verify; merge --no-ff ──▶│ development += A1
  │ Release(merge:app) ────▶│ deleted     │
  │ delete branch + worktree ─────────────▶│
   (A2 waiting on merge:app now proceeds)
```

### 14.3 Crash + steal with fencing

```
Worker of A1 (A1 holds device:board, fence=4) ──X crash (no heartbeat)
   ... ttl elapses → device:board STALE
A2 requests device:board → Steal → fence=5 granted to A2
A1 zombie wakes, tries device.flash(fence=4) → tool REJECTS (fence<5) → no corruption
```

## 15. Examples

- **Two issues, same board:** both need ESP32-S3-A for on-hardware QA. `device:esp32-s3-a` is
  exclusive; one runs the on-hardware check, the other **defers** it (Wokwi sim still PASSes the
  functional part) — no waiting, no conflict ([10_Hardware](10_Hardware.md)).
- **Two issues, same file (V1 behavior):** both edit `auth.dart`. V1 has no file lock, so both proceed
  on separate worktrees; each refreshes from `development` before merge; the second merge resolves a
  small textual conflict (or the Integrator worker if semantic). Tiny branches keep this trivial. If
  such collisions become frequent, that is the signal to enable Future advisory locks (§Future).
- **Owner commits mid-flight:** owner pushes to `development`. The Scheduler's fetch sees it; the
  affected worktree refreshes before its next gate; merge still needs `merge:<repo>` + a clean verify.
  Owner work is never overwritten (NFR-11).

## 16. Invariants (lock-specific)

- **L-1** A hard lock (`device`/`issue`/`merge`) is required for its operation; the tool layer
  enforces it.
- **L-2** All locks for an Assignment are acquired in canonical scope+key order (no deadlock, §6).
- **L-3** Only `STALE` locks are stolen; every (re)grant/steal bumps the `fence`.
- **L-4** A behind-fence mutation is always rejected (no zombie writes).
- **L-5** All lock state is persisted+fsynced on the SSD; memory is only a cache.
- **L-6** No model call ever occurs inside a lock decision (Law 18).

## Future expansion (NOT in V1 — build only when justified)

Documented so the schema and design are forward-compatible, but explicitly **out of V1 scope** to keep
the first build simple (Law 17). Enable only when metrics (measured merge-conflict rate) show a need.

- **Advisory `file` / `folder` / `module` / `repo` locks.** Purpose: keep merge conflicts tiny by
  steering the Scheduler away from admitting two Assignments that would edit the same code
  simultaneously. Derived deterministically from the Manager's `files_to_touch` plan + the dependency
  graph.
  - `module` = a plugin-defined boundary (Dart package, .NET project, firmware component, KiCad
    board); the primary advisory for large/cross-cutting changes.
  - `folder`/`file` = finer subtree/file steering.
  - `repo` = whole-repo lock (exclusive, or a `shared`/`exclusive` mode) for rare repo-wide refactors.
- **Semantics when added:** advisory locks are acquired *all-or-defer* at admission alongside the
  hard `issue` lock, in the extended canonical order `device < issue < repo < module < folder < file <
  merge`; contention → defer admission (no hold-and-wait), or admit and rely on refresh+Integrator.
  They never gate correctness (worktrees already prevent tree corruption, G-3) — only conflict size.
- **Schema:** already forward-compatible — set `advisory=true` and use the extended scope values; the
  `fence`/canonical-order machinery is unchanged.
- **Rule:** do not implement this section as part of V1. It is here so nothing has to be redesigned
  later, not as a work item.
