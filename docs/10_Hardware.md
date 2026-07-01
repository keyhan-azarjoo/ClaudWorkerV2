# 10 — Hardware

Hardware projects (firmware, ESP32, PCB, 3D design, manufacturing) run through the **same** engine,
the **same** four workers, and the **same** workflow — the difference is entirely in the **plugins**
that provide deterministic simulators and validators, and in the fact that many hardware checks are
**deferred** (physical) rather than run (P7, P10). Nothing here is a special agent; it's tools +
gates.

## Same engine, hardware tools

- A hardware Jira issue is planned by the **Manager**, implemented by the **Developer** (firmware
  code, KiCad schematic/PCB edits, CAD/OpenSCAD/Python-generated STL), verified by **QA** (running
  deterministic simulators/validators), and merged by the **Integrator** — identical to software.
- All hardware verification tools are **deterministic Go wrappers** around existing local/free tools
  (zero model tokens for running them): the model only *judges results and decides next steps*.

## Deterministic hardware toolbelt (per plugin)

Wrapping the owner's established, installed sim/verification stack:

- **Firmware / ESP32:** build (PlatformIO/ESP-IDF), flash (when a board is attached), **Wokwi** CI
  runs (`wokwi-cli`, `--expect-text`/`--fail-text`, exit 0 = PASS), multi-node **Renode**
  simulation. Tools: `fw.build`, `fw.wokwi`, `fw.renode`, `fw.flash` (only if `device.connected?`).
- **PCB:** `kicad-cli` DRC/ERC, netlist extraction, board render, **ngspice** SPICE sims, the
  `myotgo-pcb` MCP checks (DRC, creepage, SPICE, verify). Tools: `pcb.drc`, `pcb.erc`,
  `pcb.netlist`, `pcb.render`, `pcb.spice`, `pcb.verify`.
- **3D / CAD:** `python-fcl` + `trimesh` **puzzle-fit** test (interference/clearance) and
  `mesh.contains()` occupancy/alignment probes, FreeCAD `freecadcmd` cross-check, STL export, float
  / board-seat collision checks. Tools: `cad.fcl_fit`, `cad.occupancy`, `cad.freecad`, `cad.export_stl`,
  `cad.float_check`.
- **Pipeline runner:** wraps the owner's hard-gated **18-stage** hardware pipeline
  (`hw_pipeline.py` / `hw_validate.py`) as `hw.stage_run` / `hw.validate` producing status JSON.

These are exactly the tools mandated by the owner's hardware rules; the engine *calls* them and
*reads* their verdicts — it never "reasons" a DRC/fit/creepage result (that would be both wrong and
wasteful).

## Hard gates are PASS/FAIL (owner rules preserved)

The owner's hardware gates are **non-negotiable** and encoded as deterministic checks the QA/build
gate runs. A gate FAIL → STOP → fix at the generator source & regenerate → re-run; never ship a
FAIL. Summary of the gates the engine enforces (full detail lives in the owner's hardware memories,
referenced by config):

- **UK compliance HARD GATE** for mains devices (BS1363 form factor, UKCA/CE room, electrical safety
  first). PASS/FAIL.
- **Socket/plug 3D print spec** (flat printable bottom, engraved branding ~0.5 mm, BS1363 aperture
  sizes). PASS/FAIL.
- **3D print rule** (no floating/unsupported features, nothing blocks assembly, verify board seats
  on standoffs). PASS/FAIL — run the float + board-seat collision checks on the OUTPUT.
- **FCL puzzle-fit test** — mandatory on every multi-part 3D set: per-interface interference/clearance
  + occupancy/alignment probes + visual proof. PASS/FAIL.
- **PCB safety battery** — mandatory on every PCB: schematic↔PCB consistency first, DRC + **measured**
  mains creepage (≥3 mm basic / ≥6–8 mm reinforced, never the 0.5 mm default), SPICE (MOV clamp,
  X/Y-cap bleed <60 V in 1 s, rails in spec, ADC divided/clamped), current-rating weakest-link chain
  (BS1363 ⇒ 13 A), fuse the Live, earth pass-through never switched. PASS/FAIL.
- **18-stage pipeline** — Stage 0..17, FAIL→STOP, no stage skipped, no test disabled, no safety rule
  weakened, no FINAL_RELEASE until all PASS. The runner enforces first-non-PASS = `gate_blocked_at`.

The engine **calls the validators, does not reason the results** (owner rule). Which gates apply to a
given board/model is config/plugin driven (`category_skip_checks` marks mains-only checks NA for
battery devices — marked NA, never silently dropped).

## Deferral for physical reality (P7)

Simulation validates *function*; it never replaces the legally-required **physical** bench tests
(hipot / ground-bond / temp-rise / UKCA/CE) or a physically-attached board. So hardware issues
routinely **DEFER** the physical rungs:

- **No board attached** → firmware flash/on-hardware test deferred; Wokwi/Renode sim runs and can
  PASS the functional part.
- **No fab/bench** → physical safety tests deferred; sims run and are honestly labeled "function
  only, physical pending".
- **Human attestation required** → physical sign-off (`physical_signoff.json`, `lab_reports/`) is a
  human action; the engine records the deferral and the exact procedure, creates the follow-up issue,
  and **never fakes a PASS** (missing attestation = BLOCKED, not green).

A deferred physical test does not hold the branch: the code/design merges with all *runnable*
(simulation/validation) gates green and the physical gates tracked as open deferrals with follow-up
issues and a clear "NOT a physical pass" label. FINAL_RELEASE remains gated until the physical
attestations exist.

## Devices & scheduling

- The dashboard **Devices** view ([09](09_Dashboard.md)) shows attached ESP32 boards / phones so the
  Engine knows which rungs are currently possible. When a board is later attached, the owner (or a
  sweep) can **re-run the deferred** on-hardware check from the dashboard.
- Board/device access is coordinated (one board, one user at a time) via the same lock model as git
  — an "hardware access" lock prevents two issues driving the same board simultaneously (P8).

## Honesty rule (no false green light)

For hardware especially: a simulation PASS is reported as *simulation PASS*, the physical test as a
distinct deferred item. The engine never renders an un-bench-tested mains design as fully verified,
and never populates FINAL_RELEASE until the hard gate returns READY with real physical attestations.
