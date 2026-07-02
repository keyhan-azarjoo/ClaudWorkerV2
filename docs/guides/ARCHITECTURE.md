# Architecture Guide

ClaudWorker V2 is a local-first, deterministic autonomous software-engineering platform. AI runs only
behind one port (the Worker Runtime); everything else is deterministic Go. Architecture is frozen at
spec **v2.1.0** (see `SPEC_VERSION.md`); changes require an ACP.

## Principles

Local-first · Jira-first (work) · Git-first (code) · Knowledge Brain (durable knowledge) · disposable
workers · zero-token deterministic Go · minimal persisted state · feature-completion KPI · no
conflicts · small context · portable.

## Subsystems (packages)

| Subsystem | Package | Role |
|---|---|---|
| Assignment Engine | `internal/assignment` | one Jira issue → lifecycle (claimed→developing→qa→merging→done/failed); minimal persisted state |
| State Store | `internal/assignment` (Store) | durable, storage-agnostic (FileStore/MemoryStore); `spec_version` migration |
| Knowledge Brain | `internal/knowledge` | append-only versioned engineering knowledge; deterministic Prompt Builder (no embeddings) |
| Worker Runtime | `internal/runtime` | disposable `claude -p`; the only token spender; provider-agnostic |
| Policy Engine | `internal/policy` | deterministic policies (retry/runtime/merge/budget/escalation/…); no AI |
| Resource Manager | `internal/resource` | fleet inventory, health, availability, pacing, cooldown, scheduling, failover |
| Lease Manager | `internal/lease` | time-bounded ownership (issue/resource/merge); durable; auto-expiry; crash recovery |
| Verification Engine | `internal/verify` | capability-based verifier plugins; Pass/Fail/Blocked/Deferred/Inconclusive + evidence |
| Improvement Engine | `internal/improvement` | verify→improve loop; policy decides stop; can't loop forever |
| Control Plane | `internal/controlplane` | REST + SSE + auth; owns no business logic |
| Orchestrator | `internal/orchestrator` | the serve loop; wires subsystems; owns only sequencing/events |
| Operations Console | `web/ops-console` | framework-free SPA; a client of the Control Plane API |

Adapters (edges): `internal/adapters/{jira,git,runtime,verify,sim,discovery}`; migration
`internal/migration`; backup `internal/backup`; stress `internal/stress`.

## Data flow (the loop)

```
discover resources → refresh policy(budget) → find eligible Jira → claim (issue lease) →
acquire runtime (Policy→Resource→Lease) → load knowledge → select runtime → run Claude in worktree →
verify → improvement loop (policy decides) → merge (merge lease, --no-ff) → update Jira → release →
publish events → repeat
```

## Laws (enforced)

- **Law 18** — AI only behind the Worker port; everything else deterministic.
- **Law 19** — never lose or redo durable state; validate persisted format; recover deterministically.
- **Law 17** — an interface only when it has ≥2 real implementations.
- **Policy→Resource→Lease** ordering for all resource usage; never bypassed.
- Prompts contain only Assignment, Knowledge Context, Relevant Files, Acceptance Criteria.

## Determinism & modes

Everything but the worker is deterministic. **Simulation Mode** runs the entire loop with no
Claude/Jira/GitHub/hardware — the regression + demo environment. **Live Mode** uses the real edges.

## Dependencies

Single external dependency: `gopkg.in/yaml.v3`. ~10.5k lines production Go, ~5.5k lines tests, 27
packages, acyclic import graph.
