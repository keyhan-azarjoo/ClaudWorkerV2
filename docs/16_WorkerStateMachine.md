# 16 — Worker State Machine (Assignment Lifecycle)

This document defines the complete lifecycle of an **Assignment** — the deterministic execution unit
that carries **one Jira issue** from assignment to done. It is the fine-grained execution view of the
issue lifecycle in [03_Workflow](03_Workflow.md): doc 03 describes the *issue* in Jira terms; this
doc describes the *Assignment* driving it, including the states doc 03 abstracts away (Idle, Waiting,
Repair, Cancelled, Failed).

## Terminology (read this first — avoids a false contradiction)

- **Assignment** — a deterministic, orchestrator-owned execution context bound to one issue for the
  duration of its journey. It holds the issue lock ([15_LockManager](15_LockManager.md)), the
  worktree, and its record in the **Execution State** store ([04_ProjectBrain](04_ProjectBrain.md),
  [12_Database](12_Database.md)). The state machine below is the **Assignment's** state machine and is
  100% deterministic Go. (Earlier drafts called this a "Worker Slot"; it is now **Assignment**
  everywhere.)
- **Worker (reasoning)** — a short-lived `claude -p` process spawned by the Assignment for exactly one
  reasoning step (Planning, Coding, QA judgment, or an Integrator judgment). It is **disposable and
  holds no long-term memory** ([05_Workers](05_Workers.md), P4). An Assignment may spawn many workers
  over its life; none of them persist.

So: **the Assignment is durable and deterministic; the workers it spawns are ephemeral and the only
place tokens are spent.** "No worker owns long-term memory" holds — all durable state is in the
Assignment's Execution-State record, the Knowledge Brain, and Git.

## States

```
        Idle
          │ admitted (Scheduler: usage + concurrency + priority)
          ▼
        Waiting ──(locks/resources unavailable)──▶ Waiting (bounded)
          │ AcquireSet(issue,device?) granted
          ▼
        Assigned ── worktree from newest development ready
          │
          ▼
        Planning ──(Manager worker)──▶ plan JSON valid
          │
          ▼
        Coding ──(Developer worker + tools)──▶ changes committed on branch
          │
          ▼
        Building ──(deterministic gate: build+lint+fast tests)
          │ pass                         │ fail
          ▼                              ▼
        QA ──(QA worker, human-like)   Repair ◀────────────┐
          │ PASS   │ FAIL   │ DEFER        │                │
          │        └────────┼──────────────┘ (attempt++)    │
          │                 │  fail again ────────────────▶ │ (loop ≤ maxAttempts)
          ▼                 ▼
        ReadyToMerge     (attempts exhausted) ─▶ Failed
          │ Acquire(merge:repo)
          ▼
        Merged ──(Integrator: refresh→verify→merge --no-ff→delete branch)
          │
          ▼
        Done ── Jira transitioned, result comment, locks released, Assignment → Idle

   Off-nominal (reachable from most states):
     Blocked    — waiting on a human/resource; issue keeps its place, Assignment may release to serve others
     Cancelled  — owner stop-issue; cooperative teardown
     Failed     — unrecoverable / attempts exhausted → becomes NEEDS_HUMAN in Jira
```

All transitions are chosen by the deterministic **Decision Engine** ([20_DecisionEngine](20_DecisionEngine.md)),
never by a model.

## Mapping to the issue lifecycle ([03_Workflow](03_Workflow.md))

| Assignment state (this doc) | Issue state (doc 03) | Reasoning worker? |
|---|---|---|
| Idle | — (no issue) | no |
| Waiting | pre-CLAIMED (admitted, acquiring locks) | no |
| Assigned | CLAIMED | no |
| Planning | PLANNED | **Manager** |
| Coding | DEVELOPING | **Developer** |
| Building | BUILT (gate) | no |
| QA | QA | **QA** |
| Repair | DEVELOPING (loop iteration) | **Developer** |
| ReadyToMerge | (QA PASS, awaiting merge lock) | no |
| Merged | MERGING | Integrator *only if conflict* |
| Done | CLOSED | no |
| Blocked | NEEDS_HUMAN (resource/human wait) | no |
| Cancelled | (stop-issue) | no |
| Failed | NEEDS_HUMAN (attempts exhausted) | no |

Only Planning, Coding, QA, and (rarely) Merged spend tokens. Every other transition is free (P5).

## Per-state specification

For each state: **entry conditions**, **actions**, **exit conditions**, **retry**, **timeout**,
**recovery**. Exit choices are made by the Decision Engine ([20_DecisionEngine](20_DecisionEngine.md)).

### Idle
- **Entry:** Assignment has no issue (fresh, or just finished one).
- **Actions:** none; available to the Scheduler.
- **Exit:** Scheduler admits an issue (usage guard OK, concurrency < max, issue is top-priority,
  lockable, and its **Automation** field allows autonomous processing — see
  [22_Migration](22_Migration.md)) → Waiting.
- **Timeout/recovery:** n/a. An Idle Assignment with an empty backlog is a *success*, not waste (P7).

