# Architecture Review (Round 2) & Freeze Recommendation

Second full review of the ClaudWorker V2 specification (docs 00–22) after the migration/decision
extension and the owner-approved simplifications. Scope of this pass: over-engineering, duplicate
concepts, contradictions, unnecessary AI, token waste, large prompts, complex workflows, subsystems
that can merge, and anything that can become deterministic Go. **No code was written.**

## What changed since Round 1 (all owner-approved)

1. **"Worker Slot" → "Assignment"** everywhere (durable execution unit). "Worker" now means only the
   ephemeral `claude -p` step. Cross-doc rename applied; invariants renamed W-* → A-*.
2. **Lock Manager V1 simplified** to exactly three hard locks — **issue, device, merge**. Advisory
   file/folder/module/repo locks moved to a clearly-marked **Future expansion** (build only if
   measured conflict rate justifies). Schema stays forward-compatible.
3. **Project Brain split** into two independent stores: **Knowledge Brain** (`knowledge.db` +
   `knowledge/` markdown — persistent) and **Execution State** (`state.db` — temporary, rebuildable).
   Prompts read from the Knowledge Brain only.
4. **New doc 20 — Decision Engine:** all control-flow (retry/repair/escalate/split/defer/merge/abort)
   centralized as deterministic rules; removes decision logic scattered across 03/16/17.
5. **New doc 21 — Implementation Roadmap:** frozen subsystem order S0–S13, milestones M0–M5, rollback.
6. **New doc 22 — Migration:** the reusable first-phase onboarding (repo+Jira analysis, Automation
   eligibility, normalization, deferral, Knowledge Brain init, validation, human-review gate).

## Automated checks (this pass)

- **24 docs** present (00–22 + this review + README target). **0 broken internal links.**
- No leftover `brain.db`; no leftover `runs`-table references; invariant cross-refs consistent
  (A-*). Worker count "exactly four" consistent; `maxAttempts=3` consistent across 03/13/16/17/20;
  port 8790 consistent; engine-home path consistent.

## Findings

### Over-engineering — addressed
- The Round-1 over-engineering flag (advisory locks) is **resolved**: they're now Future-only. V1 lock
  surface is minimal (3 hard locks) and still satisfies every hard invariant (L-1…L-6). ✔
- No other subsystem is heavier than its job. The Decision Engine *reduces* complexity by centralizing
  rules rather than adding a layer.

### Duplicate concepts — controlled
- **Decision logic** previously appeared in 03/16/17. Now **doc 20 is authoritative**; 16/17 reference
  it ("the Decision Engine chooses"). Remaining prose in 16/17 is descriptive, not a second rule set.
  ✔ (Recommendation: at implementation, 16/17 should not re-encode thresholds — import them from 20.)
- **Hardware gate lists** still appear in both 10 and 17. Unchanged from Round 1: acceptable because
  both reference the plugin manifest as the eventual single source ([18](18_PluginContract.md)).
  Low-priority cleanup, not a contradiction.
- **Deferral** is described in 03/06/10/17/20/22 — but each from its own angle (workflow / QA / hardware
  / repair / decision rule / migration classification). One definition (Law 7 + doc 06); others
  reference it. No divergence found. ✔

### Contradictions — none blocking
- The former worker-vs-slot contradiction is fully resolved by the Assignment rename + the mapping
  table in [16](16_WorkerStateMachine.md). ✔
- Brain split is consistent across 02/04/12/14/16/19. ✔

### Unnecessary AI / token waste / large prompts — clean
- Migration (22) and the Decision Engine (20) are deterministic; AI is confined to: plan, code, QA
  judgment, ambiguous-duplicate judgment, AC drafting, and one optional architecture-prose refinement.
- Prompts remain the six small slices from the **Knowledge Brain** (P9). Execution bookkeeping
  (attempt counts, metrics, locks) never enters a prompt — it feeds the Decision Engine instead.
- No whole-repo prompt path exists; the one escalation that "deepens context" is a bounded
  dependency-graph slice ([17](17_RepairLoop.md)), not a repo dump.

### Subsystems that could merge — considered, kept separate on purpose
- **Decision Engine vs Orchestrator:** could be one component, but keeping `Decide()` pure and separate
  makes it unit-testable and keeps the state machine thin. Keep separate.
- **Knowledge Brain vs Execution State:** deliberately separate (the whole point of the split). Keep.
- **Migration vs Orchestrator:** migration is a one-shot phase with a human gate; folding it into the
  runtime loop would complicate both. Keep separate, invoked by `cwv2 migrate`.

### Things that became (or already are) deterministic Go — confirmed
- Locking, merging, gate PASS/FAIL, decisions, repo/Jira analysis, duplicate detection (similarity),
  device discovery, dependency graph, prompt assembly, migration validations. AI only where reasoning
  is irreducible. ✔ (Law 18 holds across the spec.)

## Owner decisions — RESOLVED and applied (v2.0.0 freeze)

The four Round-2 open decisions were resolved by the owner and applied across the spec:

1. **Automation field** — official Jira **single-select** custom field named **`Automation`** with
   values **Enabled / Disabled / Manual Only / Needs Review** (not a checkbox, not a label).
   Applied in 22/20/16/15/12/08.
2. **Thresholds are configurable, never hardcoded** — `abandoned_days`, `large_issue_threshold`,
   `split_threshold`, `imgdiff_threshold`, `lock_ttl`, `retry_limits` are project defaults under
   `defaults:` in [13_Config](13_Config.md); every project overrides without recompiling.
3. **Plugin naming normalized** — `PlugIn` → `Plugin` everywhere; file renamed to
   `18_PluginContract.md`; all links updated.
4. **Advisory locks remain Future-only** — V1 is issue/device/merge; advisory locks are built only
   when a measurable metric (merge-conflict rate above a configured threshold) justifies them
   ([15_LockManager](15_LockManager.md) Future expansion).

Also applied at freeze: **restart safety is now Law 19** (a core invariant); the spec is **versioned**
([SPEC_VERSION.md](../SPEC_VERSION.md)); and an **ACP process** governs future changes
([ACP_TEMPLATE.md](../ACP_TEMPLATE.md), [21_ImplementationRoadmap](21_ImplementationRoadmap.md)).

## Freeze — DONE

**Status: FROZEN at v2.0.0 (2026-07-01).**

The specification is internally consistent (0 broken links, no contradictions), minimal (Law 17 —
Lock Manager V1 trimmed, no speculative subsystems), deterministic-first (Law 18), crash-safe (Law
19), and complete for construction: the Implementation Roadmap (21) gives a dependency-ordered build
plan with per-subsystem gates (unit + integration + architecture-compliance + performance), milestones
M0–M5, and rollback; the Migration spec (22) defines a safe, reusable first phase.

Implementation proceeds strictly in roadmap order, beginning at **S0**, deterministic core first (zero
tokens through M0). No architecture change is made except via an approved ACP + a spec version bump.
