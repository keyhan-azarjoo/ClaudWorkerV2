# Operator Guide

How to run and operate ClaudWorker V2 in production.

## Commands

| Command | Purpose |
|---|---|
| `cwv2 serve --config <cfg> --mode live|simulation [--bind :8080] [--web <dir>]` | run the Orchestrator + Control Plane |
| `cwv2 validate --config <cfg>` | startup/config gate (run before serve) |
| `cwv2 doctor --config <cfg>` | environment check (tools, secrets, engine home) |
| `cwv2 backup --config <cfg> --to backup.tgz` | back up durable state |
| `cwv2 restore --config <cfg> --from backup.tgz` | restore durable state |
| `cwv2 migrate --from <V1 dir> --to <out>` | import ClaudWorker V1 config (read-only) |
| `cwv2 stress --issues 100 --restart-after 30` | deterministic regression/stress run |
| `cwv2 knowledge …` / `cwv2 assignment list …` / `cwv2 git …` / `cwv2 jira …` | inspect subsystems |

## Modes

- **Simulation** (`--mode simulation`): full loop, no external services/credentials/hardware. Use for
  demos, regression, and CI.
- **Live** (`--mode live`): real Jira + Git + Claude + resource discovery + build/API/web verification.
  Requires credentials (see Configuration Guide) and a reachable repo.

## Monitoring

- **Health:** `GET /v1/healthz` (200 = up).
- **Status:** `GET /v1/status` (orchestrator running, last issue, subsystem status).
- **Metrics:** `GET /v1/metrics` (counters, lease/resource aggregates, runtime executions).
- **Events (live timeline):** `GET /v1/events` (SSE) — every transition.
- **Console:** point `--web` at `web/ops-console` (or serve statically) and open it; set the API base
  URL + token in Settings.

## Day-2 operations

- **Start:** `cwv2 validate` then `cwv2 serve` (or the systemd/launchd unit, which validates first).
- **Graceful stop:** SIGINT/SIGTERM — the loop finishes the current step and the HTTP server drains.
- **Backups:** schedule `cwv2 backup` (durable state only: knowledge, assignments, leases). Restore
  with `cwv2 restore`.
- **Log rotation:** use `deploy/logrotate/cwv2`.
- **Upgrade:** stop → replace binary → `cwv2 validate` → start. Durable state (with `spec_version`) is
  migrated automatically; incompatible newer formats are refused, never guessed.
- **Pausing spend:** the Budget policy pauses at the usage threshold; accounts cooldown on rate-limit
  and the loop fails over. To hard-pause, stop the service.

## Autonomy

Once running in live mode, the platform watches Jira, claims work, develops with Claude, verifies,
improves, merges, updates Jira, and releases resources — without human intervention. Human input is
needed only for hardware tests and Automation-gated approvals.
