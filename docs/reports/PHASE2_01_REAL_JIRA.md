# Phase 2 — Integration #1: Real Jira Adapter (+ Live/Simulation modes)

Architecture frozen; no new subsystems. This iteration replaces **exactly one** deterministic fake —
**Jira** — with a real implementation, and establishes the **Live / Simulation** mode split. The
platform stays fully functional.

## What changed

| Added | Role |
|---|---|
| `internal/adapters/jira` (`jiraadapter`) | **Real Jira adapter** over `internal/jira.Client` — search, claim (transition), comments, labels, Automation field, attachments. Implements the frozen `orchestrator.Jira` port. |
| `internal/adapters/sim` | Deterministic **Simulation-Mode** adapters (Jira/Developer/Verifier/Merger). The fakes are **kept**, now as a runtime mode — the regression + demo environment. |
| `cmd/cwv2/serve.go` (`cwv2 serve`) | Runs the Orchestrator + Control Plane. `--mode live\|simulation`, `--bind`, `--web`, `--once`, graceful SIGINT/SIGTERM shutdown. |

Only the Jira edge is real in live mode; Worker/Verify/Merge/Resource-discovery remain simulated until
their iterations (#3–#6), so live mode is fully functional today.

## Real Jira adapter — capabilities

`Eligible` (search WorkJQL + enrich acceptance criteria) · `Get` (rehydrate on recovery) ·
`Transition` (claim → In Progress, close → Done) · `Comment` · `AddLabels`/`RemoveLabels` ·
`Automation` (read the single-select gate) · `Attachments` · `Queue` (eligible issues + status +
Automation → the console's live Jira page). All are thin maps to `jira.Client`; no business logic.

## Live vs Simulation

- **Simulation** — in-memory stores, `sim.*` adapters, **no credentials, no Claude/Jira/GitHub/
  devices/hardware**. `cwv2 serve --mode simulation` drove the full demo backlog to `done` end-to-end.
- **Live** — durable file stores (assignments/leases/knowledge under the engine home) + the **real
  Jira adapter** (base URL from config, email/token via the secrets resolver). Remaining edges
  simulated for now.

## Control Plane — going live

`cwv2 serve` mounts the Control Plane API at `/v1` and (optionally) serves the Operations Console at
`/`. Verified live over HTTP: `/v1/healthz`, `/v1/query/assignments.list` (returned the completed
`SIM-1..3`), `/v1/status`, `/v1/command/orchestrator.tick`, and the console `index.html`. In live mode
a `jira.queue` query backed by the real adapter makes the console's **Jira page real** — no fake data
remains for the Jira surface once this adapter is complete.

## Integration rules — satisfied

- **Exactly one fake replaced** (Jira). Worker/Verify/Merge/Resource untouched.
- **gofmt / go vet clean; `go test -race ./...` — 20/20 packages PASS.**
- **End-to-end validation:** `TestPlatformFunctionalWithRealJira` swaps the real Jira adapter (real
  HTTP client against a Jira-shaped server) into the orchestrator with simulated edges and drives an
  issue **claim → completion** (`SCRUM-1` → Done). Adapter unit tests cover eligible/get/transition/
  comment/labels/queue against the real client.

## Complexity rule — equal or simpler

- **No new subsystem, no ACP needed** — the `orchestrator.Jira` port already existed; this only adds an
  adapter behind it.
- **No new external dependency** (module still: `gopkg.in/yaml.v3`).
- The `sim` package **removes** the need for ad-hoc runtime fakes and gives one home for Simulation
  Mode. Net structural complexity: **equal** (one port, two implementations — real + sim).

## Performance

| Metric | Value |
|---|---|
| `cwv2 serve` startup + one orchestration step (simulation) | **< 10 ms** |
| Full backlog (3 issues) claim→done at boot | sub-second (in-memory) |
| Assignment throughput | bounded only by the (simulated) worker; loop overhead negligible |
| Jira adapter per-issue cost | 1 search + N acceptance-criteria fetches (N = queue size), real HTTP |
| Memory / CPU | unchanged from S11 (no heavy state; stores are small file/JSON) |

No regressions vs S11 (all prior package timings within noise).

## Production validation

| Property | Evidence |
|---|---|
| Restart safety | Live mode uses durable `FileStore`s; `Orchestrator.Recover` runs on startup. |
| Crash recovery | `assignment` + `lease` `FileStore` restart-from-disk tests (S3/S7B) + `TestRecoverySkipsTerminalResumesUnfinished`. |
| Lease recovery | `Recover` reaps expired leases; durable lease store survives restart; expired leases reclaim with no human step. |
| Assignment recovery | `Recover` resumes non-terminal assignments (re-hydrated from Jira via `Get`) and **never restarts completed work**. |
| Deterministic behaviour | Simulation Mode is fully deterministic (no Claude/network/hardware); adapter maps are deterministic; the loop is pure Go apart from the Developer edge (still simulated). |

## Stop

Iteration #1 complete. **Next (do not start yet): #2 Real Git Adapter.** Stopping for review.
