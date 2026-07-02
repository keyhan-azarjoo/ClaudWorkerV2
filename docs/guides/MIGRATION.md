# Migration Guide (V1 → V2)

`cwv2 migrate` imports ClaudWorker V1's persistent config into V2 artifacts. See
`docs/reports/PHASE_A_V1_MIGRATION.md` for the full matrix.

## Run

```sh
cwv2 migrate --from /path/to/Claud_worker_agent --to ./migrated --dry-run   # prints the matrix
cwv2 migrate --from /path/to/Claud_worker_agent --to ./migrated             # writes artifacts
```

Guarantees: **read-only** against V1, **idempotent** (byte-identical re-runs), **restart-safe** (atomic
writes), **reversible** (delete the output dir), **no secret values** (references only).

## Artifacts produced

- `resources.json` — V2 resource definitions (Claude/Codex accounts + devices).
- `migrated.yaml` — a config fragment (usage guard, max_concurrent, model, timeouts, gate labels) to
  merge into `cwv2.yaml`.
- `migration-matrix.{md,json}` — Found/Imported/Skipped/Missing/Validation/Notes per category.

## What migrates

Accounts (config dirs as references), devices (Android/iPhone/ESP32/build machines), usage guard,
concurrency, model, scheduler/timeouts, Jira gate labels.

## What does NOT migrate (by design)

- **Secret values** — tokens/passwords are never read; only config-dir references are carried.
- **Transient runtime state** — usage counters, sessions, in-flight assignments, worktrees, retries,
  logs, caches. V2 starts clean.
- **Role-agent "jobs"** — intentionally **retired**: V2 is Jira-issue-driven, not per-role cron agents.
- **Voice/Telegram notifications** — deferred (no V2 notification subsystem).
- **Console user DB** — replaced by token auth + client-local preferences.

## After migrating

1. Merge `migrated.yaml` into your `cwv2.yaml`; add `jira.base_url/project/auth` and `repos[]`
   (V1 had none).
2. Add Mac Mini / DGX resource declarations (absent from V1's device list).
3. `cwv2 validate --config cwv2.yaml`.
4. `cwv2 serve --mode simulation` to smoke-test, then `--mode live`.

## Validation

The migration is validated by `internal/migration` tests: mapping, secret non-leak (scans output for
token values), idempotency/determinism, transient-skip, retired-jobs documentation, matrix
completeness, and tolerance of a missing V1. A real V1 run imported 4 accounts + 10 devices with zero
token leaks.
