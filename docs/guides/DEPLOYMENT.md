# Deployment Guide

Artifacts live in `deploy/`. Every target keeps **Simulation Mode** available.

## Docker

```sh
docker compose -f deploy/docker-compose.yml up -d --build
```
Distroless non-root image; `/data` = engine home (durable volume); healthcheck; 30s graceful stop.
Mount `cwv2.yaml` into `/data` and set `--mode live` for production.

## macOS (launchd)

```sh
sudo cp deploy/launchd/com.example.cwv2.plist /Library/LaunchDaemons/
sudo launchctl load /Library/LaunchDaemons/com.example.cwv2.plist
```
`KeepAlive` = automatic recovery on crash; 30s graceful exit.

## Linux (systemd)

```sh
sudo cp deploy/systemd/cwv2.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now cwv2
```
`ExecStartPre` validates the config; `Restart=on-failure`; SIGTERM graceful; hardened
(`ProtectSystem=strict`, `NoNewPrivileges`, `StateDirectory=cwv2`).

## Windows (Service)

Run `deploy/windows/install-service.ps1` in an elevated PowerShell. Validates config, creates the SCM
service (`start=auto`), configures `sc failure` auto-restart.

## Install (any)

```sh
PREFIX=/usr/local ./deploy/install.sh   # builds + installs cwv2 + ops-console
```

## Pre-flight

1. `cwv2 validate --config <cfg>` (config + startup gate).
2. `cwv2 doctor --config <cfg>` (tools + secrets + engine home).
3. First run in `--mode simulation` to confirm the loop; then `--mode live`.

## Health, monitoring, logs

- Health: `GET /v1/healthz`. Monitoring: `/v1/status`, `/v1/metrics`, `/v1/events` (SSE).
- Logs: structured `slog`; rotate with `deploy/logrotate/cwv2`.

## Backups & upgrades

- Backup durable state: `cwv2 backup --config <cfg> --to backup.tgz` (schedule it). Restore:
  `cwv2 restore --config <cfg> --from backup.tgz`.
- Upgrade: stop → replace binary → `cwv2 validate` → start. Durable formats migrate automatically;
  newer-than-supported formats are refused (never guessed).

## Not included (by request)

No CDN/registry publishing pipeline is bundled; images/binaries are built locally via the above.
