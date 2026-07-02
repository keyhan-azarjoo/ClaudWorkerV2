# ClaudWorker V2 — Final Launch Gate

Last review before production. Reviewed with the question: *"If I had to operate this every day for
five years, what would I regret not fixing today?"* Only genuine defects were fixed. No features, no
redesign, no polish. Architecture frozen.

## Verification performed (all green)

| Area | How verified | Result |
|---|---|---|
| Reliability — crash/restart/lease/assignment recovery | `internal/stress` (120 issues + mid-run crash/restart), lease/assignment restart-from-disk tests | ✅ every issue terminal exactly once; deterministic |
| Backup / restore | roundtrip test + live smoke (wipe → restore recovered knowledge) | ✅ durable-only, zip-slip safe |
| Operations — endpoints/SSE/shutdown | live `serve` (sim): healthz, status, metrics, resources, SSE event, **SIGTERM graceful exit** | ✅ all respond; clean shutdown |
| Deployment | `plutil` (launchd valid), `docker build --check` (clean), `bash -n` all scripts | ✅ (systemd/Windows verify on target OS) |
| Security — secret leakage | static scan of all log/emit/publish paths for token values; migration output scan | ✅ **no secret values logged/emitted/migrated** |
| Runtime — rotation/failover/cooldown/pacing/usage-guard | runtime + resource + policy tests | ✅ rate-limit→cooldown→failover; budget pause |
| Git — orphan/branch/merge cleanup, rollback, restart | git-adapter tests (lifecycle, conflict-abort, cleanup-after-failure, restart-orphan) | ✅ conflicts auto-abort; idempotent cleanup |
| Jira — automation/transitions/labels/comments/attachments/queue | jira-adapter tests | ✅ all covered |
| Operations Console — every page/action/live-update | NAV↔MODULES↔files consistency (16/16), all module JS `node --check`, live not-wired → graceful 404 | ✅ no dead pages, graceful empty states, SSE-driven |
| Simulation Mode | full `serve` sim run drove SIM-1/2/3 → done autonomously | ✅ nothing regressed |

`go test -race ./...` — 27/27 PASS; `gofmt`/`go vet`/`deadcode` clean.

## Defects fixed

| Defect | Severity | Fix |
|---|---|---|
| `serve --bind` defaulted to `:8080` (all interfaces) — an **unauthenticated Control Plane exposed to the network** if no `dashboard.token` was set | Security (real) | Default changed to `127.0.0.1:8080` (loopback). Operators expose it explicitly (host + token). Deploy units already bind loopback. Behaviour-preserving for local clients; verified reachable + suite green. |

(The prior engineering-audit pass already removed all dead code and reinvented-stdlib; `deadcode`
reports zero unreachable.)

## Defects intentionally deferred (not defects for the shipping single-instance model)

| Item | Why deferred |
|---|---|
| Lease renewal during long assignments | Single-orchestrator: the in-memory resource **reservation** (held for the whole assignment) prevents double-use regardless of lease TTL; `claimNext` skips issues with a stored assignment. Renewal only matters for multi-instance (out of scope). Documented. |
| Startup-only resource discovery | The Resource Manager's health monitoring covers drift; a new device/account is picked up on restart. Periodic re-discovery is an enhancement, not a defect. |
| Orphan worktree for a *permanently* deferred assignment | Deferred assignments retry via `Recover` and clean on completion. A never-completing account leaves one worktree until it recovers — bounded, low-impact; a periodic sweep is an enhancement. |
| Serial discovery may label any USB-serial device as ESP32 | Devices are inert resources unless a verifier targets them; mislabel doesn't affect the loop. Operational note. |
| Per-repo build/verify commands | Now configurable via `serve --build-cmd/--api-url/--web-url`; the pilot repo's command is a launch config item, not a code defect. |

## Operational risks

- **Long-running Claude assignments** exceeding the ~15 min resource-lease TTL: safe on a single
  orchestrator (reservation guards); if you ever run multiple orchestrators against one engine home,
  add lease renewal first.
- **fsync-bound persistence** on slow disks throttles throughput; keep `engine_home` on an SSD.
- **Claude/Jira/network outages** pause or fail work; the loop recovers when connectivity returns — but
  monitor `/v1/metrics` and account health.
- **Disk growth** from knowledge/backups over years — schedule `cwv2 backup` rotation + logrotate.

## Launch risks

- **First live run is unproven end-to-end** against real Jira + real Claude + a real repo (validated
  with real Git + real-Claude-via-fake-CLI + real HTTP). Mitigation: `deploy/live-acceptance.sh` runs
  the full validation on one issue before trusting it broadly; pilot one small repo first.
- **Credentials/permissions**: a wrong Jira token or missing `origin` push permission fails fast at
  `validate`/first merge. Mitigation: `cwv2 validate` + `cwv2 doctor` pre-flight.
- **Verification command mismatch** for a non-Go repo: set `--build-cmd` correctly (checklist item).

## Rollback procedure

1. **Stop V2** (SIGTERM — graceful) or `systemctl stop cwv2`.
2. If durable state is bad: `cwv2 restore --config <cfg> --from <last backup.tgz>`.
3. **V1 is untouched and retained** (cutover only archives it read-only, never deletes). Re-enable V1
   (`chmod -R u+w <V1>`) and restart it if needed.
4. V2 and V1 stores are independent — no shared data, so rollback is clean and reversible.
5. Investigate via `/v1/events` timeline + `slog` logs; re-run `cwv2 stress` to reproduce
   deterministically.

## Go / No-Go recommendation

**GO for first live deployment** (Release Candidate, 8.5/10) — with the standard pilot discipline:
validate → live-acceptance on one issue → observe → widen. No architectural blockers; the one genuine
defect found (network-exposed default bind) is fixed. All remaining blockers are external
(credentials, optional hardware) and are enumerated in `LIVE_CONFIG_CHECKLIST.md` /
`LAUNCH_REPORT.md`.

**ClaudWorker V2 is ready for live deployment.**

---

## Stop

Launch gate complete. Stopping. Awaiting the owner's credentials and launch approval; the next work is
operating the platform, not redesigning it.
