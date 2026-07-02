# Phase 2 — Integration #3: Real Worker Runtime (Claude Code)

Architecture frozen. This iteration replaces **exactly one** more edge — the simulated worker — with
the real **Claude Code** runtime. The runtime stays provider-agnostic (Claude is the first provider).
Validated with a fake `claude` binary (real process lifecycle, **zero tokens**).

## Architecture impact

| Added / changed | Role |
|---|---|
| `internal/adapters/runtime` (`runtimeadapter`) | Real Worker Runtime: executes `claude -p` in the assignment worktree under the Resource-Manager-selected account; error classification; infra-only retry; account cooldown/failover signalling; metrics. Implements `gitadapter.WorktreeWorker`. |
| `internal/runtime.ClaudeWorkerRuntime` | **Extended** (additive): `Dir` (run in the worktree) + `Env` (account env, e.g. `CLAUDE_CONFIG_DIR`). No behaviour change when unset. This IS the Worker Runtime — extending it is in scope. |
| `internal/adapters/git` | `Developer` now wraps a **`WorktreeWorker`** (worktree-aware) — the seam anticipated in the 2.2 report; `FromDeveloper` adapts a plain developer (sim). |
| Ops console **AI Runtimes** page | Live runtime state (executions, accounts, durations, tokens, retries, cooldowns, failover). |

### Changes to other subsystems (documented, minimal)

- **Orchestrator:** `DevInput` gained an `Account` field, set from the account `acquireRuntime`
  reserved. **Why required:** multi-account execution — the worker must run under the account the
  Resource Manager selected; no existing field carried it. **Minimised:** one string field + two
  assignments; the prompt is unaffected (Account/Runtime are routing, never prompt inputs). The
  Assignment Engine and Policy Engine are **untouched**.
- Nothing else changed.

## Separation maintained

`Assignment requests capabilities → Policy chooses the runtime → Resource Manager selects the account
→ Worker Runtime executes.` The runtime **never** chooses an account; on a rate-limit/auth failure it
emits a **health signal** (`Cooldown`) to the Resource Manager, which then selects a different account
on the next reservation. This is health monitoring, not selection.

## Prompt discipline

The prompt is built by `internal/runtime.BuildPrompt` from **only** the four permitted inputs
(Assignment = issue+summary, Acceptance Criteria, Knowledge Context, Relevant Files — the CLI reads
the worktree directly, so Relevant Files stays empty). **Execution / Git / Policy / Lease / Account /
Runtime state never enter the prompt.** Guarded by the S5 prompt tests + the `DevInput` doc contract.

## Disposable sessions

A fresh process per call, no session reuse, no resume, no hidden context — every execution starts from
a clean `claude -p` process in a clean worktree.

## Error handling (classification)

| Class | Trigger | Handling |
|---|---|---|
| `success` | exit 0, contract-valid, OK | commit + continue |
| `semantic` | exit 0, OK=false | returns OK=false (worker declined) → Policy |
| `infrastructure` | spawn/network markers | **retried by the runtime** (only this class) |
| `authentication` | 401 / not-logged-in | cool account, return to Policy |
| `rate_limit` | 429 / quota / overloaded | cool account (failover), return to Policy |
| `timeout` | per-attempt deadline | return to Policy |
| `cancellation` | parent ctx cancelled (shutdown) | return to Policy |
| `runtime_failure` | other non-zero exit | return to Policy |

Only infrastructure is retried by the runtime; everything else returns to the Policy Engine.

## Validation (fake `claude` CLI — zero tokens)

Proven: successful execution (edits the worktree, real commit), timeout, cancellation, rate-limit →
**account cooldown**, authentication failure, infrastructure retry (retry count asserted), CLI failure
/ crash (non-zero exit), classification table, restart/disposability (fresh process per call),
**failover through the Resource Manager** (rate-limit cools acct-a → next reservation picks acct-b),
and **account exhaustion** (all cooled → no reservation → loop defers).

## Production validation — real end-to-end

`TestProductionFlowRealClaude` runs the whole loop with **real Git + the real Claude runtime**:

```
Jira → Assignment → real Git (branch+worktree) → REAL Claude (edits feature.txt in the worktree)
     → Verify → Improve → real --no-ff Merge → Jira Done → cleanup
```

Asserts the Claude-edited file is committed and **really merged onto `development`**, the assignment
reaches **Done**, and runtime metrics are captured. The autonomous loop now uses the real Claude
runtime.

## Runtime metrics (Control Plane)

Per execution: issue, account, runtime, duration, prompt/completion bytes, token estimate, retries,
class. Aggregate `runtime.state`: active executions, cooldowns, failover events, recent executions.
Exposed at `runtime.state` and via `RuntimeMetrics` events; the console AI Runtimes page renders them.

## Account metrics

Accounts are Resource-Manager resources (health, availability, usage, cooldown). The runtime's
cooldown signal drives account rotation/failover; `accounts.list` (console) shows health/usage/cooldown
per account.

## Failover validation

`TestFailoverThroughResourceManager`: acct-a rate-limited → runtime cools it via `res.Cooldown` →
`res.Reserve` fails over to acct-b. The runtime signalled; the Resource Manager selected.

## Restart validation

- Workers are stateless/disposable — nothing to recover in the runtime itself.
- Live mode persists assignment/lease/knowledge in durable stores; `Orchestrator.Recover` resumes
  unfinished assignments; account cooldowns are in-memory resource state (rebuilt by discovery), which
  is correct (a restart clears transient cooldowns, and a still-limited account simply re-cools on the
  next attempt).

## V1 migration matrix (no mature Claude-account capability lost)

| V1 capability | V2 home | Status |
|---|---|---|
| Multiple Claude accounts | Resource Manager (account resources) + runtime account env | ✅ |
| Account rotation | Resource Manager LRU/lowest-usage selection | ✅ (S7A) |
| Pacing | Resource Manager usage-ordered selection | ✅ (S7A) |
| Usage guard | Policy Engine BudgetPolicy (pause ≥ threshold) | ✅ (S6) |
| Cooldowns | Resource Manager cooldown windows; runtime signals on rate-limit/auth | ✅ |
| Scheduling | Resource Manager deterministic candidate ordering | ✅ (S7A) |
| Health monitoring | Resource Manager health + runtime error → cooldown signal | ✅ |
| Failover | Resource Manager skips cooled/unhealthy → next account | ✅ (validated) |

The V1 concepts are recovered through **Policy → Resource → Worker**, not by recreating V1's coupled
architecture: the runtime executes and reports health; the Resource Manager rotates/paces/fails over;
the Policy Engine guards usage.

## Complexity review

- **No new subsystem, no ACP.** Reused S5 `ClaudeWorkerRuntime` + S6/S7A policy/resource; added one
  adapter + one worktree-worker seam + one optional `DevInput` field.
- **No new external dependency.**
- Net complexity: **equal** — the runtime edge is now real behind the same port; simulation retained.

## Regression review

- `gofmt` / `go vet` clean; **`go test -race ./...` — 22/22 packages PASS.**
- **Simulation Mode still works** (`serve --once simulation` → processed; all prior tests green,
  including S11 orchestrator + Phase-2.1/2.2 suites).
- Ops-console JS validated (`node --check`).

## Remaining simulated after this phase

Verification Drivers · Resource Discovery. Everything else (Jira, Git incl. merge, **Worker Runtime**)
is real.

## Stop

Iteration #3 complete. **Next (do not start yet): #4 Real Verification (visual drivers, build, API).**
Stopping for review.
