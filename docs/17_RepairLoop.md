# 17 — Repair Loop

The Repair Loop is the heart of ClaudWorker V2. It is the single, uniform way the platform turns "not
yet passing" into "passing" for **every** project type. The philosophy is identical everywhere; only
the deterministic **observations** and **verifications** differ (supplied by plugins,
[11_Plugins](11_Plugins.md) / [18_PluginContract](18_PluginContract.md)).

> **One philosophy, all domains:** Observe → Analyse → Repair → Verify → (pass? stop : repeat),
> bounded and escalating. Flutter, .NET, a website, a REST API, ESP32 firmware, a PCB, a 3D part,
> documentation, and infrastructure all use the *same* loop with *different tools*.

## The universal loop

```
        ┌───────────────────────────────────────────────┐
        │                                               │
        ▼                                               │
   ┌─────────┐   ┌─────────┐   ┌────────┐   ┌────────┐  │ not passing
   │ OBSERVE │──▶│ ANALYSE │──▶│ REPAIR │──▶│ VERIFY │──┘ (attempt++, ≤ maxAttempts)
   └─────────┘   └─────────┘   └────────┘   └────────┘
        ▲             │             │            │ passing
   deterministic   reasoning     reasoning    deterministic
   (tools, 0 tok)  (small AI)    (Developer)  (gates, 0 tok)     │
                                                                 ▼
                                                              DONE (→ ReadyToMerge)
```

- **Observe (deterministic, 0 tokens):** collect the current *evidence* — build output, test
  results, screenshots, logs, DRC/ERC reports, sim results, mesh checks. Pure Go/tool calls.
- **Analyse (reasoning, small):** a worker interprets the evidence into a *diagnosis* and a *repair
  plan*. Only the *judgment* costs tokens; the evidence was gathered for free.
- **Repair (reasoning, Developer):** apply the code/design change the diagnosis implies.
- **Verify (deterministic, 0 tokens):** re-run the gates. Pass → stop. Fail → loop with the new,
  structured failure as the only added context (P9).

This maps directly onto the Assignment states Coding↔Building↔QA↔Repair in
[16_WorkerStateMachine](16_WorkerStateMachine.md): Observe+Verify are the Building/QA gates; Analyse
is folded into the Developer/QA workers; Repair is Coding.

## Why one loop for everything

- **Simplicity (P6):** one mental model, one set of stop/escalation rules, one place to reason about
  progress. No per-domain bespoke controllers.
- **Determinism (P5):** the expensive part (running builds/tests/sims/renders) is always Go; the AI
  only diagnoses and edits. This is what makes repairs cheap across all domains.
- **Portability (P10):** adding a domain = adding its Observe/Verify tools to a plugin; the loop
  itself never changes.

## Measuring progress (deterministic)

Every iteration produces a **signal set** so the loop can tell *improving* from *stuck*:

- **Gate pass ratio** — fraction of the plugin's gates now passing (e.g. build ✓, lint ✓, 8/10 tests,
  DRC ✓, creepage ✗). Monotonic improvement = progress.
- **Failure fingerprint** — a normalized signature of the top failure (error class + location).
  Stored in the Brain known-failures ([04_ProjectBrain](04_ProjectBrain.md)).
- **Novelty** — is this iteration's failure fingerprint **new** or a **repeat** of a prior attempt?

Deterministic progress rule:

- **Improving** (pass ratio up, or a new & plausibly-closer failure) → keep looping.
- **Oscillating/stuck** (same failure fingerprint seen ≥ 2 times, or pass ratio not increasing across
  2 attempts) → **stop early** and escalate; do not burn the full `maxAttempts` chasing a wall.

No AI decides "am I making progress" — the signal set decides it deterministically. The worker is
only told *what* failed, not *whether to give up*.

## Stop conditions

The loop stops when **any** of these is true:

1. **Success:** all runnable gates pass → ReadyToMerge (deferred gates recorded, not blocking — P7).
2. **Max retries:** `maxAttempts` reached (config, default 3).
3. **Stuck detector:** repeated identical failure fingerprint or flat pass-ratio (above).
4. **Budget:** cumulative token/wall-clock budget for the issue exceeded.
5. **Hard block:** a required, non-deferrable resource is missing → Blocked.
6. **Cancellation:** owner stop-issue.

On 2–5 the Assignment goes to Failed/Blocked → NEEDS_HUMAN with the full evidence trail.

## Maximum retries & escalation

