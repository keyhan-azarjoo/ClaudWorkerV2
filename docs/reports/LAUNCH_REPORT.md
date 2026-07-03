# ClaudWorker V2 — Launch Report

Final launch-preparation report. No new features; only launch-blocker removal + production prep.

## Current readiness

**Release Candidate (8.5/10) — ready for first live deployment pending credentials.**

- `go test -race ./...` 27/27 · `gofmt`/`go vet`/`deadcode` clean · Simulation Mode processes the full
  loop end-to-end.
- Deployment artifacts verified on this host: **launchd plist valid** (`plutil`), **Dockerfile linted
  clean** (`docker build --check` — base images resolve, no warnings), all shell scripts `bash -n`
  clean. systemd/Windows units verify on their target OS (not this macOS host).
- Launch tooling ready: `cwv2 validate`, `cwv2 serve` (with `--build-cmd/--api-url/--web-url`),
  `deploy/live-acceptance.sh` (one-command live validation), `deploy/cutover.sh` (V1→V2), Example
  bootstrap config (`deploy/example/`).

## Remaining blockers (all external — none are code or architecture)

1. **Credentials** (3 secret values): `example-jira-email`, `example-jira-token`,
   `example-controlplane-token`.
2. **GitHub push** creds for `origin` on the host + ≥1 logged-in Claude account `config_dir`.
3. **Confirm** per-repo build/verify commands + the `ready` Jira label convention.
4. **(Optional, device verification only)** physical device fleet + Mac Mini/DGX reach.

Full detail: `docs/guides/LIVE_CONFIG_CHECKLIST.md`.

## Credentials still required

| Secret | Purpose |
|---|---|
| `example-jira-email` | Atlassian account email |
| `example-jira-token` | Atlassian API token |
| `example-controlplane-token` | Control Plane bearer token |
| GitHub PAT/SSH (host) | push merges to `origin` |
| Claude account login (`~/.cw-accounts/*`) | run the worker |

## Hardware still required

- **None** for the first live run (backend build + API/web verification are headless).
- **For device/visual verification only:** Android/iPhone/ESP32 + Mac Mini/DGX/Windows build server
  (the fleet is discovered automatically once connected; `resources.yaml` documents it).

## Estimated launch time after credentials are provided

**~15–30 minutes.** Populate 3 secrets + log in one Claude account + confirm the build command →
`deploy/live-acceptance.sh deploy/example/cwv2.yaml --build-cmd "dotnet build" --api-url https://api.example.com/health`
→ observe the loop claim→…→merge→Jira Done.

## Estimated time to Production Ready after first successful live run

**~1–2 days of supervised operation.** After the first green live run: run a small batch of real
issues, watch a restart/recovery cycle, confirm backups against the live engine home, then a short
soak (a day). Add physical-device visual verification when hardware is connected. Then tag `v2.0.0`
(Production Ready). Interim tag: **`v2.0.0-beta.1`** after the first successful live acceptance run.

## Launch sequence (the only remaining actions)

1. Provide the 3 secrets + GitHub push creds + one Claude account login.
2. `cwv2 validate --config deploy/example/cwv2.yaml`
3. `deploy/live-acceptance.sh deploy/example/cwv2.yaml --build-cmd "<repo build>" [--api-url …]`
4. On PASS → install the service unit (`deploy/systemd` or `deploy/launchd`) and let it run.
5. Cutover from V1 when confident: `deploy/cutover.sh <V1 dir> deploy/example/cwv2.yaml --go`
   (backs up V1, imports config, starts V2, archives V1 read-only — never deletes V1).

## Stop

Launch preparation complete. **Stopping.** Awaiting the owner's credentials, hardware, and launch
approval. The next work is operating the platform — not redesigning it.
