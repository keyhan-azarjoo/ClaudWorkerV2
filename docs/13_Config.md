# 13 — Config

Configuration is **the only project-specific surface** (P10). To run the Engine against a new
project, you write a config file (and, if a new project type is involved, a plugin —
[11](11_Plugins.md)). You never edit engine source (FR-26).

## Files

- **Project config** — `cwv2.yaml` (per project). Declares repos, Jira, plugins, mappings, gates,
  thresholds. Checked in per project or kept in the engine home; never contains secrets.
- **Secrets** — loaded at runtime from the macOS **keychain** and/or **Azure Key Vault** (`az`), and
  referenced by *name* in config, never by value (NFR-6, C-2/C-5). Never committed.
- **Engine settings** — global defaults in `engine.yaml` (engine home path, default concurrency,
  usage thresholds), overridable per project.

## Project config schema (illustrative)

```yaml
project: myotgo
engine_home: /Volumes/Extreme SSD/cwv2-home     # SSD (C-6)

jira:
  base_url: https://<site>.atlassian.net
  auth: { user_secret: jira_user, token_secret: jira_token }   # names → vault
  work_jql: 'project = SCRUM AND status = "Ready" AND assignee = currentUser() ORDER BY priority DESC, rank ASC'
  status_map:
    ready:      ["Ready", "Selected for Development"]
    in_progress:"In Progress"
    in_review:  "In Review"
    done:       "Done"
  ac_field: description            # or a custom field id
  labels: { needs_human: needs-human, owner_working: owner-working, deferred: deferred, engine: engine }

github:
  user: keyhan-azarjoo             # remote/gh identity (C-2)
  commit_identity: { name: keyhanazarjoo, email: keyhanazarjoo@gmail.com }   # author (C-2)

repos:
  - name: app
    url: https://github.com/myotgo/Flutter-IoT-MobileApp
    dev_branch: development
    plugin: flutter
    path_hints: ["myotgo/"]
  - name: backend
    url: https://github.com/myotgo/DotNet-IoT-MainWebApi
    dev_branch: development
    plugin: dotnet
  - name: firmware
    url: https://github.com/myotgo/Arduino-*
    dev_branch: development
    plugin: esp32-firmware

component_map:                      # Jira component/label -> repo + plugin (optional)
  App: app
  Backend: backend
  Firmware: firmware

workflow:
  max_concurrent: 3
  max_attempts: 3
  merge: { strategy: no-ff, delete_branch: true }
  refresh_before_gates: true

usage_guard:
  provider: claude-cli
  pause_pct: 95
  resume_pct: 90
  per_account: { "admin@myotgo.com": 99, default: 80 }   # owner policy
  fail_open: true

qa:
  prefer_real_device: true          # owner rule C-4
  imgdiff_tolerance: 0.02

dashboard:
  bind: 127.0.0.1
  port: 8790
  token: auto                       # printed at startup

notifications:
  telegram: { enabled: false, chat_secret: tg_chat, token_secret: tg_token }

confluence:                         # optional deterministic publisher ([08])
  enabled: false
```

## Configuration principles

- **Names, not secrets.** Every credential is a reference to a vault entry. A leaked config file
  leaks nothing sensitive.
- **Everything portable is here.** JQL, status names, AC location, labels, repo list, branch names,
  plugin selection, gates, thresholds, device preference — all config. If the engine hard-codes any
  of these, that's a portability defect (P10).
- **Sane defaults.** Every field has a default in `engine.yaml`; a minimal project config only needs
  Jira coordinates, the repo list, and plugin choices.
- **Validation on load.** The Engine validates config at startup (referenced secrets resolvable,
  repos reachable, plugins exist, status transitions available) and refuses to start on a broken
  config with a precise error — never a silent wrong default.

## Precedence

`built-in defaults` < `engine.yaml` < `cwv2.yaml (project)` < `dashboard live overrides (persisted)`.
Live overrides from the dashboard (concurrency, thresholds) are written back to the project config so
they survive restart.

## Secrets resolution order

1. macOS keychain (`security find-generic-password`) — for the Claude credential and local secrets.
2. Azure Key Vault (`az keyvault secret show`) — for shared/cloud secrets.
3. Environment (only for local dev; discouraged for real secrets).

Resolved secrets are injected into worker/tool subprocess env at spawn time and **never logged**
(NFR-6).

## MyOTGO as "just a config"

The MyOTGO deployment is expressed entirely as one `cwv2.yaml` (the repos, the SCRUM board, the
plugins app/backend/firmware/pcb/cad, the owner's usage policy, real-device preference, hardware
gates) plus the MyOTGO Brain. No engine code is MyOTGO-aware. Onboarding "project #2" is a second
`cwv2.yaml` — the proof of P10.
