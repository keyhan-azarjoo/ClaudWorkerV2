# Recovery Guide

ClaudWorker V2 recovers deterministically. Durable state is minimal and format-versioned; transient
state is disposable.

## What is durable vs transient

- **Durable** (recovered): Knowledge Brain (`knowledge/`), Assignment state (`assignments/`), Leases
  (`leases/`). Atomic writes; `spec_version` migration.
- **Transient** (rebuilt/discarded): worktrees, artifacts, repos, in-flight processes, retry counters,
  caches, account cooldowns. V2 recovers durable state only.

## Restart / crash recovery

On startup `Orchestrator.Recover`:
1. **Reaps expired leases** (a crashed owner's lease auto-expires → reclaimable, no human step).
2. **Skips terminal assignments** — completed work is never restarted (Law 19).
3. **Resumes unfinished assignments** — re-hydrated from Jira; the git worktree is idempotently
   re-ensured; the loop continues from a safe point. No work is lost or redone.

Proven by `internal/stress` (crash + restart mid-run → every issue terminal exactly once) and the
lease/assignment restart-from-disk tests.

## Lease recovery

Leases are durable and time-bounded. After a restart, active leases are still owned; expired ones are
reclaimable. `cwv2` reaps expired leases automatically (`leases.reap` command available too). No manual
cleanup is ever required.

## Backup / restore

```sh
cwv2 backup  --config <cfg> --to backup.tgz     # durable state only (knowledge/assignments/leases)
cwv2 restore --config <cfg> --from backup.tgz   # restore into the engine home
```
Backups are deterministic tar.gz; transient state is excluded; restore is zip-slip safe.

## Machine reboot

The service manager (systemd `Restart=on-failure`, launchd `KeepAlive`, Windows `sc failure`)
restarts `cwv2`; `Recover` then resumes. Point `engine_home` at a persistent volume so durable state
survives.

## Network interruption

Jira/Git/Claude calls are bounded by timeouts and retried per policy: infrastructure failures retry
(runtime), other classes return to the Policy Engine. A failed step leaves the assignment recoverable;
the workspace stays clean (conflicts auto-abort). The loop resumes when connectivity returns.

## Disaster recovery checklist

1. Provision a host; install `cwv2` (`deploy/install.sh`).
2. `cwv2 restore --from <latest backup>`.
3. `cwv2 validate --config <cfg>`.
4. `cwv2 serve --mode live` — `Recover` resumes unfinished work automatically.
