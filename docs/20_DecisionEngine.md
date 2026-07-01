# 20 — Decision Engine

The Decision Engine is the deterministic Go component that makes **every control-flow decision** in
the platform: retry, repair, escalate, split, defer, merge, abort. It is **rules only — no AI** (Law
18). Workers supply *evidence and recommendations*; the Decision Engine supplies the *decision*; the
Orchestrator *executes* it.

> **The model proposes, the Decision Engine disposes.** A `claude -p` worker may *recommend* "this
> should be split" or "this conflict resolves this way", but whether to split, retry, defer, merge, or
> abort is computed from deterministic inputs by fixed rules. This removes probabilistic behavior from
> the parts of the system that must be safe and reproducible (P5, NFR-7).

## Why a separate component

Decision logic was previously scattered across [03_Workflow](03_Workflow.md),
[16_WorkerStateMachine](16_WorkerStateMachine.md), and [17_RepairLoop](17_RepairLoop.md). Centralizing
it:

- Guarantees **one** place defines "when do we give up / split / defer" — no drift.
- Makes the rules **testable in isolation** (pure function: inputs → decision).
- Keeps the state machine and repair loop thin: they *ask* the Decision Engine and *act*.

## Inputs (all deterministic, zero tokens to gather)

The Decision Engine is a pure function of a **DecisionContext** assembled by the engine from the
Execution State + Knowledge Brain + tool outputs:

| Input | Source | Used for |
|---|---|---|
| `attempt` (count) | `state.db.assignments` | retry vs escalate |
| `maxAttempts` | config ([13_Config](13_Config.md)) | retry cap |
| `gate_results` (per-gate PASS/FAIL/DEFER) | plugin `Verify` ([18_PluginContract](18_PluginContract.md)) | repair vs merge vs defer |
| `gate_pass_ratio` (now vs prev) | `state.db.metrics` | progress / stuck detection |
| `failure_fingerprint` (now + history) | Knowledge Brain `patterns` | stuck detection, known dead-ends |
| `qa_verdict` (PASS/FAIL/DEFER + reasons) | QA worker ([06_QA](06_QA.md)) | merge vs repair vs defer |
| `plan_size` (files/modules touched) | Manager plan ([05_Workers](05_Workers.md)) | split decision |
| `defer_signals` (missing device/creds/hardware/human) | tools + plugin `ReserveResources` | defer classification |
| `lock_availability` (merge/device) | Lock Manager ([15_LockManager](15_LockManager.md)) | proceed vs wait |
| `token_budget` / `wallclock` remaining | usage guard + Execution State | abort/escalate on budget |
| `conflict_kind` (none/textual/semantic) | git tools ([07_Git](07_Git.md)) | merge vs integrator vs escalate |
| `eligibility` (Automation field) | Jira cache ([22_Migration](22_Migration.md)) | admit vs skip |

No prompt, no model call — assembling a DecisionContext is free.

## The seven decisions

Each is produced by a fixed rule. Outputs are enumerated; the Orchestrator maps each to concrete
actions.

### 1. RETRY
Re-run the current stage without changing scope (e.g. flaky test, invalid-JSON reprompt, transient
git/tool error).
- **Rule:** transient/flaky failure **and** `stage_reprompts < 1` (for worker JSON) **or** the failing
  gate is marked flaky in Knowledge Brain **and** `flaky_retries < flakyCap`.
- **Not** counted against `maxAttempts` unless it's a genuine code failure.

### 2. REPAIR
Loop back to Coding with the structured failure as new context ([17_RepairLoop](17_RepairLoop.md)).
- **Rule:** a real (non-flaky, non-environmental) gate/QA **FAIL** **and** `attempt < maxAttempts`
  **and** **not** stuck (below).
- **Effect:** `attempt++`; add failure to *current failures*; re-enter Coding.

### 3. ESCALATE
Stop autonomous work and hand to a human (Blocked/Failed → NEEDS_HUMAN,
[16_WorkerStateMachine](16_WorkerStateMachine.md)).
- **Rule (any):** `attempt >= maxAttempts`; **stuck** (same `failure_fingerprint` ≥ 2, or
  `gate_pass_ratio` not increasing across 2 attempts); `token_budget`/`wallclock` exhausted; a
  required non-deferrable resource missing; conflict unresolved after K refresh cycles.
- **Effect:** label `needs-human`, attach evidence + fingerprints + what was tried, notify, release
  the Assignment (P7).

### 4. SPLIT
Recommend/perform decomposition into linked sub-issues ([08_Jira](08_Jira.md),
[17_RepairLoop](17_RepairLoop.md)).
- **Rule (any):** `plan_size` exceeds `splitThreshold` (files/modules, config); the plan spans
  **multiple repos/plugins**; fingerprints **oscillate** between two distinct sub-goals; AC has
  independently-shippable items.
