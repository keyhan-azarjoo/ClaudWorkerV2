# 14 — Deployment

How to install, run, and operate the Engine — locally, on the laptop, with everything on the
external SSD (P1, C-6). No VPS is required, ever (NFR-1).

## Footprint

- **One binary:** `cwv2` (Go, static). Built for macOS (arm64) on the laptop.
- **One long-lived process:** `cwv2 serve` (scheduler + orchestrator + dashboard).
- **Runtime deps:** `git`, the **`claude` CLI**, and — only when a given plugin is used — that
  plugin's toolchain (flutter, xcode/simctl, adb, kicad-cli, ngspice, platformio, wokwi-cli,
  freecad, python-fcl). Nothing else is required to start.

## Everything on the SSD (C-6)

```
/Volumes/Extreme SSD/
  ClaudWorkerV2/                  # this repo (clone) — docs now, code later
  cwv2-home/                      # engine home (state; gitignored, not in the repo)
    engine.db
    engine.yaml
    projects/
      example/
        cwv2.yaml
        knowledge.db              # Knowledge Brain (persistent; backed up)
        knowledge/                # architecture.md, conventions.md, rules.md, glossary.md, decisions/
        state.db                  # Execution State (temporary; rebuildable)
        worktrees/<ISSUE-KEY>/    # per-assignment git worktrees
        artifacts/                # screenshots, renders, logs, evidence
    logs/
```

The repo holds **design + code**; the engine **home** holds all mutable state and is never
committed (`.gitignore`). Both live on the SSD.

## Install (once implementation exists)

```bash
# 1. clone (already done, on the SSD)
cd "/Volumes/Extreme SSD/ClaudWorkerV2"

# 2. build the binary (later, when code exists)
go build -o cwv2 ./cmd/cwv2

# 3. one-time project init: create engine home + project config + brain
./cwv2 init --project example --config "/Volumes/Extreme SSD/cwv2-home/projects/example/cwv2.yaml"

# 4. verify config + secrets + repos + plugins
./cwv2 doctor --project example
```

`doctor` checks: secrets resolve (keychain/Azure KV), repos reachable, `development` exists per repo,
Jira JQL/transitions valid, plugins/toolchains present, SSD writable. It refuses to run on any
failure with a precise message (no silent wrong defaults).

## Run

```bash
# foreground (dev): scheduler + orchestrator + dashboard
./cwv2 serve --project example
# → dashboard at http://127.0.0.1:8790  (token printed)
```

Controls (also on the dashboard): `./cwv2 pause`, `./cwv2 resume`, `./cwv2 status`,
`./cwv2 stop-issue <KEY>`, `./cwv2 tool <name> …` (run any deterministic tool by hand).

## Scheduling (local, not cloud — P1, C-5)

- Background operation uses **launchd** on macOS (a `LaunchAgent` plist), starting `cwv2 serve` on
  login and keeping it alive. This keeps everything local; **no** cloud `/schedule` routines (which
  would store prompts/results server-side under a shared account).
- The engine's own internal scheduler handles polling/maintenance intervals; launchd only keeps the
  daemon running.

## Usage guard & cost posture (NFR-2/3)

- The guard reads the Claude plan usage (keychain credential → usage endpoint) and pauses spawning
  at `pause_pct`, resumes at `resume_pct`, with a per-account policy (owner: admin@example.com → 99%,
  others → ~80%). Fails **open** on read error (never blocks forever) but **never** forces
  pay-as-you-go/override.
- Because deterministic work is free, throughput while paused is only limited on the *reasoning*
  steps; deterministic gates keep running.

## Mac Mini (on-demand only)

- iOS/macOS builds, TestFlight, and macOS notarization run on the **Mac Mini** *when needed*, never
  as a hard dependency (the laptop does everything else: dev, QA, ESP32, Android, sims, flutter,
  kicad). The engine dispatches these as deterministic remote tool invocations (ssh) gated by config;
  if the Mac Mini is unavailable, those specific checks **defer** ([06](06_QA.md)), they don't block.

## Backups

- The engine home is on the SSD. The **authored knowledge** (`decisions/`, `conventions.md`) and
  deferrals are periodically exported and can be committed to a knowledge repo (aligns with the
  owner's memory-backup practice), so a lost SSD loses only rebuildable state (NFR-7), not decisions.
- The DB is WAL-mode; a nightly integrity check + copy provides a restore point.

## Upgrades

- Rebuild the binary from `development`; `cwv2 doctor` re-validates; schema migrations run
  forward-only at startup ([12](12_Database.md)). Config is backward-compatible or the loader
  reports exactly what changed.

## Security posture (NFR-6)

- Dashboard bound to localhost + startup token by default. Secrets from vault only, never logged,
  never committed. Worker subprocesses get least-privilege tool sets and their own worktrees. No
  destructive git on shared trees. Optional authenticated tunnel later — never required.

## Bring-up order (matches "implement one subsystem at a time")

Implementation, once the architecture is frozen, proceeds subsystem-by-subsystem, each fully working
before the next:

1. **Toolbelt core** (`git.*`, `jira.*`) + `cwv2 tool` CLI + `doctor`.
2. **Database + Brain** (indexers, prompt-assembly) — rebuildable, testable standalone.
3. **Worker Runner** (spawn `claude -p`, schema-validate one worker type end-to-end).
4. **Orchestrator state machine** for the software path (Manager→Dev→gate→QA→Integrator→close).
5. **Dashboard** (read model + controls).
6. **Plugins**: flutter, dotnet, web — then hardware (esp32/pcb/cad) with deferral.
7. **Usage guard, launchd, notifications, Mac-Mini dispatch.**

No subsystem ships until it works on a real Example issue. Nothing is built simultaneously.

## Definition of operational done

The Engine can be started with `cwv2 serve`, pick a real `Ready` Example issue, drive it to a
`--no-ff` merge on `development` with evidence, close it in Jira, delete the branch, and record the
run — using zero tokens for deterministic steps and a small prompt for each reasoning step, with the
usage guard and locks honored, and any impossible checks deferred with follow-ups. When that holds
for the common case and token-per-issue beats V1, V2 is validated and can replace V1.
