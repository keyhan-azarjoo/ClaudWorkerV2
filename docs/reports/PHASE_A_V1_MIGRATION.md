# Phase A — Full V1 Migration

Imports ClaudWorker **V1** persistent configuration into **V2** artifacts. V1 is treated as the source
of truth; nothing is manually recreated. Architecture unchanged (a new read-only tool + one config
fragment) — this is migration, not redesign.

## The migration tool

`cwv2 migrate --from <V1 dir> --to <out> [--dry-run]` (package `internal/migration`).

Guarantees (all tested):
- **Read-only against V1** — V1 files are never written (verified: V1 clone unchanged after a real run).
- **Idempotent + deterministic** — same input → byte-identical output; safe to re-run.
- **Restart-safe** — atomic writes (temp + rename) to the target dir.
- **Reversible** — delete the target dir to revert; V1 is untouched.
- **No secret values ever** — only *references* (config dirs, `secretsPath`). A hard test scans all
  output for token values; a real run was scanned against 3 real V1 tokens → **none leaked**.
- **Nothing silently ignored** — every category appears in the matrix (15 rows).

Artifacts written: `resources.json` (V2 resource definitions), `migrated.yaml` (config fragment to
merge into `cwv2.yaml`), `migration-matrix.{json,md}`.

## What was imported (real V1 run)

- **4 AI accounts** → resources (2 `claude_account`, 2 `codex_account`) with `engine` + `config_dir`
  *reference* + pace/pausePct/schedule labels. Tokens/usage-counters NOT migrated (secret/transient).
- **10 devices** → resources (Android, iPhone, ESP32 ×3, Windows build machine, 2 Pi hubs, iOS sim,
  Android emulator). The **modem** testbed skipped (not a compute/runtime resource).
- **Usage guard** 98/95 → `usage_guard`; **max_concurrent** 10 → `workflow`; **model**, timeouts,
  scheduler minutes, min-free-GB → `defaults`.
- **Jira gate labels** (2 required, 10 blocking) → `gate_labels`.

## Migration matrix (from the real V1)

| Category | Found | Imported | Skipped | Missing | Validation | Notes |
|---|---|---|---|---|---|---|
| AI Providers / Accounts | 4 | 4 (2 claude, 2 codex) | usage counters + tokens (transient/secret) | — | account → resource w/ engine + config-dir ref | config dirs are references; pace/pausePct/schedule preserved as labels |
| Resources / Devices | 11 | 10 | 1 modem + inUse/mode (reservation) | Mac Mini + DGX (no V1 entry) | type → V2 Kind; reach as label | reservation state transient → not migrated |
| Account Mgmt (usage/pacing/scheduling/concurrency) | usage guard, maxConcurrent, scheduler, timeouts, model | usage_guard(98/95), max_concurrent(10), model, timeouts | in-flight cap / retries (transient) | — | usage guard → BudgetPolicy; concurrency → Workflow | rotation/failover/health are real V2 Resource-Manager behaviours |
| Jira | gate labels | 2 required, 10 blocking | — | base_url/project/Automation (set in cwv2.yaml) | labels → gate config | V2 Jira configured directly (Phase 2.1) |
| Git | n/a (per-agent worktrees) | 0 | — | repo declarations (cwv2.yaml) | n/a | V2 Git real (Phase 2.2) |
| Policies | usage guard/timeouts/concurrency | budget + concurrency | — | explicit retry count (V2 default) | → Policy Engine | S6 |
| Verification | job-based (visual/e2e/qa) | gate labels | — | declarative driver config | n/a | drivers are Phase B #2 |
| Discovery | 11 devices + 4 accounts | resource definitions | live reachability/health | — | → resources.json | probing is Phase B #1 |
| Operations Console | 0 users | 0 (reference) | user records | V2 uses token auth | n/a | prefs are client-local |
| Secrets | secretsPath + config dirs + tokens | references only | ALL values | — | output scanned — no values | reuse Keychain/secure files/env |
| SSH | device reach (ssh/adb/ip) | reach references | — | explicit key refs (none in V1) | n/a | reuse host SSH keys/known_hosts |
| Notifications | voice config (8 keys) | 0 | — | no notification subsystem in V2 | n/a | V1 voice/Telegram separate; deferred |
| Logging / metrics | logDir + Sentry | 0 | — | — | n/a | V2 uses slog + Control Plane metrics |
| Jobs / role-agents | **18 role-agents** | 0 | **all — retired** | — | n/a | **JUSTIFIED RETIREMENT** (below) |
| Transient runtime state | usage counters (6), sessions, in-flight, worktrees, retries, logs | 0 | **ALL** | — | excluded by construction | V2 starts clean |

## Intentional retirements (with justification)

- **Role-agent "jobs" (18)** — retired. V2 is **Jira-issue-driven**: one Assignment per issue, worker
  coverage emergent from the queue, not per-role cron agents. This is the core V1→V2 architectural
  improvement, not a loss.
- **Notifications (voice/Telegram)** — deferred. V2 has no notification subsystem; V1's voice/Telegram
  are separate tools. Recorded as a reference; a notifications adapter is out of scope for the frozen
  architecture (would need an ACP).
- **Console user DB** — retired in favour of token auth + client-local preferences.
- **All transient runtime state** — never migrated by design (V2 recovers durable state only).

## Gaps flagged (for Phase B / config)

- **Mac Mini + DGX** are absent from V1's device list → add when defining resources for real discovery
  (Phase B #1).
- **Jira base_url / project / Automation field** — not in V1 config; set directly in `cwv2.yaml`
  (already the Phase 2.1 path).
- **Repo declarations** — set in `cwv2.yaml repos[]` (V1 had none).

## Do-not-migrate confirmed excluded

Running assignments, active leases, open worktrees, runtime processes, current retries, logs,
temporary files, cached execution state, and per-account usage counters — all **skipped by
construction** (never read into output). V2 starts clean.

## Validation

`gofmt`/`go vet` clean; `go test -race ./...` — **23/23 packages PASS**. Migration-specific tests:
account/device mapping, modem-skip, usage-guard/config mapping, **secret-non-leak**, retired-jobs +
transient-skip documentation, matrix completeness, **idempotency/determinism** (byte-identical
re-runs), and tolerance of a missing V1.

## Next

Phase A complete. **Next: Phase B — remaining real integrations, one at a time** (#1 Resource
Discovery, then #2 Verification Drivers), each validated, Simulation Mode preserved. Stopping for
review.
