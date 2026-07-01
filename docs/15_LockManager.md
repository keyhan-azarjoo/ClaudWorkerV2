# 15 — Lock Manager

The Lock Manager is the deterministic Go subsystem that guarantees **zero conflicts, zero duplicated
work, tiny merge conflicts, and immediate ownership** (P8). It is the concrete implementation of the
"Lock / Ownership manager" box in [02_Architecture](02_Architecture.md), and it extends the git/lock
rules in [07_Git](07_Git.md) and the `locks` table in [12_Database](12_Database.md).

> **The Lock Manager is 100% deterministic. Claude never decides a lock.** Workers *request* and
> *release* locks through tools; granting, ordering, timeout, stealing, and recovery are pure Go over
> SQLite. A model is never in the critical section.

## 1. Why the Lock Manager exists

- **Zero conflicts (P8):** two units of work must never mutate the same resource concurrently — the
  same working tree, the same branch's merge, the same device/board.
- **Zero duplicated work:** two workers must never pick up the same Jira issue.
- **Tiny merge conflicts:** if two in-flight issues would edit the same files/module, the Lock
  Manager **serializes or steers** them so their branches don't diverge on the same lines — keeping
  merges small and usually conflict-free ([07_Git](07_Git.md)).
- **Immediate ownership:** claiming is atomic; the instant an issue is admitted, ownership is
  unambiguous and visible (DB + Jira assignment, [08_Jira](08_Jira.md)).

Without it, the parallel-issue model of [03_Workflow](03_Workflow.md) would race on trees, branches,
devices, and files.

## 2. Lock scopes (granularity hierarchy)

Locks are ordered from coars­est to finest. **Deadlock prevention (§10) requires acquiring in this
canonical order.**

| # | Scope | Locks what | Mode | Prevents |
|---|---|---|---|---|
| 1 | `device` | a physical/virtual device or board (ESP32, phone, sim) | exclusive | two issues driving one board |
| 2 | `issue` | one Jira issue end-to-end | exclusive | duplicated work on an issue |
| 3 | `repo` | a whole repository (rare; big refactors/migrations) | exclusive / shared | tree-wide operations vs normal work |
| 4 | `module` | a logical module/package (plugin-defined boundary) | exclusive (advisory) | two issues editing one module → merge conflicts |
| 5 | `folder` | a directory subtree | exclusive (advisory) | overlapping edits in a subtree |
| 6 | `file` | a single file | exclusive (advisory) | two issues editing the same file |
| 7 | `merge` | the merge slot for one repo's `development` | exclusive (global per repo) | concurrent advances of `development` |

**Hard vs advisory:**

- `device`, `issue`, and `merge` are **hard** locks — the operation is *forbidden* without them
  (they enforce correctness: I-1, I-3, no double-drive of a board).
- `repo`, `module`, `folder`, `file` are **advisory** — they exist to **minimize merge conflicts**,
  not to prevent tree corruption (that is already prevented by one-worktree-per-issue,
  [07_Git](07_Git.md) G-3). An advisory lock steers the Scheduler away from admitting a second issue
  that would edit the same code at the same time; if it can't be honored, work still proceeds on a
  fresh `development` base and the Integrator resolves any (now-tiny) conflict.

### Why file/folder/module locks despite worktrees

Worktrees stop two issues from corrupting *the same checkout*, but two issues on *separate*
worktrees can still edit the *same file* and produce a merge conflict later. The advisory locks let
the Scheduler **avoid scheduling colliding issues simultaneously** (or warn and accept the risk),
directly serving the "tiny merge conflicts" goal. They are derived deterministically from the
Manager's `files_to_touch` plan ([05_Workers](05_Workers.md)) and the dependency graph
([04_ProjectBrain](04_ProjectBrain.md)).

## 3. Lock lifecycle

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

States: `HELD`, `WAIT`, `RELEASED`, `STALE`. A lock row in `locks` ([12_Database](12_Database.md))
always encodes exactly one live holder or is absent.

- **Acquire** — atomic `INSERT`-if-absent-or-stale in a single SQLite transaction. Success returns a
  **lease** (`lock_id`, `owner`, `ttl`, `fence` token — see §11).
