# 18 — Plugin Contract

Every capability plugin exposes the **same** interface so the engine core can treat all project types
uniformly and remain project-agnostic (P10). This document formalizes the contract sketched in
[11_Plugins](11_Plugins.md): the required methods, registration, discovery, configuration,
dependencies, lifecycle, versioning, and health.

> The contract is a **capability interface**, not a personality. A plugin adds *hands* (deterministic
> tools + gates), never a worker type — there are always exactly four workers
> ([05_Workers](05_Workers.md)). All plugin methods are **deterministic** (0 model tokens) unless they
> explicitly delegate a reasoning step back to the engine's Worker Runner.

Notation below is language-neutral pseudo-IDL (`method(inputs) -> outputs`). No implementation
language is prescribed here (the engine is Go, [02_Architecture](02_Architecture.md), but the
contract is defined abstractly).

## 1. Capabilities

A plugin declares which capabilities it supports; the engine only calls declared ones. A plugin that
doesn't support a capability declares it absent, and the engine adapts (e.g. no `Verify` visual rung
→ headless or defer).

```
Capability = one of:
  detect, build, verify, repair, reserve_resources,
  generate_acceptance_criteria, generate_artifacts, cleanup, shutdown, health
```

Capabilities are advertised in the manifest (`plugin.yaml`) and confirmed at registration (§2).

## 2. Registration

- Plugins are registered with the engine's **Plugin Registry** at startup. A plugin provides a
  **manifest** (declarative, [11_Plugins](11_Plugins.md)) and a **provider** (the code implementing
  the methods).
- Registration record:
  ```
  register(plugin) -> {
    name: string,                 # unique, e.g. "flutter", "pcb-kicad"
    version: semver,              # §7
    capabilities: [Capability],
    detect: [markers],            # files/globs identifying this project type
    modules: ModuleResolver,      # how to enumerate module boundaries (locks, §15)
    tools: {name -> ToolRef},     # deterministic tools exposed to workers
    gates: [GateRef],             # ordered PASS/FAIL checks for Build/Verify
    requires: [Dependency],       # §5
  }
  ```
- Registration is **idempotent** and **validated**: duplicate names rejected; declared capabilities
  must have implementations; declared tools must be runnable. A registration failure is a startup
  error (`cwv2 doctor`, [14_Deployment](14_Deployment.md)) — never a silent skip.

## 3. Discovery

- **Static discovery:** the engine loads all manifests under `plugins/` (core) + any configured
  external plugin dirs.
- **Project-type detection:** given a repo, the engine runs each plugin's `detect(repoPath) ->
  {matches: bool, confidence}` deterministically (marker files) and binds repos/components to plugins.
  A project config may pin the mapping explicitly ([13_Config](13_Config.md) `component_map` /
  `repos[].plugin`), overriding auto-detection.
- **Tool discovery:** a worker's least-privilege tool set for a stage is computed from the bound
  plugin's `tools` + the `_core` tools ([05_Workers](05_Workers.md)); nothing outside the declared
  set is callable.

## 4. Configuration

- Each plugin declares a **config schema** (keys, types, defaults). Project config supplies values
  (e.g. `imgdiff_tolerance`, `build_flavor`, `creepage_class`). Unknown keys are rejected; missing
  keys fall back to declared defaults ([13_Config](13_Config.md) precedence).
- Config is passed to every method via an immutable `ctx` (project, repo, worktree path, resolved
  config, secrets-by-reference). Plugins never read global state directly.

## 5. Dependencies

- A plugin declares external **toolchain dependencies** it needs (`flutter`, `dotnet`, `kicad-cli`,
  `ngspice`, `platformio`, `wokwi-cli`, `freecad`, `docker`, …) with min versions and a **probe**
  (how to check presence/version).
  ```
  Dependency = { name, min_version, probe_cmd, required: bool }
  ```
- `cwv2 doctor` runs every probe. A **required** missing dependency blocks using that plugin (clear
  error). A **non-required** missing dependency degrades gracefully: the affected gate becomes
  **deferrable** ([06_QA](06_QA.md), [17_RepairLoop](17_RepairLoop.md)), not a failure.
- Plugins may declare **inter-plugin** dependencies (e.g. `hardware-pipeline` builds on `pcb-kicad` +
  `cad-3d`); the registry resolves load order deterministically (topological, cycle = error).

## 6. Lifecycle

A plugin instance's lifecycle, all deterministic:

```
loaded ─register─▶ registered ─doctor/probe─▶ ready
   ready ──(per issue, on demand)──▶ reserve → build → verify → repair* → generate_artifacts → cleanup
   ready ──(on engine stop)──▶ shutdown
