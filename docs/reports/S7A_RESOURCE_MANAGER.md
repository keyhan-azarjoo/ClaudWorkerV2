# S7A — Resource Manager

Implements docs/21 S7A (the S7 split: **S7A Resource Manager** + S7B Lock Manager). The Resource
Manager owns RESOURCES; it contains **no ownership logic and no locking**.

## Package `internal/resource`

An in-memory `Manager` over a set of `Resource`s. Deterministic, concurrency-safe, injectable clock.

### Resource kinds
`claude_account`, `codex_account`, `local_runtime`, `android_device`, `iphone`, `esp32`, `mac_mini`,
`build_machine`, `git_worktree` (extensible — new kinds need no behaviour change).

### Responsibilities (all delivered)

| Responsibility | API |
|---|---|
| Discovery | `Discoverer` interface + `StaticDiscoverer`; `Discover()` reconciles found resources |
| Registration | `Register` / `Deregister` |
| Health | `SetHealth`; `Health` ∈ {unknown, healthy, degraded, down} |
| Availability | `AvailabilityOf` derives {available, reserved, cooldown, offline} |
| Reservation | `Reserve(holder, filter)` (best available, atomic) / `ReserveID` |
| Release | `Release` |
| Metrics | `SetUsage`, `RecordUse`, `RecordFailure`; per-resource `Metrics`; `Snapshot()` |
| Scheduling metadata | `Labels` + deterministic `Select`/candidate ordering |

### The boundary (why no locking here)

Reservation is a **transient, in-memory availability marker** used for scheduling. It is **not** a
durable lock: there is no persistence, no TTL, and no crash recovery. Those — plus issue lock, merge
lock, and durable *resource ownership* — are the **Lock Manager (S7B)**, which will build on this
inventory. On restart the inventory is rebuilt by discovery and transient reservations simply vanish,
which is correct: durability is S7B's job. The `reservedBy` holder is kept only for
scheduling/observability, not as an ownership record.

## V1 migration — concepts recovered, architecture not

The mature V1 behaviours were folded in as resource **state + mechanics**, simplified:

| V1 concept | Here |
|---|---|
| health monitoring | `Health` + `SetHealth` (+ pluggable `Discoverer` for live probes) |
| account pacing | lowest-usage-first + least-recently-used selection ordering |
| cooldowns | `Cooldown(id, until)` window → derived `Cooldown` availability |
| failover | `Reserve`/`Select` deterministically skip reserved/cooling/down and pick the next best |
| scheduling | deterministic candidate order: health → usage → LRU → id |
| usage guard | usage stored per account (`SetUsage`) as an INPUT; the pause **decision stays in the Policy Engine's BudgetPolicy (S6)** — not duplicated here |

Crucially, the **decision** (should we pause / which account preference) remains the Policy Engine's;
the Resource Manager supplies the state and executes the mechanical selection. This preserves V1
behaviour without recreating V1's tangled ownership+decision+resource coupling.

## Determinism & restart-safety

- Selection is a pure function of resource state (health, usage, LastUsed, id) → identical state
  yields identical choice (`TestSchedulingOrder`, `TestFailoverSkipsUnavailable`, `TestLRURotation`).
- No persisted state; discovery rebuilds inventory; `Discover` preserves live metrics/reservations on
  refresh (`TestDiscoverReconcilesPreservingMetrics`).

## Boundaries

- No new external dependency (module still: `gopkg.in/yaml.v3`).
- `resource` imports only stdlib — a leaf package, no coupling to assignment/policy/runtime.
- No CLI added (like the Policy Engine): the Manager is a library the orchestrator + dashboard (S10) +
  Lock Manager (S7B) consume. `Snapshot()` is the observability surface.

## Gates

`gofmt`/`go vet` clean; `go test -race ./...` — **13/13 packages PASS**. Coverage: registry, copy
isolation, health/usage, availability precedence, transient reserve/release, cooldown, deterministic
scheduling + failover, LRU rotation, discovery reconciliation, snapshot.

## Deferrals (honest)

- **Live probers** (adb device scan, claude account list, `git worktree list`) are future
  `Discoverer` implementations; only `StaticDiscoverer` ships now (no hardware/tokens in tests).
- **Resource declarations in config** are not yet surfaced (`config.Config` is frozen); wiring a
  declared-resources source is an additive step when the serve loop needs it.
- **Durable ownership / TTL / crash recovery / persistence** are intentionally **S7B** — not started.
