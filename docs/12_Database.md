# 12 — Database

The Engine's local state: **SQLite**, embedded, file-based, transactional, zero-ops. It is split into
**two independent databases** matching the Brain split ([04_ProjectBrain](04_ProjectBrain.md)):

- **`knowledge.db`** — the **Knowledge Brain** (persistent): index, dependency graph, ADR index,
  learned patterns, QA maps. Paired with the `knowledge/` markdown.
- **`state.db`** — the **Execution State** (temporary): assignments, progress, retries, metrics,
  locks, deferral tracking, events, Jira cache.

Neither DB is an authority on what work exists (Jira is, P2) or on code (Git is, P3); both are
**rebuildable** (NFR-7). Keeping them separate means wiping/rebuilding `state.db` can never damage the
Knowledge Brain.

## Why SQLite, why two files

- Embedded, single file, no server → local-first (P1) and simple (P6, NFR-4). ACID → safe locking and
  crash recovery (NFR-8). Lives on the SSD (C-6).
- **Two files** so persistent knowledge and volatile execution have independent lifetimes, backup
  policies, and blast radius ([04_ProjectBrain](04_ProjectBrain.md)).

## Location

```
<engine-home>/projects/<project>/knowledge.db   # Knowledge Brain (persistent; backed up)
<engine-home>/projects/<project>/state.db       # Execution State (temporary; rebuildable)
<engine-home>/engine.db                          # engine-global (projects registry, global settings)
```

`<engine-home>` defaults to `/Volumes/Extreme SSD/cwv2-home/` (C-6, configurable).

## Schema (logical)

SQL types illustrative; final DDL ships with the implementation. Times are UTC ISO-8601.

---

## `state.db` — Execution State

### `locks` — mutual exclusion
Full scope set, fencing, and semantics in [15_LockManager](15_LockManager.md). **V1 uses only the
hard scopes `issue`, `device`, `merge`**; the `advisory` column + finer scopes are reserved for the
documented future expansion and are unused in V1.

| col | type | notes |
|---|---|---|
| id | text pk | `issue:SCRUM-123`, `device:esp32-s3-a`, `merge:app` |
| scope | text | V1: `issue` \| `device` \| `merge` (future: `repo`/`module`/`folder`/`file`) |
| repo | text | repo the lock belongs to (null for device/issue-global) |
| owner | text | assignment id holding the lease |
| fence | int | monotonic token, bumped on every (re)grant/steal |
| acquired_at | text | |
| heartbeat_at | text | holder updates; stale ⇒ reclaimable |
| ttl_seconds | int | per-scope default from config |
| advisory | bool | always false in V1 (reserved) |
| meta | json | |

### `assignments` — one row per issue execution ([16_WorkerStateMachine](16_WorkerStateMachine.md))
| col | type | notes |
|---|---|---|
| id | text pk | assignment id |
| issue_key | text | Jira key |
| project | text | |
| state | text | Idle…Done / Blocked / Cancelled / Failed |
| attempt | int | Coding↔QA attempts (Decision Engine input) |
| branch | text | `agent/<key>-<slug>` |
| worktree | text | path |
| worker_type | text | current reasoning worker, if any |
| started_at / updated_at / ended_at | text | |
| outcome | text | closed / deferred-merge / needs-human / cancelled |

### `metrics` — per-assignment measures
| col | type | notes |
|---|---|---|
| assignment_id | text | fk |
| tokens | int | cumulative |
| cost_usd | real | cumulative |
| gate_pass_ratio | real | latest ([17_RepairLoop](17_RepairLoop.md), [20_DecisionEngine](20_DecisionEngine.md)) |
| durations | json | per-stage |

### `events` — append-only execution log (feeds dashboard SSE + audit)
| col | type | notes |
|---|---|---|
| id | int pk | |
| assignment_id | text | |
| ts | text | |
| kind | text | state-change / worker-start / tool-call / gate-result / decision / error |
| detail | json | |

### `deferrals` — deferral **tracking** (the reusable *how-to* lives in the Knowledge Brain)
| col | type | notes |
|---|---|---|
| id | int pk | |
| issue_key | text | |
| kind | text | hardware / device / visual / human / credentials / customer / design / owner |
| reason | text | |
| followup_key | text | linked Jira issue |
| created_at / resolved_at | text | |

### `issues_cache` — Jira reflection (never authoritative, P2)
| col | type | notes |
|---|---|---|
| key | text pk | |
| summary / description | text | |
| status / priority | text | |
| labels | json | |
| acceptance | json | parsed AC |
| automation | text | eligibility field: Enabled/Disabled/Manual Only/Needs Review ([22_Migration](22_Migration.md)) |
| links | json | linked issues |
| fetched_at | text | staleness marker |
| dirty_writes | json | outcomes pending write-back to Jira ([08_Jira](08_Jira.md)) |

---

## `knowledge.db` — Knowledge Brain

### Index & graph (derived, rebuildable)
- **`files`** (path, repo, lang, size, hash, summary, indexed_at)
- **`symbols`** (id, file_id, kind, name, signature, line)
- **`deps`** (from_file_id, to_file_id, kind) — deterministic import/reference edges

### Authored & learned (the irreplaceable part)
- **`decisions`** — index of `knowledge/decisions/NNNN-*.md` (id, title, context, decision,
  consequences, issue_key, created_at). Markdown is the human copy; row is the query index.
- **`patterns`** — learned failure patterns (id, signature, detail, resolution, first_seen, last_seen,
  count, flaky bool). Feeds the "current failures" prompt slice by matching signatures.
- **`qa_maps`** — screens/nav (id, screen, reach_path, golden_artifact, updated_at). Golden images are
  files on disk; row references them.

---

## `engine.db` — engine-global

- **`projects`** — registry (name, config_path, repos, engine_home_subdir, enabled, migrated_at).
- **`settings`** — global knobs (usage thresholds, default concurrency) overridable per project config.

## Transactions & locking semantics

- Lock acquire = a single transaction on `state.db`: insert-if-absent-or-stale; heartbeats update
  `heartbeat_at`; steal bumps `fence` ([15_LockManager](15_LockManager.md)).
- The **merge lock** is a single well-known row (`merge:<repo>`); the Integrator holds it for the
  duration of a merge, serializing advancement of `development` (FR-8, I-3).
- Reaper (scheduled, deterministic) deletes locks whose `heartbeat_at + ttl` < now and triggers
  cleanup of the associated worktree/assignment (NFR-8).

## Rebuildability (NFR-7)

- **`state.db` in full** → rebuildable from Jira (work) + Git (code) + `knowledge.db`. Never backed
  up; losing it loses only in-flight run history.
- **`knowledge.db` derived tables** (files/symbols/deps/qa_maps) → regenerated by deterministic
  indexers from the repo.
- **`knowledge.db` authored tables** (decisions/patterns) → backed by markdown (`knowledge/`) and, for
  deferrals, by Jira follow-up issues; periodically exported/committed so they survive DB loss.

## Migrations & integrity

- Each DB is schema-versioned; forward-only migrations run at startup.
- `PRAGMA foreign_keys=ON`, WAL mode for concurrent readers (dashboard) + one writer (engine).
- Nightly `VACUUM`/integrity check; `state.db` corruption → drop + rebuild; `knowledge.db` corruption
  → rebuild derived tables from repo + restore authored tables from `knowledge/` markdown.

## What the DBs never store

- Secrets (keychain/Azure KV only, NFR-6).
- The authoritative list of work (Jira) or code (Git).
- Anything that can't be rebuilt *and* isn't also persisted to Jira/Git/knowledge-markdown.
