# 09 — Dashboard

A local control panel for observing and steering the Engine. Deterministic Go web server, localhost
by default, no JS framework, no build step (NFR-4). It is a **view + a few controls** over the DB
and the Orchestrator — it holds no authority of its own.

## Goals

- See, at a glance, what the Engine is doing: which issues are in flight and at what stage, live
  worker progress, token/cost, usage-guard state, blockers, and open deferrals.
- Steer safely: run / pause / resume / stop, without corrupting state.
- Stay simple and local (P1, P6). Optional private tunnel later; never a hard dependency.

## Serving

- `cwv2 serve` starts the daemon which includes the dashboard on `http://127.0.0.1:<port>` (default
  from config). Bound to localhost by default (NFR-6); a token is printed at startup for access.
- Transport: plain HTML + **Server-Sent Events** for live updates (matches V1's dependency-free
  approach). No React/build pipeline.

## Views

### 1. Overview
- KPI front and center (P7): **issues closed** (today / week / total), issues in flight, deferrals
  open, needs-human count. Token/cost per completed issue trend.
- Usage-guard state (current plan %, paused/running) and concurrency (active / max).

### 2. Issues in flight
- One row per active issue: key, summary, current state (CLAIMED→…→MERGING), current worker type,
  live progress %, attempt count, branch/worktree, elapsed, tokens/cost so far.
- Drill-in: the assembled prompt context slices (task/AC/files/decisions/failures), the worker's
  latest structured output, links to evidence (screenshots/logs), and the per-issue history from the
  Brain.

### 3. Workers (live)
- Currently running `claude -p` workers: type, issue, elapsed, token budget consumed, streamed log
  tail. This is a *process* view; workers are ephemeral so rows appear and vanish.

### 4. Deferrals
- All open deferrals ([03](03_Workflow.md)/[06](06_QA.md)): kind, reason, how-to, environment
  needed, linked follow-up Jira key. Lets the owner clear them when the device/hardware is ready
  ("re-run this deferred check now" action when the environment is present).

### 5. Needs-human
- Issues flagged `needs-human`: the specific blocker, last failure detail, evidence, and a
  "re-queue" action after the human resolves it.

### 6. Devices
- Connected devices/simulators/boards (from `device.list`, plugin-specific): phones (adb), iOS
  sims/real device, ESP32 boards, etc. Shows what QA rungs are currently possible (drives whether a
  check runs or defers).

### 7. Brain browser
- Read-only view of the Project Brain: architecture summary, recent ADRs, known failures, index
  stats. Helps the owner see what the Engine "knows".

## Controls (safe, deterministic)

- **Run / Pause / Resume**: pause stops admitting/spawning **new** workers; in-flight deterministic
  work finishes cleanly. Resume re-enables admission. (Same semantics as the usage guard.)
- **Stop issue**: cancel an in-flight issue → kill its worker, clean its worktree, release its lock,
  return the Jira issue to its prior stable status. No half-merge.
- **Re-queue**: send a needs-human issue back to Ready after resolution.
- **Re-run deferred check**: when the environment is now available.
- **Adjust concurrency / usage thresholds**: within safe bounds, persisted to config.

All controls act through the Orchestrator/Lock manager so invariants (03, 07) always hold; the
dashboard never mutates git or the DB directly.

## Data model

- **Read model:** queries over the SQLite DB ([12_Database](12_Database.md)) — runs, issues cache,
  locks, deferrals, failures, cost.
- **Live model:** SSE stream from the Orchestrator for progress ticks and worker log tails.
- No separate dashboard database; it is a pure projection (P6).

## Notifications

- Optional owner notifications (e.g. Telegram) for: needs-human, long blocks, usage-guard pause,
  and (on request) run summaries. Deterministic sender; concise messages. Configurable and off by
  default for portability (P10).

## Security

- Localhost bind + startup token by default (NFR-6). Secrets are never rendered. If a private tunnel
  is added later, it must be authenticated (e.g. Cloudflare Access) — but the tunnel is **optional**
  and never required (P1).

## Non-goals

- Not a public web app. Not a multi-user console. Not a place to author work (that's Jira). It is an
  operator's window into a local engine, nothing more.
