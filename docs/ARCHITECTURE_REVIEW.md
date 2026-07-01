# Architecture Review (pre-freeze)

Review of the full ClaudWorker V2 specification (docs 00–19) for the issues called out in the
extension brief: duplicate concepts, contradictions, over-engineering, unnecessary complexity, token
waste, missing workflows/diagrams/failure-cases/recovery, and broken references — plus rename/removal
suggestions. **No code was written.** The architecture is **not yet frozen**; the recommendations
below are for the owner to accept/reject before freeze.

## Verdict

The spec is internally consistent and implementable. Automated checks pass: all 20 docs present,
**every internal link resolves**, worker count ("exactly four") consistent, `maxAttempts=3`
consistent across 03/13/16/17, dashboard port (8790) consistent, engine-home path consistent. No
contradictions block a freeze. The items below are refinements, not blockers.

## 1. Contradiction found and resolved — "worker" overload

- **Issue:** [05_Workers](05_Workers.md) defines a *worker* as a disposable per-step `claude -p`
  process, while the requested [16_WorkerStateMachine](16_WorkerStateMachine.md) describes a *worker*
  that persists Idle→…→Done. Read literally these conflict (a disposable thing can't also be
  long-lived).
- **Resolution:** introduced one term — **Worker Slot** (a.k.a. Assignment): the durable,
  deterministic execution unit that carries one issue and *spawns* disposable workers for its
  reasoning steps. Doc 16 is the slot's state machine; doc 05's "worker" stays the ephemeral step. A
  **state-mapping table** (16 ↔ 03) and cross-links were added to 02/03/05.
- **Owner decision needed:** confirm the vocabulary. Option A (current): *Worker Slot* (durable) +
  *Worker* (ephemeral). Option B: rename the durable unit to **Assignment** or **Job** to remove the
  word "worker" from the durable concept entirely (arguably clearer, but diverges from the requested
  doc title "WorkerStateMachine"). Recommendation: keep A, it satisfies the requested doc names.

## 2. Over-engineering candidates (Law 17 — simplify)

- **Advisory file/folder/module locks ([15](15_LockManager.md) §4–6).** With worktree-per-issue +
  tiny branches + immediate merge + low `maxConcurrent` (default 3), same-file collisions are rare
  and the Integrator already resolves tiny conflicts. **Recommendation:** ship v1 with only the
  **hard** locks (device, issue, merge); implement advisory module/file locks **later**, *only if*
  measured merge-conflict rate justifies them. Keep the design in 15 (it's correct) but mark it
  Phase-2. This removes real complexity from the first build without losing the option.
- **`repo` lock `shared` mode ([15](15_LockManager.md) §2/16).** The only use of shared-mode locking,
  for rare repo-wide refactors. **Recommendation:** drop `shared` mode (and possibly the `repo`
  scope) for v1; a `module` lock covers the realistic cases. Re-add if a real need appears.
- **Net effect:** v1 Lock Manager = device + issue + merge (hard) only. Simpler, still satisfies
  Laws 10/11 and every hard invariant (L-1…L-6 hold; advisory-lock invariants become no-ops).

## 3. Duplication (acceptable, with a note)

- **Hardware gates appear in both [10_Hardware](10_Hardware.md) and [17_RepairLoop](17_RepairLoop.md).**
  Doc 10 is the **authority** (the hard PASS/FAIL gates + honesty rules); doc 17 restates them as the
  Verify step per domain. This is intentional (17 is the uniform loop view) and 17 links back to 10,
  but the gate *lists* are duplicated. **Recommendation:** at implementation time, make the gate list
  live in the **plugin manifest** ([18](18_PlugInContract.md)) as the single source, and have both
  docs reference it rather than re-enumerate. Low priority; not a contradiction.
- **QA rungs vs repair Verify.** [06_QA](06_QA.md) (rungs) and [17](17_RepairLoop.md) (Verify) overlap
  by design; 17's Verify == QA's chosen rung. Cross-linked; fine.

## 4. Token-waste audit — clean

- Every deterministic step (Observe/Verify/build/screenshot/DRC/merge/lock) is Go (Laws 5/6/18).
- Only Planning/Coding/QA/(rare Integrator) spend tokens (16 W-2).
- Plugin `Repair()` runs **deterministic** auto-fixes *before* spawning a Developer, saving tokens
  ([18](18_PlugInContract.md) §9). Good.
- AC generation is deterministic first (`GenerateAcceptanceCriteria`), AI only refines. No double-AI.
- One residual risk to watch in implementation: the "deepen context once" escalation
  ([17](17_RepairLoop.md)) must stay a *dependency-graph slice*, never a whole-repo dump (Law 14).

## 5. Missing pieces — now covered

The extension added the previously-thin areas: full **lock lifecycle/recovery/fencing** (15), every
**state transition** with entry/exit/timeout/recovery (16), the **repair loop** per domain with
stop/split/defer rules (17), the **uniform plugin interface** (18), and the **system laws** (19).
Failure/recovery cases now covered end-to-end: worker crash, machine reboot, cancellation, merge
conflict, stuck-loop, budget exhaustion, missing resource/human block, zombie-write (fencing).

## 6. Broken references — none

Automated link check across all docs + README: **0 broken internal links**. Every `NN_Name.md`
reference and the `../README.md` back-link resolve.

## 7. Rename / cleanup suggestions (cosmetic)

- **File casing:** `18_PlugInContract.md` uses `PlugIn` while every other reference uses `Plugin`/
  `Plugins`. Kept as-is because the brief specified that exact filename; **suggest** normalizing to
  `18_PluginContract.md` at freeze for consistency (one-line index update).
- **"Integrator" worker:** it's invoked only on semantic merge conflict (≈1% of merges); 99% of
  merges are pure Go. It's correctly listed as one of the four workers, but it's barely a reasoning
  worker. No change needed — just don't build heavy machinery around it.

## Recommended actions before freeze

1. **Accept the "Worker Slot" vocabulary** (or choose Assignment/Job) — §1.
2. **Scope the Lock Manager v1 to hard locks only** (device/issue/merge); mark advisory + repo-shared
   as Phase-2 — §2. *(This is the main simplification.)*
3. **Move gate lists into plugin manifests** as the single source; keep 10/17 as references — §3.
4. *(Optional, at freeze)* normalize `18_PlugInContract.md` → `18_PluginContract.md` — §7.

None of these require rewriting the specification; 2–4 are annotations/scoping the implementation will
honor. With §1 confirmed, the architecture is ready to **freeze**.