### Waiting
- **Entry:** admitted to a specific issue.
- **Actions:** `AcquireSet(issue:<KEY>, device? )` in canonical order
  ([15_LockManager](15_LockManager.md)). V1 acquires only **hard** locks (issue, device if needed).
- **Exit:** issue lock (+ any needed device lock) granted → Assigned.
- **Retry:** re-attempt lock acquisition on the Scheduler tick.
- **Timeout:** `waitTimeout` → release any partial locks, return issue to backlog, Assignment → Idle.
- **Recovery:** crash here holds no durable resources except possibly the issue lock → reaped by TTL.

### Assigned
- **Entry:** locks held.
- **Actions:** `git fetch`; fast-forward `development`; create `agent/<KEY>-<slug>` in a fresh
  worktree ([07_Git](07_Git.md)); open the Execution-State record for this Assignment.
- **Exit:** worktree ready → Planning.
- **Timeout:** git op timeout → Decision Engine: retry once, else Failed.
- **Recovery:** crash → worktree/branch orphan detected on restart and cleaned; issue returns to
  Waiting.

### Planning
- **Entry:** worktree ready.
- **Actions:** assemble the small prompt from the **Knowledge Brain**
  ([04_ProjectBrain](04_ProjectBrain.md), P9); spawn **Manager** worker; validate plan JSON schema
  ([05_Workers](05_Workers.md)); persist plan (Execution State) + any proposed ADRs (Knowledge Brain).
- **Exit:** valid plan → Coding.
- **Retry:** invalid JSON → one bounded reprompt; still invalid → Decision Engine: Repair (attempt++)
  or Failed.
- **Timeout:** worker wall-clock/token budget exceeded → kill worker, attempt++, retry or Failed.
- **Recovery:** worker is disposable; nothing lost — re-run Planning from the same worktree.

### Coding
- **Entry:** plan available.
- **Actions:** spawn **Developer** worker with plan + relevant files + architecture summary + recent
  decisions + current failures (all from the Knowledge Brain slice, P9). Worker edits files in its
  worktree and calls tools to build/format/test locally; commits small/frequent on its branch (author
  `keyhanazarjoo`, C-2).
- **Exit:** changes committed → Building.
- **Retry:** no code changes produced → one reprompt; still nothing → Repair/Failed.
- **Timeout:** budget exceeded → kill, attempt++, retry or Failed (partial commits stay on the branch
  and are visible to the next attempt).
- **Recovery:** crash → branch commits persist; restart resumes at Building or re-enters Coding with
  the partial work as context.

### Building (deterministic gate)
- **Entry:** changes committed.
- **Actions:** run plugin gates ([11_Plugins](11_Plugins.md), [18_PluginContract](18_PluginContract.md)):
  build + lint + fast unit tests. Zero tokens. Plugin `Repair()` deterministic auto-fixes run first.
- **Exit:** all gates pass → QA. Any gate fails → Repair with the structured failure.
- **Retry:** flaky suites retried a bounded number deterministically ([06_QA](06_QA.md)).
- **Timeout:** build timeout → treated as failure → Repair (attempt++).
- **Recovery:** idempotent; safe to re-run from the branch state.

### QA (human-like)
- **Entry:** build green.
- **Actions:** spawn **QA** worker; run the highest possible QA rung ([06_QA](06_QA.md)) via tools
  (launch/navigate/screenshot/imgdiff/OCR/log); render a verdict.
- **Exit (Decision Engine):**
  - **PASS** → ReadyToMerge.
  - **FAIL(reasons)** → Repair (attempt++).
  - **DEFER(reasons)** → record deferral (Execution State) + follow-up issue; if no FAILs →
    ReadyToMerge; else Repair.
- **Retry:** flaky visual checks use tolerance + bounded retry before counting as FAIL.
- **Timeout:** budget exceeded → attempt++, retry or Failed.
- **Recovery:** disposable worker; re-run QA from the same branch.

### Repair
- **Entry:** a gate or QA returned FAIL, or a bounded reprompt failed.
- **Actions:** add the **structured failure** to *current failures* (P9); re-enter Coding with only
  that failure as new context (not the whole history). This is the
  [17_RepairLoop](17_RepairLoop.md) Observe→Analyse→Repair→Verify cycle in miniature.
- **Exit:** back to Coding → Building → QA.
- **Retry:** the Coding↔Building↔QA loop is bounded by `maxAttempts` (config, default 3). The Decision
  Engine's stuck-detector may stop early ([20_DecisionEngine](20_DecisionEngine.md)).
- **Timeout/exhaustion:** attempts exhausted → Failed.
- **Recovery:** attempt counter is persisted in the Execution-State record; a crash mid-repair resumes
  at the right attempt number.

### ReadyToMerge
- **Entry:** QA PASS (with any recorded deferrals).
- **Actions:** `Acquire(merge:<repo>)` ([15_LockManager](15_LockManager.md)). Wait if another merge
  holds it (short, serialized).
