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
  independently exercisable ([11_Plugins](11_Plugins.md), [18_PluginContract](18_PluginContract.md)).
- **Rollback = revert the subsystem's merge; the prior subsystem still works.** Because subsystems are
  layered and each is independently valid, reverting the top layer never breaks the ones beneath.

## Dependency graph (walking-skeleton first — amended by ACP-0001)

> **Amended by [ACP-0001](acp/ACP-0001-assignment-engine-before-database.md) → spec v2.1.0.** The
> Assignment Engine is built **before** the full database: persistence is an implementation detail
> that must *emerge* from what execution actually needs, not be designed up front (Law 17). The
> engine ships as a **walking skeleton** using **ports + minimal adapters**; later subsystems extend
> those same interfaces to full spec (they are not rebuilt).

```
S0 Foundations (repo, config loader, doctor, engine home, logging)                 ✅ done
      │
S1 Deterministic Toolbelt (git.*, jira.*) + CLI                                     ✅ done
      │
S2 ASSIGNMENT ENGINE (walking skeleton) ── deterministic execution core            ◀ NEXT
      │   ports: Locks (issue-lock only) · Decider (happy-path subset) ·
      │          Workers (claude -p runner) · Store (minimum persistence) · git/jira
      │   drives ONE real issue: claim → lifecycle → retries/progress →
      │          resume-after-restart → hand off to QA/Merge (via ports)  ── M1 "first light"
      │
S3 Persistence (emergent state.db) ── formalize the Store from S2's real needs
      │   (was old S2 "Databases"; 12_Database is the target, realized incrementally)
      │
S4 Knowledge Brain (indexers, dep-graph, small-prompt assembly) ── extends Workers input
      │
S5 Lock Manager full (add device + merge scopes, fencing, reaper) ── extends Locks
      │
S6 Decision Engine full (all 7 decisions + precedence + stuck detector) ── extends Decider
      │
S7 Worker Runner full (schema-validate, budgets, least-privilege tools) ── extends Workers
      │
S8 Repair Loop + build/QA gates (generic + flutter/dotnet/web plugins)
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

Each Sn depends only on S0..S(n-1). S2 depends on **interfaces**, so it can be built and fully tested
(with fakes) before S3–S7 provide the production adapters. No forward dependencies.

### Old → new numbering (ACP-0001)

| Old | New | Note |
|---|---|---|
| S2 Databases | **S3** Persistence | now emergent, driven by S2 |
| S3 Knowledge Brain | S4 | unchanged capability |
| S4 Lock Manager V1 | S5 (+ minimal slice pulled into S2) | issue-lock in S2; device/merge later |
| S5 Decision Engine | S6 (+ minimal slice pulled into S2) | happy-path subset in S2; full rules later |
| S6 Worker Runner | S7 (+ minimal slice pulled into S2) | `claude -p` runner in S2; validation/budgets later |
| S7 Orchestrator + Assignment state machine | **S2** Assignment Engine | moved earlier; the walking skeleton |
| S8–S13 | unchanged | |

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

### S2 — Assignment Engine (walking skeleton)  *(NEW — ACP-0001)*
The heart of ClaudWorker V2: the deterministic execution core that carries one Jira issue through its
lifecycle ([16_WorkerStateMachine](16_WorkerStateMachine.md)). **Entirely deterministic — no AI
reasoning inside it** (Law 18); it coordinates the toolbelt and spawns disposable workers via a port.
- **Build:** package `internal/assignment` (name TBD) with:
  - `Assignment` type + lifecycle state machine (Idle→…→Done/Blocked/Cancelled/Failed).
  - Responsibilities: claim a Jira issue, create the Assignment, own its lifecycle & state, track
    retries & progress, coordinate the deterministic toolbelt (S1 git/jira), spawn disposable AI
    workers, resume after restart, hand off to QA and to Merge.
  - **Ports (interfaces):** `Locks` (issue-lock only for now), `Decider` (happy-path subset of the
    seven decisions, [20_DecisionEngine](20_DecisionEngine.md)), `Workers` (spawn `claude -p`),
    `Store` (**minimum** persistence), plus the existing `git`/`jira` toolbelt.
  - **Minimal adapters:** a real issue-lock, a minimal deterministic `Decider`, a `claude -p` worker
    runner, and a minimal `Store`. In-memory **fakes** for all ports drive deterministic tests.
- **Database rule (ACP-0001):** persist **only** what the engine actually needs (the Assignment
  record + issue-lock ownership — enough for Law 19 restart safety). Do **not** build the full
  [12_Database](12_Database.md) schema here; it emerges in S3.
- **Validation:** unit + integration tests (with fakes) for every transition, retry, and
  resume-after-restart path; race detector; a crash/restart test proving unfinished work resumes and
  completed work is never redone (Law 19).
- **Acceptance (Milestone M1 — "first light"):** one real MyOTGO issue is driven Ready → (skeleton
  lifecycle) → hand-off, resumable across a restart, with only the `Workers` port spending tokens
  (fakes in tests → zero tokens in CI).

### S3 — Persistence (emergent `state.db`)  *(was S2 — Databases; reframed by ACP-0001)*
- **Build:** formalize the S2 `Store` into `state.db` (and later `knowledge.db`) using only the tables
  S2 proved it needs; forward-only migrations, WAL. [12_Database](12_Database.md) is the **target**,
  realized incrementally — any table without a real consumer is not built (Law 17).
- **Validation:** migration up/down tests; concurrent read (dashboard) + single write; integrity
  check; the S2 engine passes unchanged against the real store (the `Store` interface is the seam).
- **Acceptance:** the DB creates, migrates, and survives a simulated crash (fsync + reopen); S2's
  resume-after-restart test passes against `state.db`, not just the minimal store.

### S4 — Knowledge Brain  *(new number; ACP-0001)*
- **Build:** file/symbol indexer, dependency graph, ADR/pattern/qa-map stores, **prompt-assembly**
  (the six slices, P9) — [04_ProjectBrain](04_ProjectBrain.md). Replaces the S2 skeleton's minimal
  prompt stub feeding the `Workers` port.
- **Validation:** index a real repo; relevant-files selection returns a small, correct set; prompt
  assembly is deterministic and size-bounded.
- **Acceptance:** given an issue key, the engine emits a small, correct prompt **using zero tokens** to
  assemble it.

### S5 — Lock Manager (full)  *(new number; ACP-0001 — extends the S2 issue-lock)*
- **Build:** add device + merge scopes to the S2 issue-lock; fencing, heartbeat, reaper, reconciler
  ([15_LockManager](15_LockManager.md)).
- **Validation:** property tests for L-1..L-6; concurrent-acquire race test; crash→steal→fence-reject
  test; reboot reconcile test.
- **Acceptance:** two concurrent acquirers never both win; a stale lock is reclaimed and the zombie
  write is rejected.

### S6 — Decision Engine (full)  *(new number; ACP-0001 — extends the S2 happy-path Decider)*
- **Build:** pure `Decide(ctx)` with all 7 decisions + precedence + stuck detector
  ([20_DecisionEngine](20_DecisionEngine.md)).
- **Validation:** table-driven tests covering every rule + precedence; D-2 purity/totality property
  test.
- **Acceptance:** every DecisionContext yields exactly one logged decision; **no tokens**.

### S7 — Worker Runner (full)  *(new number; ACP-0001 — extends the S2 `claude -p` runner)*
- **Build:** per-stage allowed-tools, budgets, JSON schema validation, teardown
  ([05_Workers](05_Workers.md)).
- **Validation:** one worker type (Manager) end-to-end on a real issue; invalid-JSON reprompt path;
  budget/timeout kill path.
- **Acceptance:** a Manager worker returns a schema-valid plan for a real issue; disposability verified
  (kill mid-run → nothing lost).

> **Old S7 "Orchestrator + Assignment state machine" moved to S2 (ACP-0001).** The Assignment Engine
> is now the walking skeleton built early (S2); S4–S7 above replace its minimal/faked adapters with
> the production Knowledge Brain, Lock Manager, Decision Engine, and Worker Runner. The
> "real merged issue, only Planning/Coding/QA spend tokens" outcome is reached when S7 completes and
> folds into **M2**.

### S8 — Repair Loop + gates + first plugins
- **Build:** Observe/Verify gates, plugin `Build/Verify/Repair` for `generic`, `flutter`, `dotnet`,
  `web` ([17_RepairLoop](17_RepairLoop.md), [18_PluginContract](18_PluginContract.md)); human-like QA
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

Milestones re-anchored by ACP-0001 (walking-skeleton first):

| ID | Name | Unlocks |
|---|---|---|
| **M1** | First light — walking skeleton (**S2**) | one real issue driven through the Assignment lifecycle, resumable across restart; only the `Workers` port would spend tokens (fakes in tests → zero-token CI) |
| **M2** | Production adapters + repair (S3–S8) | emergent `state.db`, real Brain/Lock/Decision/Worker adapters, repair loop + app/web/.NET plugins → routine software issues close (real merge, only Planning/Coding/QA spend tokens), with deferral |
| **M3** | Migration (S9) | new projects onboarded safely, owner-approved |
| **M4** | Ops + hardware + portability (S10–S13) | unattended, multi-domain, multi-project |
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

## Implementation discipline (binding)

These rules govern *how* each subsystem is built. They are part of the frozen spec.

### Per-subsystem gates (all four must pass before the next subsystem)
Every subsystem Sn must pass, in order, before S(n+1) begins:
1. **Unit tests** — the subsystem's own logic.
2. **Integration tests** — it works with the subsystems beneath it (real Jira/Git/SQLite/tools as
   applicable).
3. **Architecture compliance** — it obeys the frozen spec and the 19 System Laws
   ([19_SystemLaws](19_SystemLaws.md)); deterministic where the spec says deterministic; no forbidden
   dependency direction.
4. **Performance validation** — meets the subsystem's stated performance/latency/zero-token
   expectations (e.g. prompt assembly spends 0 tokens; lock acquire is O(1); a gate run completes
   within its budget).

A failed gate blocks progress. **Never skip roadmap order.**

### No inventing architecture (ACP rule)
No subsystem may invent architecture. If, during implementation, a subsystem discovers a missing or
contradictory architectural requirement:

1. **STOP** — do not code around it, do not guess.
2. Write an **Architecture Change Proposal (ACP)** — see `ACP_TEMPLATE.md` — describing the gap, the
   proposed change, affected docs/laws, and alternatives.
3. **Do not continue** past the gap until the ACP is **approved by the owner** and the spec is updated
   (version-bumped, below).

Small, obvious implementation choices that don't change the architecture do **not** need an ACP; a
change to any documented contract, law, schema, workflow, or invariant **does**.

### Simplicity gate (before implementing any feature)
Before implementing any feature, ask: **"Can this be made simpler?"**
- If **yes** → simplify it first (Law 17).
- If **no** → implement it.

And always prefer **deterministic Go over AI reasoning** (Laws 5/6/18). If a step can be a program, it
is a program.

### Spec versioning
The architecture is frozen at **v2.0.0** ([SPEC_VERSION.md](../SPEC_VERSION.md)). Implementation
targets exactly this version. Any approved ACP bumps the spec version (semver: breaking→major,
additive→minor, clarification→patch) and updates every affected document in the same change.

## What must never happen during construction

- No subsystem built before its dependencies pass acceptance (all four gates).
- No AI reasoning inside the Assignment Engine or any deterministic component (Law 18). `claude -p`
  workers exist **only** behind the `Workers` port and are **faked** in tests, so the engine and its
  CI stay deterministic and zero-token.
- No V1 file modified.
- No architecture change without an approved ACP + a spec version bump + updating this roadmap in the
  same change.
- No roadmap order skipped.
