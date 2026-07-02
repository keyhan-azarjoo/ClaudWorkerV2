# S8 — Verification Engine

Implements docs/06 + docs/21 S8, **renamed QA → Verification Engine** (the platform verifies many
kinds of work; human-like UI testing is just one capability). Package: `internal/verify`.

## Scope — verification only

The Verification Engine **only verifies**. It does **not** decide, repair, merge, update Jira, or
manage assignments. It selects verifier plugins, runs them, times/labels the results, and reports what
it found. (Repair is explicitly **not** implemented — that is S9.)

## Plugin model (capability-based, future-proof)

- `Verifier` is the plugin contract: `Name()`, `Type()`, `Capabilities()`, `Verify(ctx, Request)`.
- `Engine.Register(v)` adds a plugin at runtime; `Select(req)` returns plugins whose **Type** matches
  and whose **Capabilities ⊇ the request's required capabilities**.
- **The Assignment Engine requests** verification (a `Type` + required `Capabilities` + target +
  steps + expectations); **the Engine chooses** the appropriate plugin(s).
- **Future-proof:** a brand-new verification kind is added purely by registering a plugin — the core
  Engine is never edited. Proven by `TestFuturePluginNoCoreChange` (registers a `"smell"` type).

Documented types: visual, ui, api, unit, integration, build, device, hardware, pcb, 3d,
documentation. `Type` is an open string, so more can be added freely.

## Verification result (every verifier returns)

`Outcome ∈ {Pass, Fail, Blocked, Deferred, Inconclusive}` plus `Evidence`, `Metrics`, `Duration`,
`Logs` (and `Summary`/`Detail`). The Engine sets `Verifier`, `Type`, and `Duration`; the plugin owns
the verdict. `Aggregate([]Result)` reduces many results to the most-severe outcome
(**Fail > Blocked > Inconclusive > Deferred > Pass**; empty → Inconclusive).

Outcome semantics:
- **Pass** — verified good. **Fail** — observed the work failing (with differences in `Detail`).
- **Blocked** — cannot verify (no capable plugin / environment/visual impossible → invites a headless
  fallback). **Deferred** — postponed. **Inconclusive** — ran/attempted but could not determine (e.g.
  a plugin error or timeout — deliberately *not* a Fail, since we did not observe the work failing).

## Reference plugins shipped

1. **CommandVerifier** (real, deterministic) — runs a command and maps exit status → Pass/Fail; ctx
   timeout → Inconclusive; captures output as logs and `exit_code` metric. Covers the non-interactive
   types (unit/build/integration/api/documentation) by wrapping the project's own commands.
2. **VisualVerifier** (human-like) — drives a real user's journey via a `VisualDriver` seam: **launch,
   navigate, click, type, scroll, pair devices, screenshot, OCR, compare against expectations, verify
   state, report differences**. Concrete drivers (Appium/adb/WebDriver/simulator) are future plugins
   that need no core change. **Headless is used only when visual interaction is impossible**: a driver
   returning `ErrVisualUnavailable` yields **Blocked**, signalling the caller to fall back to a
   non-visual verifier.

## Human-like verification (as mandated)

The visual verifier behaves like a tester: it launches the app/target, performs the interaction steps,
captures a screenshot as evidence, reads the screen via OCR, inspects UI state, compares every
expectation, and reports each difference. Proven by `TestVisualVerifierPassJourney` (launch + 3 steps,
2 expectations, evidence captured) and `TestVisualVerifierReportsDifferences` (2 differences → Fail
with detail).

## Determinism & boundaries

- Selection is a pure function of registered plugins + request (sorted by name); duration uses an
  injectable clock for deterministic tests.
- `internal/verify` is a leaf package (stdlib only) — it imports neither assignment, policy, resource,
  nor lease. It holds no decision/repair/merge/Jira logic.
- No engine rewire: the Assignment Engine will *request* verification from the serve loop; wiring is a
  later orchestration step (consistent with S4–S7). M1 untouched.

## Gates

`gofmt`/`go vet` clean; `go test -race ./...` — **15/15 packages PASS**. Coverage: capability
selection, no-capable-verifier → Blocked, metadata/duration stamping, all five outcomes, aggregation
precedence, future-plugin registration, command pass/fail/timeout, and the full visual journey
(pass / differences / visual-impossible-Blocked / interaction-error-Inconclusive).

## Deferrals (honest)

- **Real visual drivers** (Appium/adb/WebDriver/simulator) are future `VisualDriver` plugins; only the
  contract + a fake driver ship now (no hardware/tokens in tests).
- **Hardware/PCB/3D/documentation verifiers** are future plugins registrable without core change; the
  types and result model are ready.
- **Engine/orchestrator wiring** (Assignment Engine requesting verification, feeding results to the
  future Decision/Repair loop) lands with the serve loop — not S8.
- **Repair is not implemented** (S9), as instructed.
