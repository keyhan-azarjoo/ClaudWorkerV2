# ACP-0001 — Assignment Engine before Database

- **ACP:** 0001
- **Title:** Build the Assignment Engine as a walking skeleton before the full database
- **Author:** keyhanazarjoo
- **Date:** 2026-07-02
- **Status:** Approved (owner-initiated)
- **Discovered during:** roadmap validation after S1 (before starting the old S2 "Databases")
- **Target spec version:** 2.1.0 (MINOR — reorders the build sequence; no contract/law/schema removed)

This document doubles as the required **design review**. No engine code is written under this ACP;
it only revalidates and re-sequences the roadmap.

## 1. Problem / gap

The frozen roadmap ([21_ImplementationRoadmap](../21_ImplementationRoadmap.md) v2.0.0) builds the full
persistence layer (old **S2 — Databases**: `state.db` + `knowledge.db` + the complete
[12_Database](../12_Database.md) schema) **before** the Assignment Engine (old **S7 — Orchestrator +
Assignment state machine**). That designs persistence from *speculation* about what execution will
need, rather than from the real needs of the execution core.

## 2. Evidence

- [12_Database](../12_Database.md) specifies two databases and ~10 tables (assignments, metrics,
  events, deferrals, issues_cache, locks, files, symbols, deps, decisions, patterns, qa_maps).
  Most of those are consumed by subsystems that do **not** exist yet (Knowledge Brain, full Lock
  Manager, Repair Loop, Dashboard). Building them now is speculative inventory.
- The roadmap's **own stated principle** already argues for the opposite of bottom-up-DB-first:
  > "Vertical slice early. As soon as the minimum spine exists, prove one real Jira issue
  > end-to-end." (21_ImplementationRoadmap §Principles)
  Yet the frozen sequence defers that spine to S7 and front-loads a full DB at S2 — an internal
  tension.
- **Law 17 (simplicity):** "Remove before adding. No speculative machinery." A full DB schema with
  no consumer is speculative machinery.
- The Assignment Engine is what actually *writes* execution state; its concrete needs are the correct,
  non-speculative source of the persistence schema.

## 3. Proposed change

Re-sequence the roadmap so the **Assignment Engine** is the next subsystem (new **S2**), built as a
**walking skeleton** that drives one real Jira issue through its lifecycle end-to-end, persisting
**only the minimum** it actually needs. The full persistence design becomes a later, **emergent**
subsystem (new **S3 — Persistence**) derived from what S2 proved it needs; [12_Database](../12_Database.md)
remains the *target/reference*, realized incrementally rather than built up front.

The Assignment Engine is entirely **deterministic** — no AI reasoning lives inside it (Law 18). It
*coordinates* the deterministic toolbelt and *spawns* disposable AI workers, but contains none itself.

### Architecture is unchanged — only build order

Every component named here already exists in the frozen architecture (Lock Manager
[15](../15_LockManager.md), Decision Engine [20](../20_DecisionEngine.md), Worker Runner
[05](../05_Workers.md), Execution State [04](../04_ProjectBrain.md)/[12](../12_Database.md), Assignment
lifecycle [16](../16_WorkerStateMachine.md)). No component is added or removed. This ACP changes
*sequence*, not *design*.

### The one honest nuance (design-review pushback)

The Assignment Engine **cannot** be built in total isolation before everything else: its listed
responsibilities require, at minimum, an **issue lock** (Lock Manager), **deterministic transitions**
(Decision Engine), and a way to **spawn workers** (Worker Runner). Building it "before the database"
is correct; building it "before those three" is not.

Resolution — **ports & minimal adapters** (hexagonal): the engine depends on small **interfaces**
(`Locks`, `Decider`, `Workers`, `Store`, plus the existing `git`/`jira` toolbelt). S2 ships a
*minimal real implementation* of each (issue-lock only; the happy-path subset of the seven decisions;
a `claude -p` runner; a minimal persistence store) plus in-memory **fakes** for deterministic tests.
Later subsystems **extend** those same interfaces to full spec — they are not rebuilt:

- Full Lock Manager (device + merge scopes, fencing, reaper) — later, extends `Locks`.
- Full Decision Engine (all 7 decisions + precedence + stuck detector) — later, extends `Decider`.
- Full Worker Runner (schema validation, budgets, least-privilege tools) — later, extends `Workers`.
- Knowledge Brain (small-prompt assembly) — later; until then the Worker prompt is a minimal stub.

This keeps the engine deterministic and testable now, defers the speculative parts, and lets
persistence emerge.

## 4. Affected documents

- [21_ImplementationRoadmap](../21_ImplementationRoadmap.md): dependency graph re-sequenced; new S2
  (Assignment Engine); old S2 (Databases) → new S3 (Persistence, emergent); milestone **M1 "first
  light"** moves to S2; downstream capability sections shift by one (mapping table added).
- [SPEC_VERSION.md](../../SPEC_VERSION.md): 2.0.0 → 2.1.0 with history entry.
- No change to laws, schemas as *contracts*, workflow, or invariants. [12_Database](../12_Database.md)
  stays the persistence *target*; it is realized incrementally, and any table the engine ends up not
  needing is dropped from the emergent schema (a future PATCH ACP if 12 itself must change).

## 5. Alternatives considered

1. **Keep the frozen order (DB first).** Rejected: speculative, contradicts the roadmap's own
   vertical-slice principle and Law 17.
2. **Build the full Lock Manager + Decision Engine + Worker Runner first, then the engine (current
   S4–S7), just moving DB after.** Rejected as *less* simple than needed: it still front-loads full
   versions of three subsystems before proving the spine. The walking-skeleton (ports + minimal
   adapters) is the simpler path to M1 and surfaces real needs sooner.
3. **Assignment Engine with NO persistence at all first.** Rejected: violates Law 19 (restart
   safety) — the engine must resume after a crash, which requires *minimum* persistence. Hence
   "minimum persistence," not "none."

## 6. Determinism & token impact

- The Assignment Engine is 100% deterministic Go (Law 18); AI appears only behind the `Workers`
  port (disposable `claude -p`). Building the skeleton and all its tests spends **zero tokens**
  (workers are faked in tests). No change to the token model.

## 7. Compatibility & migration

- No existing project/config/data exists yet (pre-M1), so there is nothing to migrate. The `Store`
  interface localizes persistence so the emergent `state.db` (new S3) can replace the minimal store
  without touching engine logic.

## 8. Rollback

- Revert this ACP's commit → the roadmap returns to v2.0.0 order and SPEC_VERSION to 2.0.0. No code
  was written under this ACP, so rollback is a pure documentation revert.

## 9. Decision

- **Owner decision:** **Approved** (owner-initiated in the roadmap-change request).
- **New spec version:** **2.1.0**.
- **Follow-up:** roadmap + SPEC_VERSION updated in the same change as this ACP. Implementation of the
  Assignment Engine (new S2) begins **only on the owner's next go-ahead** — this ACP explicitly does
  **not** start coding.
