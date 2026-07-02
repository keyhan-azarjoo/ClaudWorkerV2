# Troubleshooting Guide

Start with `cwv2 validate --config <cfg>` and `cwv2 doctor --config <cfg>`, then check
`/v1/status`, `/v1/metrics`, and the event stream `/v1/events`.

| Symptom | Likely cause | Fix |
|---|---|---|
| `serve --mode live` fails immediately | missing Jira token / unreachable repo | check `jira.auth.token_secret` resolves; ensure the repo URL is reachable + a real remote |
| Loop idles, no work claimed | `work_jql` matches nothing, or Budget policy paused | verify the JQL in Jira; check `usage_guard` + `/v1/metrics` for pause |
| Assignments defer, never complete | no available runtime account (all cooling / unhealthy) | check `accounts.list`; a rate-limited account cools down and recovers; add accounts |
| Every assignment fails at verification | build/test command wrong for the repo | verification uses `go build ./...` by default — configure the real per-repo build command |
| Merge always fails | genuine conflict, or dev branch diverged | resolve upstream; conflicts auto-abort (clean tree); the assignment fails and the workspace is cleaned |
| Worker returns `authentication` | Claude account not logged in | re-auth the account's `CLAUDE_CONFIG_DIR`; the account cools down until healthy |
| Worker `rate_limit` repeatedly | account throttled | the Resource Manager cools it and fails over; add accounts or wait |
| Restart lost in-flight work? | no — durable state recovers | `Recover` resumes unfinished assignments; completed work is never redone (Law 19) |
| Console shows "not yet available" | that query has no data source (simulation, or serve not live) | run `serve` (live registers `jira.queue`/`git.*`/`runtime.state`) |
| Console can't connect | wrong API base URL / token | set them in the console Settings page; confirm `GET /v1/healthz` |
| `newer format` error on startup | durable state written by a newer version | upgrade the binary; V2 refuses to guess an unknown format |
| Device verification skipped | no connected hardware | build/API/web verify headless; device/visual drivers activate when hardware is present |

## Diagnostics

- **Deterministic repro:** `cwv2 stress --issues 100 --restart-after 30` runs the full loop with
  injected failures + a crash/restart — reproduces recovery behaviour with no external services.
- **Inspect state:** `cwv2 assignment list`, `cwv2 knowledge list`, `cwv2 git worktrees` (via API
  `git.worktrees`), `/v1/query/leases.active`.
- **Logs:** structured `slog`; every transition also emits an event on `/v1/events`.

## Escalation

Policy may escalate an assignment (comment on Jira, mark failed) after exhausting improvement
attempts. This is by design — a human then reviews the Jira issue.
