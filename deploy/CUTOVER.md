# Production Cutover (V1 → V2)

Run `deploy/cutover.sh <V1 dir> <V2 cwv2.yaml>` (dry run) then `... --go` (live).

| Step | Action | Safety |
|---|---|---|
| 1 | **Backup V1** — `tar` the whole V1 dir | read-only copy; V1 untouched |
| 2 | **Verify V2** — `cwv2 validate` | fails fast on bad config |
| 3 | **Import V1 config** — `cwv2 migrate` (read-only) | no secrets, idempotent; review before merging |
| 4 | **Start V2** — `cwv2 serve --mode live` | graceful, recoverable |
| 5 | **Observe** — Operations Console + `/v1/status`, `/v1/metrics`, `/v1/events` | watch autonomous processing |
| 6 | **Archive V1 read-only** — `chmod -R a-w` (only if healthy) | **V1 is never deleted** |

Rollback: stop V2, restore `v1-backup.tgz` if needed, re-enable V1 (`chmod -R u+w`). V2 and V1 stores
are independent; no data is shared, so cutover is reversible.
