# Phase 2 — Production-Readiness Review (HOLD state)

**Status: frozen.** Architecture and implementation are frozen. No adapter integration, no
orchestration/runtime/policy/account/behaviour change until the owner says **GO**. This document is
non-functional (documentation only) and implements nothing.

## Current state (verified)

- Real: **Jira**, **Git** (incl. merge), **Worker Runtime (Claude Code)**; orchestration, state stores,
  Control Plane, Operations Console.
- Simulated: **Verification Drivers**, **Resource Discovery**.
- `gofmt` / `go vet` clean; `go test ./...` — **22/22 packages PASS**.
- Both modes operable: `cwv2 serve --mode simulation` (no external services) and `--mode live`
  (real Jira + Git + Claude; verification/resource-discovery still simulated).

The remaining two integrations plug into **existing frozen seams** (no architecture change needed):
- Verification → the `orchestrator.Verifier` port (today `sim.NewVerifier`) + the S8 `verify.Engine`
  plugin model + the `verify.VisualDriver` interface.
- Resource discovery → the `resource.Discoverer` interface (today `StaticDiscoverer` + a hard-coded
  `claude-1`) called by `resource.Manager.Discover`.

---

## Integration A — Verification Drivers (Phase 2.4)

Goal: replace `sim.Verifier` with a real adapter over `verify.Engine`, registering real verifier
plugins (visual: Android/iPhone/Desktop/Web; build; API), selected by capability per issue.

### Prerequisites
- A verification target per issue type: an installable app build (Android APK / iOS .app or TestFlight
  / desktop binary / web URL) and/or a runnable service for API checks.
- Driver runtimes installed on the runner: **Appium** server + drivers (UiAutomator2 for Android,
  XCUITest/WDA for iOS), a **WebDriver** (Chromedriver/geckodriver) for web, platform toolchains for
  build verification (already have Go; add Flutter/Xcode/Gradle only for the real projects).
- An **OCR** engine for the visual verifier's screen-text checks (e.g. Tesseract) — the `VisualDriver`
  contract already exposes `OCR()`.
- A capability→verifier mapping (config): which verifier(s) run for a given issue/label/repo.

### Credentials / secrets
- iOS: Apple Developer signing (WDA needs a provisioning profile / dev team); simulator needs none but
  real-device does.
- TestFlight/store targets: App Store Connect API key (only if verifying installed store builds).
- Web targets behind auth: test-account credentials via the secrets resolver (never in prompts/logs).
- No new secret *types* beyond what the app already uses; wire through `internal/secrets`.

### Connected devices
- Android: a device/emulator reachable by `adb` (the fleet already documents a Galaxy A6 + emulators).
- iPhone: a simulator (17.5/18.6) or a cabled real device with Appium/WDA (owner rule: never build iOS
  locally on the Mac mini without asking — verification would run on the designated runner).
- Desktop/Web: the runner host + a browser.

### Expected risks
- **Flakiness** (the #1 QA risk): UI timing, animations, device state. Mitigate with the S8 outcome
  model — a driver failure is `Blocked`/`Inconclusive`, **never** a false `Fail`; retries are the
  Policy Engine's call, not the verifier's.
- **Headless environments**: `ErrVisualUnavailable → Blocked` already signals "fall back to a headless
  verifier"; ensure a non-visual verifier is registered for CI.
- **Device contention**: two assignments wanting the same device — resolved by the Lease Manager
  (device lease) once devices are real resources (overlaps Integration B).
- **Long durations**: visual runs are slow; enforce per-verifier timeouts (the S8 engine already stamps
  duration; add a ctx deadline in the driver).
- **Token spend**: none — verification is deterministic; no model calls.

### Validation plan
- Unit: each real driver against a fixture app/URL; assert Pass/Fail/Blocked/Inconclusive + evidence
  (screenshot/OCR/diff) + metrics.
- Integration: `verify.Engine` selecting the right plugin by capability; `Aggregate` precedence.
- End-to-end (Simulation-first): keep `sim.Verifier` as the regression path; add a "live verification"
  e2e that runs the real build/API verifier (deterministic, no devices) through the orchestrator
  claim→verify→improve→merge→done.
- Human-like visual e2e on one real target (e.g. the Flutter app on an emulator) as an attended demo.

### Rollback plan
- The Verifier is a single injected port. Rollback = revert the serve wiring to `sim.NewVerifier`
  (one line); the orchestrator/policy are untouched. Simulation Mode is unaffected throughout.
- Feature-flag live verification behind config so a bad driver can't block the loop (fall back to a
  build/API verifier or Blocked→policy defer).

### Success criteria
- A real build **and** a real API verifier run in live mode with correct outcomes + evidence in the
  Control Plane (`verification.recent`), console Verification page live.
- One human-like visual verifier drives a real app (launch/navigate/click/type/assert/screenshot/OCR)
  and reports differences.
- No false `Fail` on environmental issues (they surface as `Blocked`/`Inconclusive`).
- `go test -race ./...` green; Simulation Mode still processes the demo backlog.