- **Heartbeat** — the holder updates `heartbeat_at` on an interval (< ttl/3). Missing heartbeats make
  the lock `STALE`.
- **Release** — explicit delete of the row by the holder (verified by `owner` + `fence`).
- **Reap** — the deterministic reaper deletes `STALE` rows and triggers cleanup of the associated
  worktree/run ([12_Database](12_Database.md), NFR-8).

## 4. File locks

- **Granted from the plan.** After PLANNED, the Orchestrator has `files_to_touch`. It requests
  advisory `file` locks for each (plus dependency-graph neighbors it will likely edit).
- **Contention → steer, don't stall.** If another live issue holds a needed `file` lock, the
  Scheduler prefers to **defer admitting** this issue until the other releases, rather than run them
  in parallel and collide. If deferral would starve progress, it admits anyway and relies on refresh
  + Integrator (tiny conflict).
- **Released** at issue CLOSE or on any refresh that changes the plan.

## 5. Folder locks

- A `folder` lock covers a subtree; it is a coarser advisory used when a plan touches "most of a
  directory" (threshold configurable) so the Manager doesn't need to enumerate dozens of files.
- Folder and file locks compose: holding a folder lock implicitly covers its files; the Lock Manager
  detects the overlap deterministically and won't grant a conflicting finer lock.

## 6. Module locks

- A `module` is a plugin-defined logical boundary (a Dart package, a .NET project, a firmware
  component, a KiCad board). Modules are declared by the plugin ([11_Plugins](11_Plugins.md)) and
  discovered deterministically.
- Module locks are the **primary** advisory used for large or cross-cutting changes ("refactor the
  auth module") where file-level locking would be too granular. Two issues targeting the same module
  are serialized.

## 7. Device locks

- **Hard** exclusive lock over a device/board (phone via adb, iOS sim/real device, ESP32 board).
- Prevents two QA/repair runs driving the same board (a real V1 hazard). Integrates with the
  dashboard **Devices** view ([09_Dashboard](09_Dashboard.md)) and the hardware access model
  ([10_Hardware](10_Hardware.md)).
- If the device isn't present, no lock is taken and the check **defers** ([06_QA](06_QA.md)); the
  Lock Manager never blocks waiting for absent hardware.

## 8. Jira issue locks

- **Hard** exclusive lock, `id = issue:<KEY>`. Acquired at CLAIM *before* any Jira transition, so two
  workers can never both claim `SCRUM-123` (I-1, [08_Jira](08_Jira.md)).
- The engine also respects human ownership: an issue assigned to a human or labeled
  `owner-working`/`needs-human` is **not** lockable by the engine (NFR-11).
- Held for the entire issue journey ([03_Workflow](03_Workflow.md)); released at CLOSE / cancel /
  failure.

## 9. Merge locks

- **Hard**, global **per repo**: `id = merge:<repo>`. The Integrator must hold it to advance
  `development` (I-3, FR-8, [07_Git](07_Git.md)).
- Serializes all merges into one repo's `development` so it advances by exactly one verified `--no-ff`
  merge at a time. Held only for the short duration of refresh→verify→merge→delete, then released.
- A multi-repo issue acquires each touched repo's merge lock **in canonical repo order** (alpha by
  repo name) to prevent cross-repo deadlock (§10).

## 10. Deadlock prevention

Deterministic, no detection heuristics needed:

1. **Canonical acquisition order.** Every unit acquires locks strictly in the scope order of §2
   (device < issue < repo < module < folder < file < merge), and within a scope by a canonical key
   order (e.g. lexicographic id). A cycle is impossible when everyone locks in the same total order.
2. **No hold-and-wait across issues for advisory locks.** Advisory locks are acquired *together* at
   admission (all-or-defer); if the full set can't be granted, the Scheduler releases any partial
   grant and re-queues the issue — it never holds some and waits for others.
3. **Bounded waits.** Every wait has a timeout (§ below); on timeout the request is DENIED and the
   issue re-queued, never parked forever.
4. **Merge lock is leaf-level and short.** It is acquired last, held briefly, released promptly.

Result: the wait-for graph is a DAG by construction → no deadlock (proof obligation for the
implementation; verified by test).

## 11. Lock timeout & stealing (fencing)

