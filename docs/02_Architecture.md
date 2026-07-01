# 02 — Architecture

The authoritative component map. Everything else in `docs/` refines a box drawn here.

## System context

```
        ┌──────────┐        ┌──────────┐         ┌────────────────┐
        │   Jira   │        │  GitHub  │         │  Anthropic     │
        │ (work    │        │ (code    │         │  `claude` CLI  │
        │  truth)  │        │  truth)  │         │  (reasoning)   │
        └────▲─────┘        └────▲─────┘         └───────▲────────┘
             │ REST                │ git/REST             │ exec `claude -p`
             │                     │                      │
   ┌─────────┴─────────────────────┴──────────────────────┴──────────────┐
   │                    ClaudWorker V2 Engine  (single Go binary, laptop) │
   │                                                                      │
   │   Scheduler ─▶ Orchestrator ─▶ Worker Runner ─▶ (ephemeral workers)  │
   │       │            │   ▲             │                               │
   │       ▼            ▼   │             ▼                               │
   │   Usage Guard   Lock/Ownership   Deterministic Toolbelt (Go)         │
   │                     │                   │                            │
   │                     ▼                   ▼                            │
   │                 Project Brain (SQLite + knowledge files)             │
   │                     │                                                │
   │                     ▼                                                │
   │                 Dashboard (localhost web + SSE)                      │
   └──────────────────────────────────────────────────────────────────────┘
                         │
                         ▼  on-demand only
                 ┌───────────────┐
                 │  Mac Mini     │  (iOS/macOS builds, TestFlight)
                 └───────────────┘
```

## Components

### 1. Scheduler
- Deterministic Go. Polls Jira (FR-1) on an interval, applies the usage guard, and decides which
  issues are eligible to start based on priority, ownership, and concurrency budget.
- Also runs periodic maintenance jobs: `git fetch`, Brain re-index, lock reaping, deferral sweep.
- No AI. No tokens.

### 2. Orchestrator (the state machine)
- Deterministic Go. Owns the **issue lifecycle** in [03_Workflow](03_Workflow.md) (execution view:
  the worker-slot state machine in [16_WorkerStateMachine](16_WorkerStateMachine.md)). For each active
  issue it decides the next stage and either (a) runs a deterministic step itself or (b) asks the
  Worker Runner to spawn the right worker with an assembled prompt.
- Enforces the bounded Developer↔QA retry loop (the universal [17_RepairLoop](17_RepairLoop.md)),
  deferral rules, and the merge gate.

### 3. Lock / Ownership manager
- Deterministic Go. Grants exclusive ownership of an issue and of a working tree/branch. Guarantees
  P8 (no conflicts). Locks are persisted (DB) with TTL + heartbeat so a crashed worker's lock is
  reaped (NFR-8). Full design in [15_LockManager](15_LockManager.md); see also [07_Git](07_Git.md)
  and [12_Database](12_Database.md).

### 4. Worker Runner
- Deterministic Go. Spawns `claude -p` with: `--output-format json`, `--permission-mode acceptEdits`,
  a **per-stage allowed-tools** set, `--strict-mcp-config`, and an appended system prompt (the
  charter/role). Streams progress to the dashboard, enforces a wall-clock + token budget, parses the
  JSON result, validates it against the stage schema, and tears the worker down. Workers are
  **stateless and disposable** (P4).

### 5. Deterministic Toolbelt
- Deterministic Go. The library of zero-token operations (FR-13). Exposed three ways:
  1. Internally to the Orchestrator (direct Go calls).
  2. To workers as **callable tools** (the only way a worker performs an action).
  3. On the CLI (`cwv2 tool <name> …`) for testing and manual use.
- Organized by capability and by plugin (see [11_Plugins](11_Plugins.md)). Examples: `git.*`,
  `jira.*`, `build.*`, `flutter.*`, `ios.*`, `android.*`, `screenshot.*`, `imgdiff.*`, `ocr.*`,
  `kicad.*`, `cad.*`, `index.*`, `log.*`.

### 6. Project Brain
- Deterministic Go over SQLite + markdown knowledge files (see [04_ProjectBrain](04_ProjectBrain.md)
  and [12_Database](12_Database.md)). The durable knowledge authority (P4). Provides the **prompt
  context slices** (P9) and stores decisions, indexes, failures, deferrals.

