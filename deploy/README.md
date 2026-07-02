# Deployment (Phase C)

Artifacts to run ClaudWorker V2 in production. All keep **Simulation Mode** available (`--mode simulation`).

| File | Platform | Notes |
|---|---|---|
| `Dockerfile` + `docker-compose.yml` | container | distroless non-root; `/data` = engine home; healthcheck; graceful stop |
| `launchd/com.myotgo.cwv2.plist` | macOS | `KeepAlive` = auto-recovery; 30s graceful exit |
| `systemd/cwv2.service` | Linux | `ExecStartPre` validates config; `Restart=on-failure`; SIGTERM graceful |
| `windows/install-service.ps1` | Windows | SCM service; `sc failure` = auto-restart; validates config first |
| `logrotate/cwv2` | Linux/macOS | daily rotation, keep 14, compress |
| `install.sh` | any | build + install binary + ops-console |

Operational commands: `cwv2 validate` (startup/config gate), `cwv2 backup`/`restore` (durable state),
`cwv2 doctor` (environment), `/v1/healthz` (health check), `/v1/metrics` + `/v1/status` (monitoring).
