# 21 — Implementation Roadmap

The construction manual. It defines the **order** subsystems are built, their **dependencies**,
**validation** and **acceptance** criteria, **rollback** strategy, **milestones**, and **phases**.

> **Nothing may be implemented out of order.** Each subsystem must be fully working and pass its
> acceptance criteria before the next begins (the owner rule: "implement one subsystem at a time;
> every subsystem fully working before moving on"). This document is only actionable **after the
> architecture is frozen** ([README](../README.md), freeze recommendation in
> [ARCHITECTURE_REVIEW](ARCHITECTURE_REVIEW.md)).

## Principles for building

- **Deterministic core first, AI last.** Build and validate all zero-token machinery before wiring in
  any `claude -p` worker. Most of the system must be demonstrable with **no tokens spent**.
- **Vertical slice early.** As soon as the minimum spine exists, prove one real Jira issue end-to-end;
  then broaden.
- **Every subsystem ships with tests + a CLI entry** (`cwv2 tool …` / `cwv2 <cmd>`) so it's
  independently exercisable ([11_Plugins](11_Plugins.md), [18_PlugInContract](18_PlugInContract.md)).
- **Rollback = revert the subsystem's merge; the prior subsystem still works.** Because subsystems are
  layered and each is independently valid, reverting the top layer never breaks the ones beneath.

## Dependency graph (build bottom-up)

```
S0 Foundations (repo, config loader, doctor, engine home, logging)
      │
S1 Deterministic Toolbelt core (git.*, jira.* read) + `cwv2 tool` CLI
      │
S2 Databases (state.db + knowledge.db) + migrations
      │
S3 Knowledge Brain (indexers, dep-graph, prompt-assembly) ──┐
      │                                                      │
S4 Lock Manager V1 (issue/device/merge, fencing, reaper)    │
      │                                                      │
S5 Decision Engine (pure rules)                             │
      │                                                      │
S6 Worker Runner (spawn claude -p, schema-validate)  ◀──────┘ (needs S3 prompts)
      │
S7 Orchestrator + Assignment state machine (software path) ── first end-to-end issue
      │
S8 Repair Loop wiring + build/QA gates (generic + flutter/dotnet/web plugins)
      │
S9 Migration phase (repo+jira analysis, brain init, eligibility, report)
      │
S10 Dashboard (read model + controls)
      │
S11 Usage guard + launchd + notifications + Mac-Mini dispatch
      │
S12 Hardware plugins (esp32/pcb/cad) with deferral
      │
S13 Second project onboarding (portability proof) + Future lock expansion (only if justified)
```

Each Sn depends only on S0..S(n-1) (plus the noted branch). No forward dependencies.

## Subsystem order, validation & acceptance

For each subsystem: **build** (what), **validation** (deterministic checks it must pass), **acceptance**
(the demonstrable outcome that unlocks the next).

### S0 — Foundations
- **Build:** Go module skeleton, config loader ([13_Config](13_Config.md)), `cwv2 doctor`, engine-home
  layout on the SSD, structured logging.
- **Validation:** `doctor` validates a config, resolves secrets by name, reports missing toolchains.
- **Acceptance:** `cwv2 doctor --project myotgo` runs green against a real config; **zero tokens**.

### S1 — Toolbelt core (git + jira read)
- **Build:** `git.*` (fetch/branch/worktree/commit/merge/push) and `jira.*` read (query/get/transitions)
  as deterministic tools; `cwv2 tool <name>` runner.
- **Validation:** unit tests + live read against the real Jira board and a scratch repo; identity
  (author `keyhanazarjoo`) enforced.
- **Acceptance:** create a worktree/branch, commit, and read the SCRUM backlog **from the CLI**, no
  engine, no tokens.

### S2 — Databases
- **Build:** `state.db` + `knowledge.db` schemas ([12_Database](12_Database.md)), forward-only
  migrations, WAL.
- **Validation:** migration up/down tests; concurrent read (dashboard) + single write; integrity check.
- **Acceptance:** both DBs create, migrate, and survive a simulated crash (fsync + reopen).

### S3 — Knowledge Brain
- **Build:** file/symbol indexer, dependency graph, ADR/pattern/qa-map stores, **prompt-assembly**
  (the six slices, P9) — [04_ProjectBrain](04_ProjectBrain.md).
- **Validation:** index a real repo; relevant-files selection returns a small, correct set; prompt
  assembly is deterministic and size-bounded.
- **Acceptance:** given an issue key, the engine emits a small, correct prompt **using zero tokens** to
  assemble it.

### S4 — Lock Manager V1
- **Build:** issue/device/merge locks, fencing, heartbeat, reaper, reconciler
  ([15_LockManager](15_LockManager.md)).
- **Validation:** property tests for L-1..L-6; concurrent-acquire race test; crash→steal→fence-reject
  test; reboot reconcile test.
- **Acceptance:** two concurrent acquirers never both win; a stale lock is reclaimed and the zombie
  write is rejected.

### S5 — Decision Engine
- **Build:** pure `Decide(ctx)` with the 7 decisions + precedence + stuck detector
  ([20_DecisionEngine](20_DecisionEngine.md)).
- **Validation:** table-driven tests covering every rule + precedence; D-2 purity/totality property
  test.
- **Acceptance:** every DecisionContext yields exactly one logged decision; **no tokens**.

### S6 — Worker Runner
- **Build:** spawn `claude -p` with per-stage allowed-tools, budgets, JSON schema validation, teardown
  ([05_Workers](05_Workers.md)).
- **Validation:** one worker type (Manager) end-to-end on a real issue; invalid-JSON reprompt path;
  budget/timeout kill path.
- **Acceptance:** a Manager worker returns a schema-valid plan for a real issue; disposability verified
  (kill mid-run → nothing lost).

### S7 — Orchestrator + Assignment state machine (software path)
- **Build:** the full Assignment lifecycle ([16_WorkerStateMachine](16_WorkerStateMachine.md)) wiring
  S3–S6; Manager→Developer→Building→QA→Integrator→Done for a **generic** software repo.
- **Validation:** drive a trivial real issue to a `--no-ff` merge on `development`; crash/resume at
  each state.
- **Acceptance (Milestone M1 — "first light"):** one real MyOTGO issue goes Ready → merged → Done,
  branch deleted, Jira updated, with only Planning/Coding/QA spending tokens.

### S8 — Repair Loop + gates + first plugins
- **Build:** Observe/Verify gates, plugin `Build/Verify/Repair` for `generic`, `flutter`, `dotnet`,
  `web` ([17_RepairLoop](17_RepairLoop.md), [18_PlugInContract](18_PlugInContract.md)); human-like QA
  rung for app/web.
- **Validation:** induced-failure → repair → pass; deferral path when no device; visual diff vs golden.
- **Acceptance (M2):** a Flutter and a .NET issue each complete with the repair loop, including one
  deferred check that does not block merge.

### S9 — Migration phase
- **Build:** the migration pipeline ([22_Migration](22_Migration.md)): repo analysis, Jira analysis,
  eligibility recommendations, task-normalization proposals, deferred-work classification, Knowledge
  Brain initialization, build validation, dashboard init, human-review report.
- **Validation:** run against MyOTGO read-only; report is accurate; **no issue auto-deleted**; nothing
  applied without approval.
- **Acceptance (M3):** migrating a fresh project produces an approved profile + initialized Knowledge
  Brain before any Assignment starts.

### S10 — Dashboard
- **Build:** localhost read model + SSE + controls ([09_Dashboard](09_Dashboard.md)).
- **Validation:** live issue view, deferrals, needs-human, devices; run/pause/resume/stop act through
  the Orchestrator safely.
- **Acceptance:** operator can observe and steer a live run; controls preserve all invariants.

### S11 — Ops (usage guard, launchd, notifications, Mac-Mini)
- **Build:** usage guard (pause/resume, per-account policy), launchd agent, Telegram notifications,
  ssh-dispatched Mac-Mini iOS builds ([14_Deployment](14_Deployment.md)).
- **Validation:** guard pauses at threshold and resumes; launchd keeps the daemon up; iOS build defers
  gracefully if Mac-Mini absent.
- **Acceptance:** unattended overnight operation within usage policy, no forced spend.

### S12 — Hardware plugins
- **Build:** `esp32-firmware`, `pcb-kicad`, `cad-3d`, `hardware-pipeline` with their gates + deferral
  ([10_Hardware](10_Hardware.md)).
- **Validation:** Wokwi/Renode/DRC/FCL gates run; physical checks defer with attestation follow-ups;
  device lock honored.
- **Acceptance:** a firmware and a PCB/3D issue complete with simulation gates green and physical tests
  honestly deferred.

### S13 — Portability + Future
- **Build:** onboard a **second, non-MyOTGO** project by config + plugins only; evaluate whether Future
  advisory locks are warranted by measured conflict rate ([15_LockManager](15_LockManager.md)).
- **Validation:** no engine-core change needed for the new project (Law 16).
- **Acceptance (M4 — "portable"):** project #2 runs the full loop from its own `cwv2.yaml`; advisory
  locks added **only** if metrics justify.

## Milestones

| ID | Name | Unlocks |
|---|---|---|
| **M0** | Deterministic core (S0–S5) | everything runs with **zero tokens** |
| **M1** | First light (S7) | one real issue Ready→Done autonomously |
| **M2** | Repair + app/web/.NET (S8) | routine software issues close, with deferral |
| **M3** | Migration (S9) | new projects onboarded safely, owner-approved |
| **M4** | Ops + hardware + portability (S11–S13) | unattended, multi-domain, multi-project |
| **M5** | V2 replaces V1 | token/issue beats V1; common case autonomous |

## Rollback strategy

- **Per-subsystem:** each Sn is merged as its own set of tiny branches; reverting Sn's merges restores
  S(n-1)'s working state (subsystems are layered and independently valid).
- **Runtime safety net:** the Execution State is disposable — a bad build can be stopped, `state.db`
  reset, and work resumed from Jira + Git + Knowledge Brain (NFR-7/8). The Knowledge Brain is backed
  up separately, so a rollback never risks decisions/standards.
- **V1 untouched:** V2 runs entirely from its own repo + engine home on the SSD; at no point does
  bringing up V2 modify V1 (C-1). V1 remains production until **M5**.
- **Go/no-go gates:** a milestone is only declared when its acceptance criteria pass; a failed
  acceptance blocks the next subsystem (no out-of-order build).

## What must never happen during construction

- No subsystem built before its dependencies pass acceptance.
- No `claude -p` worker wired in before the deterministic core (S0–S5) is green.
- No V1 file modified.
- No architecture change without updating the frozen spec + this roadmap together.
