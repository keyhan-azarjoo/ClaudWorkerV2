# 11 — Plugins

Plugins are how one engine supports many project types (Flutter, .NET, web, AI, ESP32, PCB, 3D,
hardware) **without changing engine source** (P10). A plugin is **data (a manifest) + a set of
deterministic Go tools**. The **formal interface** every plugin implements (required methods,
registration, discovery, versioning, health) is [18_PlugInContract](18_PlugInContract.md); this doc
is the conceptual overview. The engine core stays project-agnostic; all domain knowledge about "how do
I build/test/QA *this kind* of thing" lives in plugins.

## What a plugin provides

1. **Detection** — how to recognize this project type in a repo (marker files: `pubspec.yaml`,
   `*.csproj`, `package.json`, `platformio.ini`, `*.kicad_pcb`, `*.scad`/`*.FCStd`, …).
2. **Toolset** — the deterministic tools this type needs: build, format, lint, test, launch/drive,
   package, and any domain validators (DRC, FCL-fit, Wokwi, …). Each is a zero-token Go wrapper
   around a real CLI/toolchain.
3. **QA strategy** — which QA rung applies ([06](06_QA.md)): how to launch and drive the app,
   where goldens live, image-diff tolerances, or "headless only", or "simulation + defer physical".
4. **Gates** — the deterministic PASS/FAIL checks that must run in the Build/QA gate for this type
   (e.g. hardware's DRC/creepage/FCL/18-stage; a Flutter app's build + widget tests + visual diff).
5. **Prompt hints** — small, type-specific notes appended to the Architecture Summary slice (e.g.
   "this is a Flutter app; state via Riverpod; icons via MyotgoIcons") — kept tiny (P9).

## Plugin manifest (declarative)

A manifest is a small YAML/JSON file; tools it references are Go functions registered in the plugin
package. Illustrative:

```yaml
plugin: flutter
detect: ["pubspec.yaml"]
tools:
  build:   flutter.build          # -> go wrapper: `flutter build`
  format:  flutter.format
  lint:    flutter.analyze
  test:    flutter.test
  launch:  flutter.launch         # adb / simctl backed
  drive:   flutter.drive          # ui.tap/type/... backend
gates:
  - build
  - lint
  - test
qa:
  strategy: visual                # visual | headless | simulation
  goldens: brain                  # goldens stored in the Brain
  imgdiff_tolerance: 0.02
hints_file: hints/flutter.md
```

The engine loads manifests at startup, indexes them by `detect`, and exposes their tools to workers
(least-privilege per stage, [05](05_Workers.md)).

## Core vs project plugins

- **Core plugins** ship in the repo under `plugins/` and cover the common types: `generic`,
  `flutter`, `dotnet`, `web`, `python-ai`, `esp32-firmware`, `pcb-kicad`, `cad-3d`,
  `hardware-pipeline`.
- **`_core`** holds cross-cutting, project-agnostic assets: worker charters, the base git/jira/brain
  tools, and the prompt-assembly logic. `_core` is not a "project type"; it's the engine's shared
  toolbelt.
- A project can declare which plugins it uses in its config ([13](13_Config.md)); a multi-repo
  project maps each repo/component to a plugin.

## Adding a new project type (no core edits — P10)

1. Write a manifest describing detection, tools, gates, QA strategy.
2. Implement its deterministic tool wrappers in a new Go plugin package (only if the tools don't
   already exist in another plugin).
3. Drop a tiny hints file.
4. Reference it from the project config.

No change to Scheduler, Orchestrator, Workers, Brain, Git, or Jira layers. That boundary is the test
of whether the plugin system is right: if supporting a new type requires touching the engine core,
the design has leaked and must be fixed.

## Tool contract (all plugins)

Every tool, in any plugin, obeys the same contract so the engine can treat them uniformly:

- **Typed input / typed output** (structured; no free-form parsing by workers).
- **Deterministic** (same inputs → same outputs where the underlying tool allows).
- **Zero model tokens** to execute (it's Go/subprocess).
- **Independently runnable** on the CLI: `cwv2 tool <plugin>.<name> [args]` for testing.
- **Reports structured results** including PASS/FAIL for gates, with evidence references.
- **Honest about environment**: if it can't run (missing device/board/toolchain), it returns a
  **DEFER-able** result with the reason and how-to, never a fake success ([06](06_QA.md),
  [10](10_Hardware.md)).

## Relationship to workers

Plugins do **not** add worker types (there are always exactly four, [05](05_Workers.md)). A plugin
only changes *which tools are available* and *which gates run*. "Backend engineer" vs "firmware
engineer" is the Developer worker holding a different plugin's toolbelt — a difference of hands, not
of mind (P6).

## Portability guarantee

The engine core has **no** `if project == "myotgo"` logic. MyOTGO is expressed entirely as: a
project config + a selection of these plugins + the MyOTGO Brain. Any future project is onboarded
the same way (P10, FR-26/27).
