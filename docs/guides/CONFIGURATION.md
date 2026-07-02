# Configuration Guide

Config is a single YAML file (`cwv2.yaml`) loaded by every command. Validate with
`cwv2 validate --config <cfg>`.

## Minimal example

```yaml
project: myproject
engine_home: /var/lib/cwv2            # durable state root (SSD/persistent volume)
github:
  user: keyhan-azarjoo
  commit_identity: { name: keyhanazarjoo, email: keyhanazarjoo@gmail.com }
repos:
  - name: app
    url: https://github.com/org/app.git
    dev_branch: development
    plugin: generic
jira:
  base_url: https://org.atlassian.net
  project_key: SCRUM
  work_jql: 'project = SCRUM AND status = "To Do"'
  auth:
    user_secret: jira-email          # secret NAMES, resolved via Keychain/Azure KV/env
    token_secret: jira-token
usage_guard: { pause_pct: 95, resume_pct: 80, fail_open: false }
dashboard: { token: <control-plane bearer token> }
```

## Sections

- **project / engine_home** — identity + durable state root. `engine_home/projects/<project>/`
  holds `knowledge/`, `assignments/`, `leases/`, `worktrees/`, `repos/`.
- **github.commit_identity** — author for every commit/branch (must be `keyhanazarjoo`).
- **repos[]** — repositories to work on (name, url, dev_branch, plugin).
- **jira** — base_url, project_key, work_jql (the eligibility queue), `auth.{user_secret,token_secret}`
  (secret NAMES, never values).
- **usage_guard** — Budget policy thresholds (pause/resume %, fail_open).
- **defaults** — retry limits, thresholds, timeouts (see `internal/config`). Sensible defaults apply.
- **dashboard.token** — Control Plane bearer token (empty = open; set in production).

## Secrets

Never put secret values in config. Use secret *names* resolved by the chain Keychain → Azure Key Vault
→ environment. The Jira token, per-account Claude config dirs, and push credentials live outside config.

## Migration from V1

`cwv2 migrate --from <V1 dir> --to <out>` produces `migrated.yaml` (usage guard, concurrency, model,
gate labels) and `resources.json` (accounts + devices) — merge these into your `cwv2.yaml` / resource
declarations. See the Migration Guide.

## Resource / account config (live)

Live discovery finds Claude account config dirs under `~/.cw-accounts/*` and `~/.claude`, plus local
providers (Ollama :11434, LM Studio :1234) and devices (adb/simctl/serial). Declared infra (Mac Mini,
DGX, Windows build server, Pi) is added via the migrated `resources.json` / static discovery.