- **TTL + heartbeat.** Each lock has `ttl_seconds`; the holder heartbeats. `heartbeat_at + ttl < now`
  ⇒ `STALE`.
- **Stealing.** A new requester may **steal** a `STALE` lock atomically (same transaction that
  reassigns `owner` and **increments a monotonic `fence` token**). The previous (dead) holder, if it
  ever wakes, presents an **old fence** and is rejected by every tool — its writes cannot land (this
  prevents a zombie worker from corrupting a stolen resource).
- **Live locks are never stolen.** Only `STALE` locks are reclaimable. A slow-but-alive holder keeps
  heartbeating and keeps its lease.
- **Timeouts are per scope** (config): device (long — a flash may be slow), issue (long — full
  journey, refreshed by heartbeat not by a single ttl window), merge (short), advisory (short).

## 12. Recovery after crash (worker/process)

- A crashed worker stops heartbeating → its locks go `STALE` → reaper (or the next requester via
  steal) reclaims them, with a fence bump so the dead worker can't write.
- The associated `runs` row is returned to the last **stable** stage ([03_Workflow](03_Workflow.md));
  its worktree is cleaned; attempt counter incremented; if attempts exhausted → NEEDS_HUMAN.
- No half-state persists: because `development` only advances under a held `merge` lock via one
  atomic `--no-ff`, a crash can never leave a half-merge (NFR-8, I-3).

## 13. Recovery after reboot (whole machine)

- On `cwv2 serve` startup, the **reconciler** runs before admitting new work:
  1. Load all `locks`. Any whose holder process is gone (no live run) and whose heartbeat is stale ⇒
     reap.
  2. Detect orphan worktrees/branches not backed by a live run ⇒ clean (branch kept if it has an
     unmerged commit that maps to an issue; otherwise removed).
  3. Re-fetch Jira truth and rebuild the issues cache; reconcile in-flight `runs` against it.
  4. Only then resume the Scheduler.
- Because all lock state is **persisted on the SSD** (§15), a reboot loses nothing and cannot
  double-grant.

## 14. Worker cancellation

- **Stop-issue** ([09_Dashboard](09_Dashboard.md)) is a cooperative cancel: signal the worker,
  bump the fence (so any in-flight tool write is rejected), kill the process, clean the worktree,
  release **all** locks the issue held (in reverse canonical order), and return the Jira issue to its
  prior stable status. Idempotent and safe to call repeatedly.

## 15. Lock persistence & SSD storage

- All locks live in SQLite (`locks` table) in `brain.db` per project, under the engine home on the
  **external SSD** (C-6): `/Volumes/Extreme SSD/cwv2-home/projects/<project>/brain.db`.
- SQLite **WAL** mode + a single writer (the engine) makes acquire/steal/release atomic; the
  dashboard reads concurrently. `fsync` on the acquire/steal transaction guarantees a lease survives
  power loss.
- No lock state is kept only in memory; memory is a cache of the DB, reloaded on startup.

## 16. Database schema (delta over [12_Database](12_Database.md))

Extends the existing `locks` table with fencing + scope keys:

| col | type | notes |
|---|---|---|
| id | text pk | `<scope>:<key>`, e.g. `issue:SCRUM-123`, `merge:app`, `file:app/lib/x.dart`, `device:esp32-s3-a` |
| scope | text | `device`\|`issue`\|`repo`\|`module`\|`folder`\|`file`\|`merge` |
| repo | text | repo the lock belongs to (null for device/issue-global) |
| owner | text | run/worker id holding the lease |
| mode | text | `exclusive`\|`shared` (only `repo` uses `shared`) |
| fence | int | monotonic token, incremented on every (re)grant/steal |
| acquired_at | text | |
| heartbeat_at | text | holder updates; stale ⇒ reclaimable |
| ttl_seconds | int | per-scope default from config |
| advisory | bool | true for repo/module/folder/file |
| meta | json | plan link, canonical order key, etc. |

Indexes: `(scope, repo)`, `(heartbeat_at)` for the reaper, unique on `id`.

## 17. APIs (deterministic tools + internal)

Workers use only the tool surface; the Orchestrator uses the internal Go API. Both are deterministic.

