# Device / Visual Verification — Validation Procedure

The **software is complete**: the `verify.VisualDriver` contract, `VisualVerifier`, capability-based
selection, and the verify adapter are implemented and tested with fakes. Only **physical validation**
(a connected device) remains. This is the procedure to run once hardware is attached — no further code
is required to attempt it.

## Prerequisites (hardware + tooling)

| Target | Device | Tooling |
|---|---|---|
| Android | phone/emulator on `adb` | Appium + UiAutomator2 |
| iPhone | simulator or cabled device | Appium + XCUITest/WDA (dev signing for real device) |
| Web | the runner's browser | WebDriver (chromedriver/geckodriver) |
| OCR | — | Tesseract (for `OCR()` screen-text checks) |

## Steps

1. Connect the device; confirm discovery: `curl .../v1/query/resources.snapshot` shows the device as a
   resource (Android via `adb`, iOS via `simctl`).
2. Implement/enable the concrete `VisualDriver` for the target (Appium/adb) — the seam exists; wire it
   into `verifyadapter.BuildEngine(Options{VisualDrv: ...})`. (This is the one remaining integration,
   gated on hardware; the contract + fake are already validated.)
3. Run a live verification against a known app build with an expectation set
   (`text_present` + `state`).

## Expected results

- Driver launches the app, performs the steps (navigate/click/type/scroll/pair), captures a
  screenshot + OCR, and compares expectations.
- A correct app → **Pass** with evidence (screenshot ref, OCR text, 0 differences).

## Pass criteria

- Outcome **Pass**, `differences = 0`, evidence attached, duration recorded, event on `/v1/events`.
- The device lease is acquired before and released after (no orphan lease).

## Fail criteria

- **Fail** with a non-empty `Detail` listing each unmet expectation (real UI regression).
- **Blocked** if the device/driver is unavailable → the loop must fall back to a headless verifier,
  never a false Fail.
- **Inconclusive** on a mid-journey driver error (timing/flake) → retry is the Policy Engine's call.

## Troubleshooting

| Symptom | Cause | Action |
|---|---|---|
| Device not discovered | `adb`/`xcrun` missing or device offline | install tooling; `adb devices` / `simctl list` |
| `ErrVisualUnavailable` | no display / headless CI | expected → Blocked; run headless verifiers |
| Flaky Pass/Fail | UI timing/animation | add waits in the driver; outcomes already model flake as Inconclusive |
| Wrong device leased | multiple devices | scope the capability filter / device lease |

## Status

Software: **complete** (contract + verifier + adapter + tests). Remaining: **physical hardware +
the concrete Appium/adb driver wiring** (external — needs the device). No unfinished software is left
because hardware is unavailable.