- **Effect:** create linked Jira sub-issues (deterministically), set their Automation field to
  `Needs Review`, and either continue with the first slice or return the parent to backlog. A worker
  may *propose* the split boundaries; the engine *creates* them.

### 5. DEFER
Mark a specific check deferred; do not block the merge ([06_QA](06_QA.md), Law 7).
- **Rule:** a gate/QA returns **DEFER** or a `defer_signal` is present (missing device/hardware/
  credentials, customer/design/owner wait, visual/human-only) **and** the failure is **environmental,
  not code**.
- **Effect:** record deferral (Execution State) + reusable how-to (Knowledge Brain) + linked follow-up
  issue; treat as non-blocking. **Never** rendered as a pass.

### 6. MERGE
Proceed to integrate ([07_Git](07_Git.md)).
- **Rule:** all **runnable** gates PASS (deferred gates recorded, none FAIL) **and** `merge:<repo>`
  lock acquirable **and** branch refreshes cleanly onto `development`.
- **Sub-rule (conflict):** `conflict_kind == textual` → auto-resolve deterministically + re-verify;
  `== semantic` → consult **Integrator** worker for a *recommendation*, then the Decision Engine picks
  MERGE / RETRY(refresh) / ESCALATE.

### 7. ABORT
Cancel the Assignment cleanly (distinct from Escalate: no human needed, just stop).
- **Rule:** owner **stop-issue**; issue became ineligible mid-flight (Automation flipped to
  Disabled/Manual Only, or reassigned to a human); duplicate detected (another Assignment already
  merged the equivalent work).
- **Effect:** cooperative cancel ([15_LockManager](15_LockManager.md)): fence bump, kill worker, clean
  worktree, release locks, return the issue to its prior stable status. Idempotent.

## Decision precedence (deterministic ordering)

When multiple rules could fire, the Decision Engine evaluates in this fixed order and takes the first
match (so behavior is unambiguous):

```
1. ABORT        (external cancel / ineligibility / duplicate)
2. ESCALATE     (budget/attempts exhausted, stuck, required resource missing, unresolved conflict)
3. DEFER        (environmental, non-code failures)
4. SPLIT        (oversized / multi-repo / oscillating / separable AC)
5. MERGE        (all runnable gates pass, lock + clean refresh)
6. REPAIR       (real failure, attempts remain, not stuck)
7. RETRY        (transient/flaky, within sub-caps)
```

Rationale: never keep spending on a cancelled/ineligible issue; stop before over-spending; prefer
honest deferral over forcing; decompose before grinding; ship when green; otherwise fix; retry only
transient noise.

## Progress & stuck detection (the core deterministic heuristic)

- **Progress** = `gate_pass_ratio` strictly increased since the last attempt, **or** a *new*
  `failure_fingerprint` appeared (plausibly closer).
- **Stuck** = same `failure_fingerprint` seen ≥ 2 times, **or** `gate_pass_ratio` flat across 2
  attempts.
- Stuck → ESCALATE early (don't burn the full `maxAttempts` against a wall). This is pure arithmetic
  over Execution-State metrics; no model judges "am I stuck".

## Worked examples

- **Flaky widget test:** gate FAIL, fingerprint = known-flaky, `flaky_retries=0` → **RETRY** (not an
  attempt). Passes on retry → continue.
- **Real compile error, attempt 1/3, new fingerprint:** → **REPAIR** (attempt→2), failure added to
  context.
- **Same DRC violation twice:** fingerprint repeated → **stuck** → **ESCALATE** with the DRC report,
  even though attempt 2 < max.
- **No ESP32 board attached, firmware builds + Wokwi passes:** on-hardware gate returns DEFER →
  **DEFER** the physical check, **MERGE** the rest ([10_Hardware](10_Hardware.md)).
- **Plan touches app + backend + firmware:** `plan_size` multi-repo → **SPLIT** into three linked
  sub-issues before coding.
- **Owner flips Automation=Manual Only mid-run:** → **ABORT**, return issue untouched.

## Interfaces

- Internal (Go): `Decide(ctx DecisionContext) -> Decision{kind, params, reason}` — pure, total,
  logged to `state.db.events` as `kind = decision` with the inputs that produced it (full auditability,
  NFR-9).
- The Orchestrator calls `Decide` at each stage boundary and executes the returned decision; it never
  branches on model output directly.

## Invariants (decision-specific)

- **D-1** Every control-flow decision is produced by `Decide` from deterministic inputs; no model call
  occurs inside `Decide` (Law 18).
- **D-2** `Decide` is pure and total: same DecisionContext → same Decision; every context yields
  exactly one decision (precedence guarantees this).
- **D-3** Every decision is logged with its inputs (auditable, reproducible).
- **D-4** Loops are bounded: REPAIR respects `maxAttempts`; RETRY respects its sub-caps; ESCALATE is
  the terminal safety net.
- **D-5** DEFER never masquerades as PASS; ABORT never overwrites owner work.
