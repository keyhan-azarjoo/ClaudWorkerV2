# Security Guide

## Secrets

- Config holds secret **names**, never values. The resolver chain is Keychain → Azure Key Vault → env.
- The Jira token, per-account Claude config dirs, and push credentials live outside config.
- Migration emits **references only** — a test scans output for token values; a real run leaked none.
- The Jira client never logs its token; the Worker Runtime never puts credentials in prompts.

## Prompt isolation

Prompts contain **only** Assignment, Knowledge Context, Relevant Files, Acceptance Criteria. Execution
state, Git state, Policy state, Lease state, account, and runtime name **never** enter the prompt
(enforced by `BuildPrompt` + the `DevInput` contract).

## Control Plane auth

Bearer-token authentication (`TokenAuth`, constant-time compare) on every route except `/v1/healthz`.
Set `dashboard.token` in production (empty = open, dev only). The `Authenticator` interface allows
JWT/mTLS without touching the server.

## Worker isolation

Workers are disposable `claude -p` processes: fresh process per call, no session, no resume, no hidden
state. Each runs under a selected account's `CLAUDE_CONFIG_DIR` and inside an isolated git worktree —
never the main working tree.

## Filesystem safety

- Git: per-assignment disposable worktrees; conflicts auto-abort (clean tree); the human/main tree is
  never modified.
- Backup restore is **zip-slip safe** (rejects paths escaping the destination).
- Atomic writes (temp + fsync + rename) for all durable state.

## Determinism as a safety property

Everything but the worker is deterministic. Format-version mismatches on durable state are **refused,
never guessed** (Law 19). Simulation Mode runs the full loop with no external trust surface.

## Resource/account safety

- Policy → Resource → Lease ordering is never bypassed. Rate-limited/authentication-failed accounts are
  cooled down (health signal) so work fails over — the runtime never chooses the account.
- Leases are time-bounded; a crashed owner's lease auto-expires and is reclaimed with no human step.

## Known considerations

- The `git`/`adb`/`ssh` shell-outs use fixed argv (no shell interpolation of untrusted input).
- Live device drivers run as the host user; run on a dedicated runner, not a developer's primary machine.
- Push to `origin` requires host git credentials; scope them to the target repos.
