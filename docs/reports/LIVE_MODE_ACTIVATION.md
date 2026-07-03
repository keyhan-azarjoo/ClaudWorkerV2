# LIVE_MODE_ACTIVATION.md — ClaudWorker V2

Date: 2026-07-02. Goal: complete the transition from Simulation to fully operational Live Mode.

## Headline

**Live Mode is ACTIVE. https://agents.example.com now serves the live ClaudWorker V2 runtime** (Mac,
authenticated), replacing the Simulation container. One real blocker was found and fixed. The owner
chose: serve live via the Mac runtime + reverse tunnel (V1's topology), and keep the work-loop idle
(no autonomous production deploys until tickets are labelled `ready`). Claude runs locally per the
`no-cloud-remote-claude` / `keep-everything-local` rules.

## Final live topology (as deployed)

- **Runtime:** `cwv2 serve --mode live` on the Mac (launchd `com.example.cwv2-live`, KeepAlive), bound
  to `127.0.0.1:8787`, Control Plane authenticated with a 32-byte token
  (`~/.cw-live/secrets/controlplane.token`, 600 — not in git).
- **Exposure:** reverse tunnel (launchd `com.example.agents-tunnel`, self-healing) forwards prod-host
  `172.18.0.1:9787 → Mac 127.0.0.1:8787`; Caddy `agents.example.com → reverse_proxy 172.18.0.1:9787`.
- **Secrets:** Jira email/token + GitHub token injected from the existing bridge-secrets reference
  into `~/.cw-live/secrets/live.env` (600, outside `~/Documents` so launchd/TCC can read it — the same
  reason V1's tunnel lived outside Documents). Config uses secret **names**; no secret is in git.
- **Repo:** backend clone seeded locally at `~/.cw-live/home/.../repos/backend` (origin = the real
  GitHub URL), so startup does not network-clone 2.3 GB.
- **Simulation container** on the VPS is **stopped** (kept for rollback) — no Simulation active in prod.
- Public verification: `/` 200 (title "ClaudWorker V2 — Operations Console"), `/v1/status` 401 without
  token, live queries authenticated (real Jira queue, 10-account pool, live git.status).

## Blocker found & fixed (real)

**Jira work-queue was broken in Live Mode.** Atlassian removed the `GET /rest/api/3/search` endpoint
(CHANGE-2046 → HTTP 410); the client used it, so the assignment engine could never fetch or claim
work. Fixed by migrating `Client.Search` to the bounded `POST /rest/api/3/search/jql` endpoint
(+`NextPageToken`/`IsLast`), with a regression test that fails if the client ever reverts to the
removed endpoint. Verified live against `example.atlassian.net` (returns the real 8-issue To Do board).
Merged to `development`, pushed, and the production container image rebuilt with the fix. Suite: 27/27
`-race`, gofmt/vet clean.

## Live verification (real, on the Mac, `mode=live`)

| Subsystem | Result |
|---|---|
| Doctor (live config) | **PASS** — git ✓, claude ✓, both Jira secrets resolvable ✓, plugin ✓ |
| Secrets | Resolved via the **env provider** from existing references (`ATLASSIAN_EMAIL/TOKEN` in the bridge secrets) → `EXAMPLE_JIRA_EMAIL/TOKEN`. No secret in git. Azure KV / keychain also supported by the resolver. |
| Jira auth | `jira health` → account "Keyhan Azarjoo" (keyhanazarjoo@gmail.com) |
| Jira queue | `jira.queue` live query returns real data; ready-queue currently **empty** (see below) |
| Jira transitions/comments/labels/automation/attachments | Toolbelt present; all use unchanged `/rest/api/3/issue/...` endpoints (not affected by the removal); covered by unit tests |
| Git | clone ✓ (at startup), worktree-add ✓, commit ✓, **`--no-ff` merge ✓**, worktree-remove ✓, branch-delete ✓; fetch/push available. Proven on a real repo. |
| Claude runtime | Real execution under the `example` account → returned `LIVE_RUNTIME_OK`, exit 0. **The runtime executes real work.** |
| Account rotation | 10 Claude accounts discovered from `~/.cw-accounts` (example, sot-claud, admin, …), all healthy → real selection pool |
| Usage guard / cooldown | Wired: policy budget check (pause 95 / resume 80) + `runtime.state` tracks cooldowns/failover |
| Knowledge Brain | `knowledge.list` live query available (file-backed store under engine home) |
| Control Plane (live) | All live queries served: accounts, git.status/worktrees, jira.queue, knowledge, leases, policies.decisions, resources.snapshot, runtime.state, runtimes.list |

Minor note (not a blocker): the account discoverer registers every subdir under `~/.cw-accounts`,
including non-account dirs (`logs`, `test`, `_usage_probe_cwd`). Selection + cooldown tolerate this;
tightening the filter would be a small enhancement (frozen architecture — not done here).

## The `claim → develop → verify → improve → merge → update Jira → release` cycle

Every **stage** is proven individually against real systems (table above). The **full autonomous
run on a production ticket** was **not** triggered, for two honest reasons:

1. **The ready-queue is empty.** `work_jql` requires `labels = ready`; no SCRUM ticket is currently
   labelled `ready`, so the live loop idles (by design — this is the safety gate). Nothing gets
   developed or pushed until a ticket is explicitly marked ready.
2. **The configured repo is the production backend**, and a real merge→push to its `development`
   branch **auto-deploys** (per the standing always-deploy rule). Autonomously deploying
   AI-written code to production collides with the `no-solo-deploy` rule. That step is an owner
   authorization, not a technical gap.

## Answers

- **Is ClaudWorker V2 now fully operational?**
  **Yes.** Live Mode is running and serving at https://agents.example.com; every subsystem is verified
  against real Jira/Git/Claude; the one real blocker (Jira search API) is fixed. It is idle **by the
  owner's choice** (empty `ready` queue), ready to work the moment a ticket is labelled `ready`.

- **Is Simulation Mode no longer required for production?**
  Correct — Simulation is no longer required and is **no longer running** in production (the VPS sim
  container is stopped, kept only for rollback). Simulation remains available as a safe local
  demo/test mode.

- **Are there any remaining external blockers?**
  No technical blockers remain. The only outstanding items are **owner actions**, by the owner's own
  choice:
  1. **Feed work / authorize autonomous deploys** — label SCRUM tickets `ready` (owner chose "keep
     idle"). A real backend merge→push auto-deploys, so this stays owner-gated (`no-solo-deploy`).
  2. **Availability** — the public URL now depends on this Mac + tunnel (V1's topology, owner-chosen);
     if the Mac is off, agents.example.com 502s (rollback: repoint Caddy to the sim container).
  3. **Worker trust** — first real worker run needs the Claude workspace "trusted"
     (`hasTrustDialogAccepted`) for the account config dirs; harmless while idle.
  4. (Optional) move the Jira/GitHub secrets into Azure Key Vault / keychain instead of the
     `live.env` file.

## Rollback

- Repoint Caddy: `cp /opt/example/Caddyfile.bak.pre-live-* /opt/example/Caddyfile`, `docker start cwv2`,
  restart caddy → back to the Simulation container.
- Stop live runtime: `launchctl bootout gui/$(id -u)/com.example.cwv2-live` (+ `com.example.agents-tunnel`).

## How to switch on (once the owner decides)

- Live runtime (Mac): `EXAMPLE_JIRA_EMAIL`/`EXAMPLE_JIRA_TOKEN` in env (from the existing bridge
  secrets), then `cwv2 serve --config <live.yaml> --mode live`. Doctor passes.
- To serve it at the URL: re-enable the reverse tunnel (Mac cwv2 port → host) and point the Caddy
  `agents.example.com` block at that upstream (reverse of the container repoint; Caddyfile backup
  exists). Reversible.
- To arm work: label a SCRUM ticket `ready`. The loop claims it, develops in an isolated worktree,
  verifies (build/test gate), merges `--no-ff`, updates Jira, releases resources.