**Tool surface (least-privilege, [05_Workers](05_Workers.md)):**
- `lock.acquire(scope, key, ttl?) -> {granted, fence} | {denied, reason}`
- `lock.release(scope, key, fence)`
- `lock.heartbeat(scope, key, fence)`
- `lock.status(scope?, key?) -> [leases]` (read-only; also powers the dashboard)

**Internal Orchestrator API (Go):**
- `AcquireSet(runID, []Request) -> Lease | Deferred` — all-or-defer, canonical order (§10).
- `ReleaseAll(runID)` — reverse order, used at CLOSE/cancel/crash-cleanup.
- `Steal(scope, key) -> Lease` — only if STALE; bumps fence.
- `Reap()` — scheduled sweep of STALE locks + cleanup hooks.
- `Reconcile()` — startup recovery (§13).

**Fencing contract:** every mutating tool (git write, file write, device drive, merge) takes the
current `fence` and the tool layer rejects any call whose fence is behind the DB — so a stale holder
physically cannot mutate a resource it lost.

## 18. Sequence diagrams

### 18.1 Claim an issue (immediate ownership)

```
Scheduler        LockMgr(DB)        Jira
   │  AcquireSet(issue:K)              │
   ├───────────────▶│  INSERT issue:K (fence=1)
   │◀──── Lease ────┤                  │
   │  assign + transition ────────────▶│  (In Progress, assignee=engine)
   │  create worktree from newest development
   ▼  (PLANNED …)
```

### 18.2 Advisory file locks at admission (tiny merges)

```
Scheduler                LockMgr(DB)
  │ plan.files=[a.dart,b.dart]
  │ AcquireSet(file:a.dart, file:b.dart)   (canonical order)
  ├──────────────────────▶│ a.dart free → held(fence)
  │                        │ b.dart HELD by run R2 (live) → contended
  │◀──── Deferred ─────────┤ release a.dart (no hold-and-wait)
  │ re-queue issue; try again after R2 releases (or admit + rely on refresh)
```

### 18.3 Serialized merge

```
Integrator(run R1)     LockMgr(DB)        Git
  │ Acquire(merge:app)      │              │
  ├────────────────────────▶│ held(fence)  │
  │ refresh dev; verify; merge --no-ff ───▶│ development += R1
  │ Release(merge:app) ─────▶│ deleted      │
  │ delete branch + worktree ──────────────▶│
   (R2 waiting on merge:app now proceeds)
```

### 18.4 Crash + steal with fencing

```
Worker W1 (holds file:x, fence=4) ──X crash (no heartbeat)
   ... ttl elapses → file:x STALE
Worker W2 requests file:x → Steal → fence=5 granted to W2
W1 zombie wakes, tries file.write(fence=4) → tool REJECTS (fence<5) → no corruption
```

## 19. Examples

- **Two issues, same file:** `SCRUM-101` and `SCRUM-102` both plan to edit `auth.dart`. Issue locks
  are independent (both proceed to PLANNED), but at admission the advisory `file:auth.dart` lock is
  granted to `101`; `102` is deferred until `101` closes (or admitted with the small-conflict path).
  Result: no lost work, at most a trivial merge.
- **Two issues, same board:** both need the ESP32-S3-A board for on-hardware QA. `device:esp32-s3-a`
  is exclusive; one runs the on-hardware check, the other **defers** it (Wokwi sim can still PASS the
  functional part) — no waiting, no conflict ([10_Hardware](10_Hardware.md)).
- **Owner commits mid-flight:** owner pushes to `development`. The Scheduler's fetch sees it; the
  affected worktree refreshes before its next gate; merge later still needs `merge:<repo>` and a
  clean verify. Owner work is never overwritten (NFR-11).

## 20. Invariants (lock-specific)

- **L-1** A hard lock (`device`/`issue`/`merge`) is required for its operation; the tool layer
  enforces it.
- **L-2** All locks for a unit are acquired in canonical scope+key order (no deadlock, §10).
- **L-3** Only `STALE` locks are stolen, and every (re)grant/steal bumps the `fence`.
- **L-4** A behind-fence mutation is always rejected (no zombie writes).
- **L-5** All lock state is persisted+fsynced on the SSD; memory is only a cache.
- **L-6** No model call ever occurs inside a lock decision (fully deterministic).
