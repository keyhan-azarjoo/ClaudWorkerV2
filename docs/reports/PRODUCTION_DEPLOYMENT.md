# Production Deployment Report — ClaudWorker V2 replaces V1 at agents.example.com

Date: 2026-07-02. Executed live against the production host `85.215.240.37` (shared Example backend).
Result: **https://agents.example.com now serves ClaudWorker V2. V1 is retired.** No product service
was interrupted.

## Outcome vs goal

| Goal | Status | Evidence |
|---|---|---|
| agents.example.com runs V2 | ✅ | `GET /` → 200, `<title>ClaudWorker V2 — Operations Console</title>`; `/v1/status` → orchestrator running |
| V1 no longer running | ✅ | 4 V1 launchd jobs booted out + renamed `.disabled`; no V1 process on Mac; `:8787`/`:9787` free |
| Old console no longer accessible | ✅ | old upstream `172.18.0.1:9787` removed from Caddy; tunnel disabled |
| URL unchanged | ✅ | same domain, same Let's Encrypt cert (CN=agents.example.com, exp 2026-09-10) |
| Users see the new UI | ✅ | V2 SPA + assets (js/css) all 200 over HTTPS |
| No unrelated production disruption | ✅ | api/mongo/emqx/support-ai/mcp/marketing/coturn/step-ca untouched and Up throughout |

## What was actually discovered (V1's real architecture)

V1 did **not** run on the production host. It ran on the **local Mac** as launchd job
`com.example.claudworker` (Go binary `Claud_worker_agent/claudworker -serve`, dashboard on `:8787`),
exposed to the internet by a **reverse SSH tunnel** (`com.example.agents-tunnel` →
`85.215.240.37:172.18.0.1:9787`), which Caddy proxied as `agents.example.com → 172.18.0.1:9787`.
At deployment time all V1 launchd jobs were already **unloaded** and the tunnel was down — that was
the live 502.

## Deployment (surgical, reversible)

1. **Recon** (read-only): confirmed the host runs the whole shared product; `:9787` dead; no V1
   systemd/cron on host.
2. **Build**: rsynced V2 source → `/opt/cwv2-src`; `docker build` → `example/cwv2:latest` (multi-stage,
   distroless, non-root).
3. **Config + secret**: generated a 32-byte Control Plane bearer token on the host
   (`/opt/cwv2/secrets/controlplane.token`, `chmod 600`); wrote runtime config to the `/data` volume
   (`/opt/cwv2/data/cwv2.yaml`, owned by the container's non-root uid, `chmod 600`) — Jira uses secret
   **names** (keychain/Azure KV/env), the CP token is a runtime value in the root-only volume, **never
   committed to git**.
4. **Run**: `cwv2` container on the existing Caddy network `example_example`, `--restart unless-stopped`,
   `--mode simulation`, Control Plane authenticated.
5. **Repoint proxy**: backed up `/opt/example/Caddyfile`; changed only the `agents.example.com` upstream
   `172.18.0.1:9787 → cwv2:8080`; `caddy validate` = Valid. (Graceful `reload` did not apply — a stale
   persisted admin config held the old upstream — so Caddy was **restarted** to load the file
   deterministically: ~3 s proxy blip, all upstream containers kept running.)
6. **Validate** (public HTTPS): see below.
7. **Fix**: static assets initially 403 (source tree carried owner-only `0700` perms from the exFAT
   SSD → non-root user couldn't read them). Fixed in `deploy/Dockerfile` with `COPY --chmod=0755`,
   rebuilt, recreated the container. Assets now 200.
8. **Retire V1**: archived V1 plists + `config.json` + tunnel script to
   `~/cwv2-migration-backups/v1-retirement-<ts>/`; `launchctl bootout` + renamed the 4 V1 jobs to
   `.disabled` (reversible). Left the unrelated `agent-monitor` and all other `com.example.*` jobs
   alone.

## Validation results (over https://agents.example.com)

- Operations Console: `/` 200, title correct, `js/app.js` `css/app.css` `js/api.js` 200.
- API: `/v1/status` 200 (orchestrator running), `/v1/metrics` 200 (AssignmentCompleted=3…),
  `/v1/queries` + `/v1/commands` 200.
- Queries: `resources.snapshot` 200 (claude account `healthy`); assignments/leases/knowledge present.
- **SSE**: `/v1/events` streams (PolicyDecision events incl. usage-guard "within budget usage 0% <
  pause 95%").
- **Auth**: `/v1/status` **without** token → 401 (Control Plane not publicly exposed unauthenticated).
- **Simulation Mode**: active and processing (`last_issue=SIM-3`).
- Health: `/v1/healthz` 200.
- Product services: caddy + api + mongo + emqx (+ staging) + support-ai + mcp + marketing all Up.

## Migration of V1 capabilities

| V1 capability | Where it lives in V2 |
|---|---|
| account management / Claude rotation | resource discovery + runtime-selection policy (`resources.snapshot`, `accounts.list`); V1's `config.json` (accounts, paces, pause pct) archived for reference |
| usage guard / cooldown / pacing | `usage_guard` (pause 95 / resume 80) in config; Budget/Retry policies; cooldown visible in SSE |
| scheduling / activation | V2 is a long-running `serve` loop (no external activator needed) |
| dashboards / monitoring / logs / runtime visibility | Operations Console + `/v1/metrics` + SSE `/v1/events` + `docker logs cwv2` |
| deployment configuration | `deploy/` (Dockerfile, compose, example/ configs) + this report |

## Secrets

- Control Plane token: generated on host, `/opt/cwv2/secrets/controlplane.token` (600), used by the
  runtime config in the root-only `/data` volume. Not in git.
- Jira email/token: referenced by **name** only; resolved at runtime via keychain → Azure Key Vault →
  env (V2 secret system). No secret values in the repo.

## Remaining external actions (genuinely cannot be done autonomously)

1. **Live Mode autonomous execution** — the distroless production image intentionally contains no
   `claude` CLI or `git`, and Jira secrets are not yet provisioned on the host. To switch from
   Simulation to Live: provide the Jira email/token secrets (keychain/Azure KV/env) + a Claude-capable
   runtime + GitHub push creds, then run the container with `--mode live`. This is prepared (config +
   secret references in place) and documented; it is an owner/credential action, not a code gap.
2. **Local V1 repo directory** (`Claud_worker_agent`, 366 MB) — services are disabled and artifacts
   archived; the directory itself is left in place per the standing never-delete rule. Deleting it is a
   one-line owner action if desired (archive already taken).
3. **GitHub repo archival** — the V1 GitHub repository may be archived (not deleted) by the owner.

## Rollback (if ever needed)

- Restore Caddy: `cp /opt/example/Caddyfile.bak.pre-cwv2-* /opt/example/Caddyfile` and restart caddy.
- Re-enable V1: rename the `.disabled` plists back and `launchctl bootstrap` them (tunnel + worker).
- `cwv2` container: `docker rm -f cwv2` removes V2 cleanly.

## Verdict

Production cutover **complete** for the service: agents.example.com serves ClaudWorker V2, authenticated,
over the existing domain/HTTPS/proxy, with V1 retired and no product disruption. The only work left is
external credential provisioning to move from Simulation to Live autonomous operation.