---

## Integration B — Resource Discovery (Phase 2.6)

Goal: replace the static `claude-1` registration with real `resource.Discoverer` implementations that
find and health-check the fleet, feeding the Resource Manager (which already does availability,
pacing, cooldown, scheduling, failover).

### Prerequisites
- Discovery probes per kind:
  - **Claude accounts** — enumerate configured `CLAUDE_CONFIG_DIR`s / logged-in accounts; health =
    `claude` reachable + authenticated.
  - **Codex accounts** — analogous (Codex CLI), if used.
  - **Android (ADB)** — `adb devices`; health = device online.
  - **iPhone (iOS)** — Appium/`idevice`/simulator list; health = reachable.
  - **Mac Mini / Build Machine** — self-hosted runner reachability (ssh/health endpoint).
  - **ESP32** — serial/USB enumeration (or the dummy-device simulator for CI).
- A resource-declaration config schema (accounts, hosts, device selectors) — **note:** `config.Config`
  currently has no `resources`/`accounts` block; adding one is an additive config change to prepare
  (not implement) now.
- Labels convention the runtime already reads: `claude_config_dir`, `model` (accounts); region/os for
  hosts.

### Credentials / secrets
- Claude/Codex account tokens/config dirs (via existing per-account config dirs; never in prompts).
- SSH keys / runner tokens for Mac mini + build machines (secrets resolver).
- No cloud credentials required for local device discovery.

### Connected devices / hardware
- The physical fleet: Android phone(s), iPhone(s), ESP32 board(s), Mac mini, build machine(s).
- For CI/no-hardware: the existing dummy-device simulator + `StaticDiscoverer` remain the fallback
  (Simulation Mode needs no hardware).

### Expected risks
- **Transient device drop-out** (USB, sleep) → discovery must reconcile without clobbering live
  reservations (the `Discover` reconcile already preserves metrics/reservations — verified in S7A).
- **Stale/ghost devices** → health monitoring must mark `down` so scheduling skips them.
- **Account auth expiry** → surfaces as the runtime's `authentication` class → cooldown; discovery
  should re-check health.
- **Discovery cost/latency** → probes must be bounded + periodic, not per-loop (no busy polling).
- **Security** → shelling out to `adb`/`ssh` from a service: validate inputs, no shell injection.

### Validation plan
- Unit: each `Discoverer` against a mocked probe (fake `adb`/`claude`/ssh output) → correct
  Resource set + health; reconcile preserves reservations/metrics.
- Integration: `resource.Manager.Discover` merges multiple discoverers; scheduling/failover picks the
  right resource after a health change.
- End-to-end: live mode discovers ≥1 real Claude account + ≥1 device; the loop reserves/leases/
  releases them; `resources.snapshot` + `accounts.list` live in the console.
- Restart: discovery re-populates inventory on boot; leases recover; no double-use.

### Rollback plan
- Discovery is additive: register the real discoverers alongside `StaticDiscoverer`. Rollback = stop
  registering the live discoverers (config flag) → falls back to the static default; the Resource
  Manager and everything downstream are unchanged.
- A discovery failure must not stall the loop — treat as "no new resources", keep the last-known set.

### Success criteria
- Real accounts + devices appear as resources with correct health/availability; the loop uses them via
  Policy→Resource→Lease with real failover across accounts and device leases.
- Console `resources.snapshot`/`accounts.list`/AI-Runtimes fully live; no static placeholder in live
  mode.
- `go test -race ./...` green; Simulation Mode still runs with the static/dummy fleet.

---

## Cross-cutting gaps to resolve before GO

- **Config schema:** no `resources`/`accounts`/`verification` blocks in `config.Config` yet — an
  additive change to design (not build) so live mode can declare accounts/devices/verifier mapping.
- **Missing credentials to gather:** Jira token (present for #1), GitHub/remote push creds for real
  merges to `origin`, Apple signing (iOS visual), per-account Claude config dirs, runner SSH keys.
- **Missing hardware to connect:** the physical device fleet for real visual/device verification and
  resource discovery (CI stays on simulator/dummy).
- **Ordering note:** Verification (device leases) and Resource Discovery (devices as resources)
  overlap on device ownership — recommend discovering devices (B) at least far enough that Verification
  (A) can take **device leases**, or scope A's first pass to build/API/web (no physical device) to
  decouple.

## Ordering recommendation (for when GO is given)

1. **Resource Discovery — accounts + build/host only** (no physical devices) → real account fleet.
2. **Verification — build + API + web** (no physical device) → real deterministic verification.
3. **Resource Discovery — physical devices** (ADB/iOS/ESP32).
4. **Verification — visual device drivers** (Android/iPhone), taking device leases.

Each step: one edge at a time, `gofmt`/`vet`/`go test -race`, e2e, perf + production validation,
Simulation Mode preserved, stop for review.

---

**HOLD.** Awaiting the owner's explicit **GO** before any integration resumes.
