# S6 — Policy Engine

Implements docs/20 + docs/21 S6, renamed **Decision Engine → Policy Engine** to make explicit that
ClaudWorker does not "think": it executes **deterministic policies**. The Policy Engine contains **no
AI** and owns **policies only** — no hidden state, no Git, no Jira, no runtime execution, no QA.

## Package `internal/policy`

An aggregate `Engine` built from `Config`, composed of nine small independent modules:

| Policy | Question it answers | Key inputs |
|---|---|---|
| **Retry** | Retry this failed attempt? | attempts, transient? |
| **RuntimeSelection** | Which runtime satisfies these capabilities? | Capabilities (vision, long-context, preferred) |
| **Merge** | Merge now, and how? | gates passed?, conflicts? |
| **Escalation** | Involve a human? | attempts, blocked, failed, age |
| **Split** | Is the issue too large to do as one Assignment? | estimated units |
| **Defer** | Pause pending an external unblocker? | blocked, missing dependency |
| **Budget** | May work proceed, and on which account? | usage %, tokens today, known?, cooldown |
| **Approval** | Autonomous, or needs human approval? | Automation field value |
| **Failure** | Overall disposition of a failure | composes Retry + Escalation |

## Policy rules — all satisfied

Every policy is:
- **Deterministic** — pure functions over `(config, input)`; identical inputs → identical outputs
  (`TestDeterminism`, 50× stable).
- **Independently testable** — each has its own focused test; no cross-dependencies except the
  intentional composition below.
- **Configurable** — behaviour comes from `Config`; `FromConfig` maps the frozen `config.Config`
  (retry limits, thresholds, merge, usage guard) with no change to the config package.
- **Restart-safe** — policies hold **no mutable/persisted state**. Live signals (attempt counts, usage
  %) are supplied as inputs (from persisted Assignment state / the usage probe), so a restart changes
  nothing.
- **Observable** — every decision carries a `Reason` string.

## The three mandated moves

1. **Retry moved here.** The Assignment Engine no longer encodes the retry cap. It ASKS via the new
   `assignment.RetryDecider` port; `policy.RetryPolicy` implements it. Proven by
   `TestRetryDelegatedToPolicy` (engine `MaxAttempts=999`, policy cap 2 → stops at 2). Backward
   compatible: with no policy wired, the engine falls back to `MaxAttempts` so all frozen M1 tests pass
   unchanged.
2. **Runtime selection moved here.** `RuntimeSelectionPolicy.Select(Capabilities)` chooses the runtime
   deterministically (preference → order → default). The Assignment Engine requests *capabilities* and
   never names Claude; the orchestration layer injects the chosen runtime into the engine's `Worker`
   slot.
3. **Budget centralised here.** `BudgetPolicy` is the single home for every usage rule: usage guard,
   pause, resume/cooldown hysteresis, daily token limits, account selection, fail-open/closed on
   unknown usage. This replaces scattered runtime decisions. Default `PausePct=95` matches the owner's
   usage-guard convention; it never queries usage itself (input-driven → deterministic & restart-safe).

## Deterministic-first / no-AI

The Policy Engine does zero AI work and spends zero tokens — it is pure Go decision logic (Law 18).
`FailurePolicy` **composes** `RetryPolicy` + `EscalationPolicy` instead of duplicating their thresholds
(one place to turn a failure into `retry`/`defer`/`escalate`/`fail`).

## Boundaries respected

- No new external dependency (module still has one: `gopkg.in/yaml.v3`).
- `policy` imports only `config` (leaf) — no cycle; `assignment` does **not** import `policy` (the
  engine depends on its own `RetryDecider` port, which `RetryPolicy` satisfies structurally).
- The engine change is additive (one field + one port + a fallback helper); M1 behaviour preserved.

## Gates

`gofmt`/`go vet` clean; `go test -race ./...` — **12/12 packages PASS**. Coverage of `policy`: every
policy has direct decision tests plus determinism, config-mapping, and default-application tests.

## Notes / deferrals

- **Escalation/Failure vs age & blocked signals** are ready but only consumed once the orchestration
  (serve) loop and Lock/Defer subsystems exist — the policies are complete and unit-proven now.
- **Config additions** (daily token limit, account list, per-runtime capability map) live in
  `policy.Config` with defaults; they are not yet surfaced in `config.Config` (frozen). Surfacing them
  is a later, additive config change when the serve loop needs them.
- Nothing in S7+ was started.
