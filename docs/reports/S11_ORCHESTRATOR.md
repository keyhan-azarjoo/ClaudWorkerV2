# S11 — Orchestrator (Serve Loop)

Implements docs/21 S11. The Orchestrator is the heart of ClaudWorker V2: it owns the **execution
loop** and nothing else — startup, shutdown, scheduling, event flow, orchestration, and subsystem
wiring. Package: `internal/orchestrator`.

## It connects; it never duplicates

The Orchestrator holds the real subsystem **instances** (Resource Manager, Policy Engine, Lease
Manager, Knowledge Brain, Verification Engine, Assignment Store, Control Plane) and small **ports** for
the external/non-deterministic edges (`Jira`, `Developer`, `Verifier`, `Merger`) so deterministic
fakes stand in when a service is unavailable. Every step calls the existing subsystem; the
Orchestrator adds only sequencing + event publishing. The verify→improve loop is delegated to the S9
Improvement Engine, which itself composes S8 (verify) and S6 (policy) — no loop logic is re-written.

## The main loop (each step = an existing subsystem)

```
Recover ─► [ Refresh Policy(budget) ─► Find eligible Jira ─► Claim Assignment ─►
            Acquire leases (Policy→Resource→Lease) ─► Load Knowledge Context ─►
            Select Runtime ─► Run Worker ─► Verify ─► Improvement loop ─►
            Policy decision ─► Merge ─► Update Jira ─► Release resources ─►
            Publish events ] ─► drain ─► block until Notify()/shutdown ─► Repeat
```

- **Refresh policy / budget** — `Policy.Budget.Decide`; paused ⇒ don't even claim.
- **Find eligible / claim** — `Jira.Eligible`, persist `assignment.Assignment`, acquire the **issue
  lease**, move Jira in-progress, publish `AssignmentCreated` + `LeaseGranted`.
- **Acquire runtime** — strictly **Policy → Resource → Lease**: `Policy.Budget` → `Resources.Reserve`
  → `Leases.Acquire(resource)`. Never bypassed (`acquireRuntime`).
- **Knowledge** — `Knowledge.SelectContext`/`RenderContext` (deterministic, zero tokens).
- **Select runtime** — `Policy.RuntimeSelection.Select` (the engine never names Claude directly).
- **Run worker** — `Developer.Develop` (the only non-deterministic edge; wraps the Worker Runtime).
- **Verify + Improvement loop** — `improvement.Engine.Run` with adapters over `Verifier` (S8) and
  `Developer` (S9 improver) and `policy` (S6 stop decision).
- **Merge** — acquire the **merge lease** (Policy→Resource→Lease), `Merger.Merge`, then release it.
- **Update Jira / release** — transition Done/comment; release the runtime lease + reservation and the
  issue lease.
- **Publish events** — at every transition (see Observability).

## Event-driven — no busy waiting

`Run` drains all eligible work, then **blocks** on a trigger channel or context cancellation
(shutdown). `Notify()` (non-blocking) wakes it when new work arrives. There is no polling and no busy
loop. `ProcessOnce` handles exactly one issue for step-wise/testable execution and the
`orchestrator.tick` Control Plane command.

## Restart / recovery

`Recover` (run at startup): **reap expired leases**, list persisted assignments, **skip terminal**
ones (never restart completed work, Law 19), and **resume** each unfinished one (re-fetching the issue
from Jira; leases re-acquire idempotently for the same owner). Proven by
`TestRecoverySkipsTerminalResumesUnfinished` (a Done assignment is untouched; a Developing one resumes
to Done).

## Resource-usage discipline

`Policy → Resource → Lease`, always, encoded in `acquireRuntime`/`mergeAndClose`. When no healthy
resource is available the pipeline **defers before running the worker and takes no resource lease**
(`TestPolicyResourceLeaseGating`).

## Observability — the Timeline

Every significant transition publishes to the Control Plane bus: `AssignmentCreated/Completed`,
`LeaseGranted/Expired`, `RuntimeStarted/Finished`, `KnowledgeUpdated`, `VerificationStarted/Finished`,
`PolicyDecision`, plus `MergeCompleted`, `AssignmentResumed`, `AssignmentDeferred`. The Logs/Timeline
page is the chronological sum of these events.

## Control Plane goes live

`RegisterControlPlane` registers, all delegating to subsystems (no logic in the Control Plane or the
registration):
- **Queries:** `assignments.list`, `leases.active`, `resources.snapshot`, `accounts.list`,
  `runtimes.list`, `knowledge.list`, `policies.decisions`.
- **Commands:** `orchestrator.tick`, `leases.reap`.
- **Status:** `orchestrator` (running, last issue).
- **Metrics:** `counters`, `leases` (active by kind), `resources` (by availability).

`TestControlPlaneGoesLive` drives one issue then reads `assignments.list`, `status`, `metrics` and the
`orchestrator.tick` command back through the HTTP API — the Operations Console now populates from real
data.

## End-to-end proof

`TestAutonomousClaimToCompletion`: with real subsystems + deterministic fakes for Jira/worker/merge,
the platform **autonomously processes a Jira issue from claim to completion** — assignment reaches
Done, the worker ran, Jira moved In-Progress→Done, the resource and all leases were released, and the
Timeline events were published. `TestImprovementLoopRunsUntilPass` confirms a failed verification
drives another worker iteration until it passes.

## Gates

`gofmt`/`go vet` clean; `go test -race ./...` — **18/18 packages PASS**.

## Boundaries / deferrals (honest)

- The Orchestrator imports many subsystems (expected — it is the wiring layer); no subsystem imports
  it (no cycles).
- **Real edge adapters** (jira.Client→`Jira`, runtime.Runner→`Developer`, verify.Engine+driver→
  `Verifier`, git.Git→`Merger`) and a `cwv2 serve` command that constructs the Orchestrator from
  config are the remaining wiring; the ports + deterministic fakes prove the whole loop today, per
  "use deterministic fakes where external services are unavailable".
- **Production packaging / deployment** — intentionally **not** started.