- **Exit:** merge lock held → Merged.
- **Timeout:** merge-lock wait timeout → stay queued (merges are quick); repeated failure → Blocked.
- **Recovery:** no code state changes here; safe to retry.

### Merged
- **Entry:** merge lock held.
- **Actions:** refresh `development`; if branch still applies cleanly, `merge --no-ff` and delete the
  branch + worktree; push `development` ([07_Git](07_Git.md)). If conflict → refresh + re-verify;
  only a semantic conflict invokes the **Integrator** worker (the model *recommends*, the Decision
  Engine *decides*, Go *executes*).
- **Exit:** merge landed → Done. Unresolved conflict after K tries → Blocked/Failed.
- **Retry:** refresh-and-retry up to K ([07_Git](07_Git.md)).
- **Timeout:** merge op timeout → release merge lock, retry once, else Blocked.
- **Recovery:** because the merge is one atomic `--no-ff` under the merge lock, a crash leaves either
  "not merged" (retry) or "merged" (proceed) — never half (I-3, NFR-8).

### Done
- **Entry:** merge landed.
- **Actions:** transition Jira to done, post result comment (changes, checks + evidence, deferrals +
  follow-up keys, merge ref), worklog; append the Assignment's history to the Knowledge Brain
  (learned facts) and close its Execution-State record; `ReleaseAll(assignmentID)` in reverse order.
- **Exit:** Assignment → Idle.
- **Recovery:** each write is idempotent ([08_Jira](08_Jira.md)); a crash mid-close re-runs the
  remaining writes from `dirty_writes` ([12_Database](12_Database.md)).

### Blocked
- **Entry:** waiting on a human or an unavailable resource that is *required* (not deferrable), or a
  repeatedly-unresolvable merge.
- **Actions:** label the issue (`needs-human` / keep place), notify the owner
  ([09_Dashboard](09_Dashboard.md)), and — to keep the KPI flowing — **release the Assignment** so it
  can serve other issues (P7, P8). The issue keeps its position; it re-enters Waiting when unblocked.
- **Exit:** human resolves / resource appears → re-queued to Waiting.
- **Timeout:** none (a human owns it), but a periodic reminder can re-notify.

### Cancelled
- **Entry:** owner **stop-issue** ([09_Dashboard](09_Dashboard.md)).
- **Actions:** cooperative cancel ([15_LockManager](15_LockManager.md)): bump fence, kill worker,
  clean worktree, `ReleaseAll`, return Jira issue to prior stable status.
- **Exit:** Assignment → Idle. Idempotent.

### Failed
- **Entry:** attempts exhausted or an unrecoverable error.
- **Actions:** same as Blocked but classified as a failure: `needs-human` label + the failure detail
  + evidence; notify; `ReleaseAll`; Assignment → Idle. Becomes NEEDS_HUMAN in Jira.
- **Exit:** owner re-queues after fixing/splitting the issue ([17_RepairLoop](17_RepairLoop.md) and
  [20_DecisionEngine](20_DecisionEngine.md) on when to split/defer/abort).

## Retry, timeout & restart — global rules

- **Bounded loops only.** Coding↔Building↔QA ≤ `maxAttempts`; reprompt ≤ 1 per worker step; merge
  refresh ≤ K. No unbounded loop anywhere (P6, [20_DecisionEngine](20_DecisionEngine.md)).
- **Every reasoning step has a wall-clock + token budget.** Exceeding it kills the worker and counts
  as an attempt (NFR-2).
- **Worker restart ≠ Assignment restart.** Killing/restarting a worker is cheap and stateless. The
  Assignment's durable state (Execution-State record, branch commits, locks) survives, so restarting a
  worker never loses progress (P4).
- **Assignment restart (crash/reboot):** the reconciler ([15_LockManager](15_LockManager.md)) rebuilds
  Assignments from the Execution-State store, reaps stale locks, cleans orphans, and resumes each issue
  at its last stable state. Determinism makes resume safe.

## Disposability guarantee (P4)

At every state, all information needed to continue lives in **durable, non-session** stores: Jira
(work), Git (code + partial commits), the **Knowledge Brain** (plan inputs, decisions, learned failure
patterns, deferral how-tos), and the **Execution State** store (assignment record, attempts, metrics,
locks, deferral tracking). A worker can be killed at any instant with zero knowledge loss. This is the
property that lets V2 use cheap, small, throwaway reasoning steps instead of expensive long-lived
agents.

## Invariants (Assignment-specific, reinforcing 03/07/15/20)

- **A-1** An Assignment processes exactly one issue at a time; one issue is processed by exactly one
  Assignment (I-1).
- **A-2** No token is spent outside Planning/Coding/QA/(Integrator-on-conflict).
- **A-3** Every loop is bounded; every reasoning step is budgeted.
- **A-4** Durable progress is never held only in a worker; killing a worker loses nothing (P4).
- **A-5** Blocked/Failed release the Assignment so other issues keep flowing (P7).
- **A-6** Assignment state transitions occur only with the issue lock held (I-6, L-1) and are chosen
  deterministically by the Decision Engine (Law 18).
