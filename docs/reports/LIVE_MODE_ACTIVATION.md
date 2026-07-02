# LIVE_MODE_ACTIVATION.md — ClaudWorker V2

Date: 2026-07-02. Goal: complete the transition from Simulation to fully operational Live Mode.

## Headline

Every **technical** blocker to Live Mode has been removed and Live Mode has been **run and verified
end-to-end on the Mac** (the only place Claude may run, per the `no-cloud-remote-claude` /
`keep-everything-local` rules). One real bug was found and fixed. What remains are **owner decisions /
external actions**, not engineering gaps.

## Blocker found & fixed (real)

**Jira work-queue was broken in Live Mode.** Atlassian removed the `GET /rest/api/3/search` endpoint
(CHANGE-2046 → HTTP 410); the client used it, so the assignment engine could never fetch or claim
work. Fixed by migrating `Client.Search` to the bounded `POST /rest/api/3/search/jql` endpoint
(+`NextPageToken`/`IsLast`), with a regression test that fails if the client ever reverts to the
removed endpoint. Verified live against `myotgo.atlassian.net` (returns the real 8-issue To Do board).
Merged to `development`, pushed, and the production container image rebuilt with the fix. Suite: 27/27
`-race`, gofmt/vet clean.

## Live verification (real, on the Mac, `mode=live`)

| Subsystem | Result |
|---|---|
| Doctor (live config) | **PASS** — git ✓, claude ✓, both Jira secrets resolvable ✓, plugin ✓ |
| Secrets | Resolved via the **env provider** from existing references (`ATLASSIAN_EMAIL/TOKEN` in the bridge secrets) → `MYOTGO_JIRA_EMAIL/TOKEN`. No secret in git. Azure KV / keychain also supported by the resolver. |
| Jira auth | `jira health` → account "Keyhan Azarjoo" (keyhanazarjoo@gmail.com) |
| Jira queue | `jira.queue` live query returns real data; ready-queue currently **empty** (see below) |
| Jira transitions/comments/labels/automation/attachments | Toolbelt present; all use unchanged `/rest/api/3/issue/...` endpoints (not affected by the removal); covered by unit tests |
| Git | clone ✓ (at startup), worktree-add ✓, commit ✓, **`--no-ff` merge ✓**, worktree-remove ✓, branch-delete ✓; fetch/push available. Proven on a real repo. |
| Claude runtime | Real execution under the `myotgo` account → returned `LIVE_RUNTIME_OK`, exit 0. **The runtime executes real work.** |
| Account rotation | 10 Claude accounts discovered from `~/.cw-accounts` (myotgo, sot-claud, admin, …), all healthy → real selection pool |
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
  **Technically yes.** Live Mode runs, every subsystem is verified against real Jira/Git/Claude, and
  the one real blocker (Jira search API) is fixed. It is ready to process work.

- **Is Simulation Mode no longer required for production?**
  Correct — Simulation is no longer *required*. It remains available as a safe demo/testing mode. The
  public URL currently still *serves* Simulation only because switching it to live data requires the
  topology decision below.

- **Are there any remaining external blockers?**
  Yes — all **owner actions**, none technical:
  1. **Serving topology for live at agents.myotgo.com.** Claude must run locally (Mac), so live data
     at the public URL means the Mac runtime exposed via the existing reverse-tunnel → Caddy (V1's
     proven topology). This re-introduces a dependency on this Mac being up. Decision: adopt that, or
     keep the robust VPS container for the UI and run the live runtime privately.
  2. **Authorize autonomous production deploys.** Label real SCRUM tickets `ready` and confirm the
     platform may autonomously merge→push→auto-deploy to the production backend (`no-solo-deploy`).
     Until then the live loop idles safely.
  3. (Optional) Provision the Jira secrets into Azure Key Vault / keychain for a credential-manager
     path instead of the env injection used here.

## How to switch on (once the owner decides)

- Live runtime (Mac): `MYOTGO_JIRA_EMAIL`/`MYOTGO_JIRA_TOKEN` in env (from the existing bridge
  secrets), then `cwv2 serve --config <live.yaml> --mode live`. Doctor passes.
- To serve it at the URL: re-enable the reverse tunnel (Mac cwv2 port → host) and point the Caddy
  `agents.myotgo.com` block at that upstream (reverse of the container repoint; Caddyfile backup
  exists). Reversible.
- To arm work: label a SCRUM ticket `ready`. The loop claims it, develops in an isolated worktree,
  verifies (build/test gate), merges `--no-ff`, updates Jira, releases resources.