- **Default `maxAttempts = 3`** (config, [13_Config](13_Config.md)); tunable per project and per
  plugin (some domains, e.g. flaky visual QA, may warrant a slightly higher cap; hardware sims that
  are deterministic warrant a lower one).
- **Escalation ladder** when the loop can't finish:
  1. **Deepen context once:** if stuck on a *new* failure, expand the relevant-files slice via the
     dependency graph (still small) and try one more Repair. This is deterministic, not "send the
     whole repo".
  2. **Split the issue** (below) if the diagnosis reveals the task is really several tasks.
  3. **Defer** the specific failing check if it's environmental, not code (below).
  4. **NEEDS_HUMAN** with the evidence, failure fingerprints, and what was tried.

## When to split a Jira task

Split (create sub-issues, link, and either continue with the first slice or hand the split back to
the backlog) when the deterministic + diagnosis signals show:

- The plan's `files_to_touch` or module set is **large** (crosses a configurable module/file
  threshold) — a smaller branch merges sooner and conflicts less (P8).
- The issue **mixes concerns** that map to **different plugins/repos** (e.g. "add feature in app +
  backend + firmware") — split per repo so each merges independently.
- Repairs keep **oscillating between two sub-goals** (fingerprints alternate) — a sign of two
  entangled tasks; split them.
- AC has **independent acceptance items** that can ship separately.

Splitting is a deterministic action (create linked Jira issues via `jira.*`, [08_Jira](08_Jira.md))
driven by a worker's structured recommendation; the engine performs the split, not the model.

## When to create follow-up issues

- **Deferred checks** always create a linked follow-up ("Deferred QA: …") with how-to + environment
  needed (P7, [06_QA](06_QA.md)).
- **Discovered-but-out-of-scope work** (the Developer finds a real bug unrelated to this AC) → a new
  Jira issue, not scope-creep into the current tiny branch.
- **Known-failure resolutions** worth generalizing → an improvement issue (e.g. "make test X
  non-flaky").

## When to defer testing (never block — P7)

Defer a *specific* verification (not the whole issue) when it can't run for an **environmental**
reason — never because the code is wrong:

- No device/board connected; no simulator available; visual rendering impossible; human-only or
  physical test (bench/notarization/store review).

Deferring records the check + reason + how-to + follow-up key and lets the issue merge on all
runnable gates. It is **not** a pass and is shown as a distinct state ([06_QA](06_QA.md),
[10_Hardware](10_Hardware.md)).

---

## Per-domain repair loops

Same loop; domain-specific Observe (evidence) and Verify (gates). Each is a plugin
([18_PluginContract](18_PluginContract.md)). "Deferred" rows are the common human/hardware cases.

### Flutter (app)
- **Observe:** `flutter analyze`, `flutter test`, build output; launch on device/sim → screenshots;
  runtime logs (adb/idevicesyslog); image-diff vs goldens.
- **Analyse:** classify (compile error / failing widget test / layout-visual diff / runtime
  exception); map to files via the Brain.
- **Repair:** Developer edits Dart/widgets/state; obey icon & size rules where configured.
- **Verify:** build ✓, analyze ✓, tests ✓, visual diff within tolerance, no new runtime errors.
- **Defer:** no device/sim → visual rung deferred, headless widget tests still run.

### .NET (service)
- **Observe:** `dotnet build`, `dotnet test`, analyzer/warnings, run service + hit health/endpoints,
  logs.
- **Analyse:** compile vs test-failure vs runtime/DI/config error.
- **Repair:** Developer edits C#; keep migrations/config consistent.
- **Verify:** build ✓, tests ✓, service boots, targeted API assertions pass.
- **Defer:** external dependency (DB/broker) unavailable → integration checks deferred, unit checks
  run.

### Website (frontend)
- **Observe:** build (`npm/next build`), lint/typecheck, unit tests, launch in headless/real browser
  → screenshots + DOM/OCR assertions; console/network logs.
- **Analyse:** build vs type vs visual vs interaction failure; **app↔web parity** checks where
  configured (a feature present in the app must exist on the web — a parity defect is a real FAIL).
- **Repair:** Developer edits components/routes/styles.
- **Verify:** build ✓, typecheck ✓, tests ✓, visual diff within tolerance, key flows clickable.
- **Defer:** no browser environment → visual deferred, unit/type checks run.

### REST API
- **Observe:** build, contract/schema (OpenAPI) checks, `api.request`/`api.assert` against a locally
  booted instance, status/latency, logs.
- **Analyse:** contract mismatch vs handler bug vs auth/validation error.
- **Repair:** Developer edits handlers/DTOs/validation; keep the contract as source of truth.
- **Verify:** schema ✓, endpoint assertions ✓, auth/negative cases ✓.
- **Defer:** upstream service unavailable → those endpoints deferred, rest run.

### ESP32 (firmware)
- **Observe:** PlatformIO/ESP-IDF build; **Wokwi** run with `--expect-text`/`--fail-text`; **Renode**
  multi-node sim; on-board flash + serial **only if** `device.connected?`.
- **Analyse:** compile vs logic (sim output) vs timing/peripheral issue.
- **Repair:** Developer edits firmware.
- **Verify:** build ✓, Wokwi expected output ✓, Renode scenario ✓; on-hardware smoke ✓ *if board
  present*.
- **Defer:** no board → flash/on-hardware **deferred** (device lock, [15](15_LockManager.md)); sim
  gates still PASS the functional part ([10_Hardware](10_Hardware.md)).

### PCB
- **Observe:** `kicad-cli` ERC/DRC, netlist extraction, board render, **ngspice** SPICE, `example-pcb`
  verify; **measured** creepage.
- **Analyse:** schematic↔PCB drift vs DRC violation vs creepage/clearance vs SPICE (MOV clamp, X/Y-cap
  bleed, rails, ADC) vs current-rating weakest-link.
- **Repair:** Developer edits schematic/layout **at the generator source** and regenerates (never
  hand-patch the output — owner rule).
- **Verify:** ERC ✓, DRC ✓, creepage ≥ threshold ✓, SPICE ✓, rating chain ✓ (the mandatory PCB
  safety battery, [10_Hardware](10_Hardware.md)).
- **Defer:** physical bench tests (hipot/ground-bond/temp-rise) + UKCA/CE = **human/physical**,
  deferred with attestation follow-up; sim validates *function only*, never a false green.

### 3D
- **Observe:** generate mesh/STL; **python-fcl + trimesh** puzzle-fit (interference/clearance);
  `mesh.contains()` occupancy/alignment probes; float check; board-seat collision; FreeCAD
  cross-check; render.
- **Analyse:** interference vs clearance-too-tight vs floating/unsupported feature vs
  aperture/seat/alignment error.
- **Repair:** Developer edits the parametric generator (OpenSCAD/Python/Fusion/KiCad-3D) and
  regenerates.
- **Verify:** FCL fit ✓, occupancy/alignment ✓, printability (flat bottom, engraved branding, BS1363
  apertures) ✓, board seats on standoffs ✓ (the mandatory 3D gates, [10_Hardware](10_Hardware.md)).
- **Defer:** physical print/fit = **human/physical**, deferred; sims validate geometry only.

### Documentation
- **Observe:** markdown lint, link-checker (no broken internal refs), spell/style, doc-build,
  code-block compile/snippet checks where applicable.
- **Analyse:** broken link vs stale reference vs failing example vs style violation.
- **Repair:** Developer edits docs.
- **Verify:** links ✓, build ✓, examples ✓, style ✓ (this very spec is validated this way — see the
  Architecture Review link-check).
- **Defer:** rarely; almost always fully runnable.

### Infrastructure
- **Observe:** IaC validate/plan (e.g. terraform/compose/k8s manifest lint), Docker build, container
  boot + healthcheck, config drift check, dry-run.
- **Analyse:** syntax vs plan diff vs failed healthcheck vs missing secret/config.
- **Repair:** Developer edits manifests/compose/Dockerfiles/config.
- **Verify:** validate ✓, build ✓, container healthy, plan matches intent, no destructive diff.
- **Defer:** applying to a live/paid environment = **human-gated** (never spend / never solo-deploy
  rules) → deferred; local validation/build still run.

## Invariants (repair-specific)

- **R-1** Observe and Verify are always deterministic (0 tokens); only Analyse/Repair spend tokens.
- **R-2** Every loop is bounded by `maxAttempts` and by a token/wall-clock budget.
- **R-3** Progress is measured deterministically (gate ratio, failure fingerprint, novelty); the
  stuck-detector can stop early.
- **R-4** A failing environmental check **defers**; it never fails the issue or blocks the merge (P7).
- **R-5** Splitting, follow-ups, and deferrals are performed deterministically by the engine on a
  worker's structured recommendation — the model proposes, Go disposes.
- **R-6** The loop is identical across all domains; only plugin tools differ (P6, P10).