### 7. Dashboard
- Deterministic Go web server on localhost (see [09_Dashboard](09_Dashboard.md)). Read model over
  the DB + live SSE from the Orchestrator. Controls: run/pause/resume/stop. No framework required.

### 8. Usage Guard
- Deterministic Go. Reads plan usage (keychain credential → usage endpoint), pauses worker spawning
  at the configured threshold, resumes below it (NFR-2). Fails **open** on read error (never blocks
  forever) but never forces override/pay-as-you-go (NFR-3).

## The strict determinism boundary

This is the heart of the design (P5). Every capability is classified once, at design time:

| Class | Runs as | Tokens | Examples |
|---|---|---|---|
| **Deterministic** | Go | 0 | git, Jira REST, build, screenshot, image diff, OCR, DRC/ERC, STL export, log parse, indexing, scheduling, merge, rebase |
| **Reasoning** | `claude -p` worker | yes | plan an issue, write/modify code, decide if a screen meets AC, diagnose a failure, judge a merge |

Rule: **if it can be expressed as a program, it is Go.** A worker never runs a shell command that
the toolbelt could run; it calls the tool. The toolbelt is the worker's hands; the model is only its
judgment. Adding a new deterministic capability = a new Go tool, never a new prompt.

## Runtime processes

- **One long-lived process**: `cwv2 serve` (the daemon: scheduler + orchestrator + dashboard).
- **Many short-lived processes**: `claude -p` workers, and per-tool subprocesses (git, flutter,
  kicad, …). All are children of the daemon, all reaped on exit.
- **No other services required** (NFR-1). Optional later: a tunnel for remote dashboard, a Mac Mini
  runner for iOS.

## Concurrency model

- The Scheduler admits up to `maxConcurrent` issues (config), gated by the usage guard.
- Each admitted issue is processed by **one active worker at a time** (Manager, then Developer, then
  QA, …) — stages are sequential *within* an issue, parallel *across* issues.
- Every in-flight issue owns its **own git worktree** (never a shared checkout), so parallel issues
  cannot corrupt each other's tree (NFR-5).
- Merges are **serialized** by a single Integrator lock so `development` only ever advances by one
  verified `--no-ff` merge at a time (FR-8).

## Technology choices (and why)

- **Go** for the entire engine: single static binary, trivial local deploy (NFR-4), great at
  process orchestration and concurrency, matches V1's proven `exec the claude binary + parse JSON`
  pattern. No official Go SDK is needed or used.
- **`claude -p` CLI** for reasoning: keeps everything local (P1, C-5), avoids cloud routines that
  store prompts/results server-side, reuses the flat subscription (NFR-3).
- **SQLite** for engine state: embedded, file-based, zero-ops, transactional (good for locks). Lives
  on the SSD (C-6). See [12_Database](12_Database.md).
- **Markdown knowledge files** alongside SQLite for human-readable, git-diffable decisions/ADRs.
- **Plain HTML + SSE** for the dashboard: no build step, no JS framework, dependency-free (NFR-4).
- **Plugins as manifests + Go tool sets** so project types are data + isolated code, not core edits
  (P10).

## Where state lives

| State | Store | Authority |
|---|---|---|
| What work exists | Jira | **Jira** (P2) |
| Code | GitHub / local clones | **Git** (P3) |
| Knowledge (decisions, indexes, failures) | Brain (SQLite + md) | **Brain** (P4) |
| Coordination (locks, run status, cache) | SQLite | Engine (ephemeral, rebuildable) |
| Config | `cwv2.yaml` per project | Owner |
| Secrets | keychain / Azure KV | Vault (never in repo) |

All of the engine-owned stores physically live under the engine home on the **external SSD** (C-6):
`/Volumes/Extreme SSD/cwv2-home/` (configurable), with per-project subdirectories.

## Failure & recovery (summary)

- Worker timeout/crash → lock reaped, worktree cleaned, issue returned to its prior stable stage,
  attempt counter incremented; after N attempts → `needs-human`.
- Merge conflict → refresh from `development`, re-verify; if still conflicting after K tries →
  `needs-human` (never force).
- Engine crash → on restart, reconcile: reap expired locks, detect orphan branches/worktrees,
  resume from DB run state, re-fetch Jira truth.

Full state machine, gates, and error transitions are in [03_Workflow](03_Workflow.md).
