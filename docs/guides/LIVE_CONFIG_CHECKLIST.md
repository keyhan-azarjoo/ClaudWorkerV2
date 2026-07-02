# Live Configuration Checklist

Exactly what must be provided to go live. Items marked **MISSING** are the only remaining blockers;
everything else is prepared (`deploy/myotgo/cwv2.yaml`). No manual searching required — this is the
complete list.

## Credentials (secret NAMES already referenced in config; provide VALUES in Keychain/Azure KV/env)

| Secret name | What | Status |
|---|---|---|
| `myotgo-jira-email` | Atlassian account email | **MISSING** — provide value |
| `myotgo-jira-token` | Atlassian API token | **MISSING** — provide value |
| `myotgo-controlplane-token` | Control Plane bearer token | **MISSING** — generate + provide |

Verify with `cwv2 doctor --config deploy/myotgo/cwv2.yaml` (checks secret resolution without printing).

## GitHub / Git

| Item | Status |
|---|---|
| Push credentials for `origin` (SSH key or PAT for `keyhan-azarjoo`) on the host | **MISSING** — ensure `git push` works to the pilot repo |
| Pilot repo URL + `dev_branch` | ✅ set (`backend` / `development`) — confirm reachable |

## Claude accounts

| Item | Status |
|---|---|
| At least one logged-in account `config_dir` (`~/.cw-accounts/*` or `~/.claude`) | **MISSING** — log in ≥1 account |
| Account → config-dir mapping | ✅ prepared (`deploy/myotgo/resources.yaml`) |

## SSH keys

| Item | Status |
|---|---|
| Host SSH keys + `known_hosts` for repos + remote build machines | **REUSE existing** — no new keys generated (see Security Guide) |

## Build / verification commands (per repo)

| Repo | Build cmd | Verify | Status |
|---|---|---|---|
| backend (.NET) | `dotnet build` | API `https://api.myotgo.com/health` | **CONFIRM** command + URL |
| mobile (Flutter) | `flutter build apk --debug` | — | **CONFIRM** if piloted |
| website (NextJs) | `npm run build` | web `https://myotgo.com` | **CONFIRM** if piloted |

Passed to `cwv2 serve` via `--build-cmd`, `--api-url`, `--web-url` (see `deploy/live-acceptance.sh`).

## Repository configuration

| Item | Status |
|---|---|
| `repos[]` (name/url/dev_branch/plugin) | ✅ set for `backend`; uncomment others to add |
| `work_jql` (eligibility queue) | ✅ set (`status="To Do" AND labels=ready`) — **confirm labels** |

## Resource configuration

| Item | Status |
|---|---|
| Accounts + local providers + devices | ✅ prepared (`resources.yaml`); discovery auto-detects live |
| Mac Mini / DGX host details | **MISSING** — add real reach when connected |

## Summary of what is MISSING

1. Three secret VALUES (Jira email, Jira token, Control Plane token).
2. A logged-in Claude account + `origin` push credentials on the host.
3. Confirm per-repo build/verify commands + the `ready` Jira label convention.
4. (Optional, for device verification) physical device fleet + Mac Mini/DGX reach.

Nothing else. Once (1)–(3) are in place, run the live acceptance script.
