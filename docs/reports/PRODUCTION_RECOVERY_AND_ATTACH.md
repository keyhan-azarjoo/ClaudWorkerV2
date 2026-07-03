# Production Recovery + Example Attach + Idle/Manual-Start

Date: 2026-07-02. All five priorities complete and externally validated.

## 1. Production Recovery Report

**agents.example.com was momentarily offline during a live-runtime redeploy, not a real outage.**
The live runtime runs on the Mac (Claude-local rule) behind a reverse tunnel → Caddy. Each time the
binary is rebuilt and the launchd job is `kickstart`-ed, there is a ~5 s window where `:8787` is down,
so the tunnel has nothing to forward and Caddy returns 502. That window is what was observed.

Chain checked (all healthy now): DNS `agents.example.com → 85.215.240.37` ✓ · HTTPS Let's Encrypt cert
valid ✓ · Caddy container Up ✓ · reverse tunnel `172.18.0.1:9787` LISTENING ✓ · Mac live runtime
launchd `com.example.cwv2-live` up, `:8787` listening ✓ · auth gate returns 401 without token ✓.
Stability: 5/5 external hits `200`, SSE connects, API responds. (The stale `PermissionError` lines in
the runtime log are from before the `live.env`/TCC fix and are harmless.)

Residual risk: the public URL depends on this Mac + tunnel (the owner-chosen topology); a redeploy
blips it for a few seconds. Rollback to the VPS Simulation container remains one command.

## 2. Website URL confirmation

`https://agents.example.com` → **200**, `<title>ClaudWorker V2 — Operations Console</title>`, API +
SSE + auth all working externally.

## 3. Project Attachment Report

The Example project is the default, persisted project (config `~/.cw-live/cwv2.yaml`, loaded by launchd
— no manual reconfiguration per start):

- **Repository:** `backend` = `github.com/keyhan-azarjoo/DotNet-IoT-MainWebApi.git`, branch
  `development`, seeded clone present, `git.status` = clean.
- **Build command:** `dotnet build` (replaces the default `go build ./...`, which is wrong for a .NET
  repo — this was the verification mismatch). **API verification:** `https://api.example.com/health`.
- **Worktrees:** created per assignment under the engine home (disposable).
- **Jira:** connected (`example.atlassian.net`, project SCRUM), auth verified, queue live.
- **Knowledge Brain:** loaded (file store under engine home; currently empty — no entries yet).
- **Resources / accounts:** 10 Claude accounts discovered from `~/.cw-accounts`, all healthy;
  runtime-selection + cooldown wired.

Note: the pipeline processes one active repo (`repos[0]` = backend). Additional Example repos
(mobile-app, website) can be made the active project by switching `repos[0]` + build/verify flags;
multi-repo concurrent processing is not part of the frozen architecture.

## 4. Dashboard Status Report

The Operations Console shows: current project + repo status (git.status), Jira connection
(jira.queue), Claude account status (accounts.list, 10 healthy), resources + health
(resources.snapshot, metrics), pending work (jira.queue = ready-labelled), and a new topbar
**state badge + Start/Stop control**. Default state on load: **○ Idle** (`active:false`,
`state:"idle"`, `running:true`).

## Idle-by-default + manual start (platform change)

The serve loop now starts **idle** (`Config.StartActive=false`): it stays alive but claims no work and
does not auto-resume interrupted assignments until started. New Control Plane commands
`orchestrator.start` / `orchestrator.stop` drive the console's **Start Working / Stop** button; status
reports `state=idle|working`. Regression test `TestIdleByDefaultSkipsResume`. Suite 27/27 `-race`.

Functional proof (external, via the API the button calls):
`orchestrator.start` → `active=true state=working`; `orchestrator.stop` → `active=false state=idle`;
`assignments.list` empty throughout (nothing auto-claimed).

## Answers

- **Is https://agents.example.com online?** Yes — 200, verified externally, stable.
- **Is the Example project fully attached?** Yes — backend repo on `development`, `dotnet build` +
  API-health verification, Jira connected, 10 accounts healthy, Knowledge Brain loaded, as the default
  persisted project.
- **Is the platform idle and waiting for your click?** Yes — `state:"idle"`, `active:false`, no work
  claimed.
- **Can you press one button to start processing Jira issues?** Yes — the topbar **Start Working**
  button (→ `orchestrator.start`) begins claiming/processing the Jira queue; **Stop** returns to idle.
- **Remaining blocker?** None for online/attached/idle/manual-start. To actually *produce* work you
  must label a SCRUM ticket `ready` (the queue is currently empty by design). Availability depends on
  this Mac + tunnel (owner-chosen topology); a redeploy blips the URL for a few seconds.