```

- Plugins are **stateless across issues**: any per-issue state lives in the `ctx`/worktree, never in
  the plugin instance (mirrors worker disposability, P4). A plugin instance may be reused across many
  issues; it must behave as if freshly constructed each call.

## 7. Versioning

- Plugins are **semver**. The engine declares a **plugin API version**; a plugin declares the API
  version it targets. On mismatch beyond the compatible range, the engine refuses to load it (clear
  error) rather than call an incompatible method.
- Manifest + provider version must agree. Gate definitions and tool names are part of the plugin's
  public surface; breaking them is a major bump.
- The Brain records which plugin version verified each issue (per-issue history,
  [04_ProjectBrain](04_ProjectBrain.md)) for reproducibility (NFR-7).

## 8. Health

- `health(ctx) -> { status: ok|degraded|down, checks: [{name, ok, detail}] }` — a fast,
  deterministic self-check: are required tools present, is the toolchain responsive, are resource
  pools reachable. Surfaced on the dashboard ([09_Dashboard](09_Dashboard.md)).
- `degraded` (e.g. no device attached) means "some rungs will defer", not "broken". `down` (e.g.
  required toolchain missing) removes the plugin from admission for its project type until fixed.

## 9. Required methods

Every plugin implements these (declaring a capability absent where truly N/A). All are deterministic;
`Verify`/`Build` return structured PASS/FAIL with evidence refs; none call a model directly.

### `Detect(repoPath) -> { matches: bool, confidence: 0..1 }`
Deterministic project-type detection from marker files. Used by discovery (§3).

### `Build(ctx) -> BuildResult { ok, logs_ref, artifacts_ref, duration }`
Compile/assemble the project (or the relevant module). Zero tokens. Feeds the **Building** gate
([16_WorkerStateMachine](16_WorkerStateMachine.md)) and the **Observe** step
([17_RepairLoop](17_RepairLoop.md)).

### `Verify(ctx, criteria) -> VerifyResult { verdict: PASS|FAIL|DEFER, gate_results: [...], evidence_refs: [...] }`
Run the plugin's ordered gates (lint, tests, visual diff, DRC/ERC, SPICE, FCL-fit, healthchecks, …)
against the acceptance criteria. Returns per-gate PASS/FAIL and **DEFER** for environmentally
impossible gates (with reason + how-to). This is the **Verify** step of the repair loop and the
deterministic core of QA ([06_QA](06_QA.md)). It never renders a false green (a sim PASS is labeled
as such, [10_Hardware](10_Hardware.md)).

### `Repair(ctx, failure) -> RepairResult { changed: bool, actions: [...], residual_failure? }`
**Deterministic auto-remediation only** — the cheap fixes that need no reasoning: auto-format, run a
codegen/formatter/fixer, reinstall dependencies, regenerate an artifact from its source, apply a
known-safe migration. The repair loop calls `Repair` **before** spending tokens; if it clears the
failure, no Developer worker is spawned (token savings, P5). Reasoning repairs remain the Developer
worker's job ([17_RepairLoop](17_RepairLoop.md) Analyse/Repair). `Repair` must be **safe and
idempotent** and must never make a change it can't justify deterministically.

### `ReserveResources(ctx, need) -> Reservation { lock_ids: [...], granted: bool, defer_reason? }`
Acquire the domain resources a build/verify needs — a device/board, a simulator slot, a port — by
requesting the appropriate **locks** from the Lock Manager ([15_LockManager](15_LockManager.md)). If a
required resource is absent, returns `granted:false` with a `defer_reason` so the loop **defers** that
check instead of blocking (P7). The plugin never talks to hardware without holding the lock (L-1).

### `GenerateAcceptanceCriteria(ctx, issue) -> [Criterion]`
Deterministically derive **checkable** acceptance criteria from the issue + project conventions where
possible (e.g. "builds", "no analyzer warnings", "DRC clean", "creepage ≥ 6 mm for mains", "endpoint
returns 200 + schema"). This seeds/augments the AC the Manager worker refines ([08_Jira](08_Jira.md),
[05_Workers](05_Workers.md)); it makes "done" objective and testable per domain.

### `GenerateArtifacts(ctx) -> [Artifact]`
Produce the domain outputs the issue requires: an APK/IPA, a compiled binary, a rendered board image,
an exported STL, a container image, a doc site. Artifacts are written under the issue's `artifacts/`
dir on the SSD and referenced in the run record + Jira result comment.

### `Cleanup(ctx)`
Release per-issue resources: delete temp build dirs, stop launched apps/containers, release
reservations/locks, close device sessions. Called at issue CLOSE, cancel, or failure. Idempotent.

### `Shutdown()`
Release process-wide resources at engine stop (long-running toolchain daemons, simulator processes).
Idempotent; safe if `ready` was never reached.

## 10. Method call contract (uniform)

- **Typed in/out, structured** — no free-form parsing by workers or the engine.
- **Deterministic + zero model tokens** — a plugin method never spends tokens; if a step needs
  reasoning, the *engine* spawns a worker around the plugin's evidence, not the plugin.
- **Idempotent where stated** (`Repair`, `Cleanup`, `Shutdown`, `ReserveResources` release).
- **Honest about environment** — inability to run returns DEFER/`granted:false`, never a fake success
  ([06_QA](06_QA.md)).
- **Independently runnable** — every tool/gate is invocable via `cwv2 tool <plugin>.<name>` for
  testing ([11_Plugins](11_Plugins.md)).
- **Bounded** — each method honors a timeout from `ctx`; overruns return a structured timeout the loop
  treats as a failure/defer.

## 11. Adding a future capability without changing the engine (P10)

The whole point of the contract. To support a **new project type** (e.g. Rust, Kubernetes operators,
Verilog, GStreamer pipelines):

1. Write a `plugin.yaml` manifest: name, version, `detect` markers, `tools`, `gates`, `requires`,
   config schema, module resolver.
2. Implement the required methods (§9) as deterministic wrappers around that domain's toolchain.
3. Declare dependencies + probes (§5) so `doctor` validates the environment.
4. Drop a tiny hints file and reference the plugin from a project config.

No change to Scheduler, Orchestrator, Worker Runner, Brain, Lock Manager, Git, or Jira layers. If
adding a capability requires editing engine core, the contract has leaked and must be fixed — that is
the acceptance test for this document.

## 12. Reference examples (capability sketches)

| Plugin | Build | Verify (gates) | Repair (deterministic) | ReserveResources | Artifacts |
|---|---|---|---|---|---|
| **flutter** | `flutter build` | analyze, test, visual-diff | `dart format`, `flutter pub get` | device/sim lock | APK/IPA |
| **dotnet** | `dotnet build` | test, analyzers, boot+API | `dotnet format`, restore | port lock | binary/image |
| **website** | `next build` | typecheck, test, visual, parity | prettier, `npm ci` | browser/port lock | static site |
| **rest-api** | build | OpenAPI schema, endpoint asserts | codegen from spec, format | port lock | api image |
| **esp32-firmware** | pio/idf build | Wokwi, Renode, on-board* | format, regenerate config | **device lock** | firmware bin |
| **pcb-kicad** | generate board | ERC, DRC, creepage, SPICE, rating | regenerate from source | (none) | render, gerbers |
| **cad-3d / fusion** | generate mesh | FCL-fit, occupancy, printability | regenerate from parametric source | (none) | STL |
| **kicad** | (see pcb-kicad) | (see pcb-kicad) | regenerate | (none) | board files |
| **ai** | build/train stub | eval suite, metric thresholds | reformat, env sync | GPU/port lock | model/report |
| **docker / infra** | image build | validate, healthcheck, dry-run | lint-fix, format | (none) | image/manifests |

`*` on-board = deferred when no device is attached (device lock, [15_LockManager](15_LockManager.md),
[10_Hardware](10_Hardware.md)).

## 13. Invariants (plugin-specific)

- **PL-1** All plugins expose the identical contract (§9); the engine calls only declared
  capabilities.
- **PL-2** Plugin methods are deterministic (0 tokens); reasoning is always the engine's workers.
- **PL-3** A missing required dependency blocks the plugin; a missing optional one **degrades to
  defer**, never a false pass.
- **PL-4** Plugins are stateless across issues (P4) and versioned (semver); the verifying version is
  recorded (NFR-7).
- **PL-5** Adding a project type requires **no** engine-core change (P10) — else the design has
  leaked.
