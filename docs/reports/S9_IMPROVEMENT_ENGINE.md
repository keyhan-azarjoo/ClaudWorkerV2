# S9 — Improvement Engine

Implements docs/17 + docs/21 S9, **renamed Repair Loop → Improvement Engine** (the platform improves
software, not only repairs failures). Package: `internal/improvement`.

## Inputs — exactly four

The improve step receives **only** `Assignment`, `Verification Results`, `Knowledge Context`,
`Relevant Files` (`ImprovementInput`) — nothing else (no execution state, Git, Jira, or policy
internals). Proven by `TestReceivesOnlyFourInputs`.

## Responsibilities

The Improvement Engine **may** improve across categories — defect, reliability, performance, UX,
maintainability, accessibility, security, documentation (`Category` constants, reported by the
Improver for observability). It does **not** verify, merge, update Jira, own Assignment state, or own
policies — each is delegated to a port or lives elsewhere.

## The loop (delegated everywhere it must be)

```
verify ─► pass? ─► DONE (passed)
   │ no
   ▼
ask Policy ─► defer/escalate/fail ─► terminal
   │ continue
   ▼
improve (Worker Runtime) ─► verify ─► …
```

Ports (dependency inversion keeps the engine deterministic and decoupled):
- **Verifier** → the S8 Verification Engine (the engine does **not** verify itself).
- **Improver** → wraps the Worker Runtime (S5) — the **only non-deterministic** step.
- **StopDecider** → the S6 Policy Engine (`PolicyDecider` adapter over `FailurePolicy`).

**The Improvement Engine never decides when to stop.** The loop terminates only when:
- verification **passes** (an observed fact, not a decision), or
- the **Policy** defers, escalates, or fails.

`TestPassFirstNoImprovement`, `TestImproveThenPass`, `TestPolicyDeferStops`,
`TestPolicyEscalateStops`, `TestPolicyFailStops`, and `TestPolicyAdapterOwnsStopDecision` (wires the
**real** Policy Engine — retry cap 2 stops the loop well before the safety ceiling) prove this.

## Design rules — all satisfied

| Rule | How |
|---|---|
| Deterministic except the Worker Runtime | The loop is pure Go over port outputs + an injectable clock; only `Improver` is non-deterministic. |
| Never hide retries | Every iteration is an `IterationRecord` in `Progress.Records` — including improve *errors* (`TestImproveErrorRecordedNotHidden`). |
| Every iteration = measurable progress | `Delta` = failures reduced vs the previous iteration (`TestMeasurableProgressDelta`). |
| Detect repeated identical failures | A SHA-256 `Signature` of the non-passing results; `StuckThreshold` identical repeats with no progress raise `Stuck`, surfaced to the policy (`TestStuckDetection`). |
| Infinite loops impossible | A hard `MaxIterations` ceiling always terminates, even under a misconfigured always-continue policy (`TestSafetyCeilingGuaranteesTermination` → `StatusExhausted`). The ceiling is a **safety invariant**, not a work decision — the policy is the intended decider. |

## Progress (observable)

`Progress` = iteration count, cumulative unique changed files, final outcome, elapsed, and a per-
iteration `Records` list; each `IterationRecord` carries N, outcome, failures, delta, changed files,
category, reason, elapsed, and the failure signature. Nothing is swallowed.

## Boundaries

- Imports only `verify` (result types) and `policy` (the adapter) — both leaf-ish; no cycles
  (`verify`/`policy` do not import `improvement`).
- Holds no Assignment persistence, no Git/Jira, no policy thresholds (those live in `policy`).
- No engine rewire; the serve loop will own the verify/improve wiring later. M1 untouched.

## Gates

`gofmt`/`go vet` clean; `go test -race ./...` — **16/16 packages PASS**. Coverage: pass-first,
improve-then-pass, all three policy stops, safety ceiling, stuck detection, measurable delta,
improve-error recording, four-inputs-only, and real-policy ownership of the stop decision.

## Deferrals (honest)

- **Real Improver** (worker-runtime-backed: build the improvement prompt from the four inputs, run the
  runtime, apply files to the worktree, report changed files) is wired by the serve loop — S9 ships the
  loop + the port + a tested fake. The Worker Runtime (S5) and prompt builder already exist.
- **Real Verifier** is the S8 Verification Engine adapted to the port at wiring time.
- Nothing in S10 (Dashboard) was started.
