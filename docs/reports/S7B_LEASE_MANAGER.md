# S7B — Lease Manager

Implements docs/15 + docs/21 S7B, **renamed Lock Manager → Lease Manager**. Ownership is modelled
with **time** (a lease), not exclusion-only (a lock). Package: `internal/lease`.

> **Lock** = exclusion only. **Lease** = ownership *with time* → crash recovery, automatic cleanup,
> resource reclamation, restart safety, and ownership transfer fall out naturally.

## Responsibilities (all delivered)

Issue Lease · Resource Lease · Merge Lease · renewal · expiration · persistence · recovery · transfer
· validation — and nothing else.

| Operation | Method |
|---|---|
| Acquire (with reclamation) | `Acquire(Request)` |
| Renewal | `Renew(kind, resource, owner, ttl)` |
| Release | `Release(kind, resource, owner)` |
| Transfer | `Transfer(kind, resource, from, to, reason)` |
| Validation | `Validate(kind, resource, owner)` |
| Expiration/recovery | derived from `ExpiresAt`; `Active()`, `Reap()` |
| Persistence | `FileStore` (atomic temp+fsync+rename) + `MemoryStore` behind `Store` |

## Separation from the Resource Manager (S7A)

- **Resource Manager** answers *"what resources exist?"* (inventory, health, availability).
- **Lease Manager** answers *"who currently owns them, until when?"* (durable, time-bounded ownership).

They are **completely separate**: `internal/lease` is a leaf package (stdlib only) and imports neither
`resource` nor `assignment`. A lease's `Resource`/`Owner` are opaque IDs (resource id / issue key /
merge target / owning Assignment), so ownership and inventory never entangle.

## Design-rule compliance

Every `Lease` contains exactly the mandated fields — **Resource, Owner (the Assignment), CreatedAt,
ExpiresAt, Renewable, Reason** — plus `ID`/`Kind` and a `SpecVersion` format-metadata field (same
sanctioned pattern as S3/S4). Expiry is a **pure function of timestamps**, so **no expired lease ever
needs human intervention** and **recovery is deterministic**.

---

## 1. Lease lifecycle report

```
Acquire ── grant (free / expired→reclaimed / idempotent same-owner)
   │            │
   │            ├── Renew ──► extend ExpiresAt (active + owner + renewable only)
   │            ├── Transfer ► reassign owner, reset expiry (active + from-owner only)
   │            └── Validate ► active && owner matches
   ▼
 ExpiresAt reached ──► lease inactive (ownership lapses automatically)
   │
   ├── another owner Acquire ► reclaims deterministically (no human)
   └── Reap / Release ─────► record deleted (automatic cleanup)
```

- **One active lease per `(kind, resource)`** (id = `"<kind>/<resource>"`).
- Acquire succeeds iff the resource is free, its lease has expired (reclamation), or it is already the
  caller's (idempotent). A *different* active owner ⇒ `ok=false`. Proven: `TestAcquireExclusiveWhileActive`.
- Renewal only extends an **active, renewable, owner-matched** lease (`TestRenewExtendsActiveOnly`).
- Transfer moves an **active** lease from the correct current owner and resets expiry
  (`TestTransferOwnership`).
- Release deletes only the caller's own lease (`TestReleaseOnlyByOwner`).

## 2. Crash-recovery validation

`TestCrashRecoveryReclamation`: owner **A** acquires a resource lease (TTL 1 min) and *crashes* —
never releasing. Time advances 90 s. Owner **B** calls `Acquire` and **reclaims** the resource with
**no human step and no manual cleanup** — because ownership validity is derived from `ExpiresAt`, an
abandoned lease is simply inactive and thus acquirable. Result: `ok=true`, owner becomes B. This is
the core advantage of lease over lock semantics.

## 3. Lease-expiration validation

`TestExpirationFreesOwnership` + `TestReapDeletesExpired`:
- Before expiry: `Validate` is true, `Active()` includes the lease.
- After expiry (clock advanced past TTL): `Validate` is false, `Active()` excludes it — with **no
  mutation required** (expiry is computed, not scheduled).
- `Reap()` deletes only expired leases (automatic cleanup / reclamation), leaving active ones intact
  (1 reaped, active one retained).

## 4. Restart validation

`TestRestartFromDisk`: a brand-new `Manager` + `FileStore` over the **same directory** (a genuine
reload from disk) recovers persisted ownership:
- an active lease is still owned after "restart" (`Validate` true);
- a different owner still **cannot** steal the active recovered lease;
- once the clock passes expiry, the lease becomes reclaimable post-restart.
Persistence uses atomic writes (temp+fsync+rename); a newer on-disk format is **rejected**, never
silently ignored (`TestStoreRejectsNewerFormat`), matching the S3/S4 recovery policy.

## 5. Simplification review

- **Reused the established store pattern** (interface + FileStore + MemoryStore + `migrate`) rather
  than inventing a new persistence approach — one mental model across assignment/knowledge/lease.
- **No new abstraction beyond need**: a single `Manager` + `Lease`; the three lease kinds share one
  code path (namespaced by `Kind`), not three implementations.
- **Derived, not stored, validity**: "active" is computed from `ExpiresAt`, so there is no separate
  status field, no scheduler, and no reaper *required* for correctness (`Reap` is optional
  housekeeping). This removes a whole class of state to keep consistent.
- **Leaf package, zero new deps**: stdlib only; module still has one external dependency.
- **No engine rewire**: the Assignment Engine's existing transient issue-skip is untouched (M1
  preserved). Wiring durable Issue/Merge leases into the engine's claim/merge is an orchestration step
  for the serve loop — intentionally deferred (consistent with S4/S5/S7A), keeping S7B self-contained.
- Complexity trend: **flat/down** — a new leaf subsystem, no growth in existing packages.

## Gates

`gofmt`/`go vet` clean; `go test -race ./...` — **14/14 packages PASS**. Determinism via an injected
clock; both `Store` implementations exercised.

## Deferrals (honest)

- **Engine/orchestrator wiring** (issue lease on claim, resource lease around runtime use, merge lease
  around merge, background `Reap`) lands with the serve loop — not S7B.
- **TTLs from config** use documented defaults now (issue 30 m / resource 15 m / merge 10 m); mapping
  from `config.Config` is an additive step when the serve loop needs it.
- Nothing in S8 was started.
