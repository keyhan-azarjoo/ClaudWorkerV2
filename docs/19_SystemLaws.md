# 19 — System Laws

The immutable laws of ClaudWorker V2. Where the other documents describe *how* the system works,
these state *what must never change*. Every design decision, plugin, and future extension must comply.
A change that violates a law is a defect, not a trade-off.

Each law maps to the principles in [00_Vision](00_Vision.md) (P1–P10) and is stated with **why**,
**what it prevents**, **trade-offs**, and a **failure example** (what goes wrong if broken — several
drawn from real V1 incidents).

---

## Law 1 — Jira is the only source of tasks
**(P2)** No local backlog, no second task list, no work that isn't a Jira issue.

- **Why:** one authority for "what to do" that humans and the engine share.
- **Prevents:** drift between two backlogs; the engine inventing work; double-booking with the owner.
- **Trade-offs:** a Jira outage pauses *new* claims (mitigated by cache + resync,
  [08_Jira](08_Jira.md)); everything must be expressible as an issue.
- **Failure example:** a local queue diverges from Jira → the engine "completes" work nobody wanted
  while real priorities starve; the KPI (closed *Jira* issues) becomes unmeasurable.

## Law 2 — GitHub is the only source of code
**(P3)** All code lives in Git; the newest `development` is the only valid starting point.

- **Why:** one authority for "what the code is"; reproducible, auditable history.
- **Prevents:** work on stale/local-only code; lost changes; unshareable state.
- **Trade-offs:** every change must round-trip through branches/merges (kept cheap by tiny branches,
  Law 8).
- **Failure example:** a worker builds on a week-old checkout → its "fix" reverts newer work on merge
  (a real V1 stale-tree hazard).

## Law 3 — The Knowledge Brain is the only persistent memory
**(P4)** Durable knowledge lives in the **Knowledge Brain** (persistent), never in an AI session.
Volatile run data lives in the separate **Execution State** store and is never confused with knowledge
([04_ProjectBrain](04_ProjectBrain.md)).

- **Why:** sessions are ephemeral; knowledge must outlive them and be rebuildable.
- **Prevents:** knowledge dying with a killed worker; re-deriving the same facts repeatedly (token
  waste).
- **Trade-offs:** knowledge must be explicitly written back via tools ([04_ProjectBrain](04_ProjectBrain.md)).
- **Failure example:** a decision made in one agent's chat is invisible to the next → the next agent
  re-litigates it and picks the opposite, causing churn.

## Law 4 — Workers are disposable
**(P4)** A worker is a short-lived reasoning step holding no long-term memory; killing one at any
instant loses nothing.

- **Why:** enables cheap, small, parallel reasoning instead of expensive long-lived agents.
- **Prevents:** context bloat; "precious" agents whose loss is costly; hidden state.
- **Trade-offs:** all progress must be externalized (Git/Brain/run record) before it counts
  ([16_WorkerStateMachine](16_WorkerStateMachine.md) A-4).
- **Failure example:** a long-lived agent accumulates 200 KB of context, gets slower and pricier each
  turn, then crashes and takes its unsaved reasoning with it.

## Law 5 — AI performs reasoning only
**(P5)** Models are used solely for judgment (plan, code, diagnose, decide). Never for mechanical
work.

- **Why:** tokens are spent only where a program genuinely can't decide.
- **Prevents:** paying a model to do what Go does for free.
- **Trade-offs:** requires building deterministic tools up front (Law 6).
- **Failure example:** asking a model to "run the tests and tell me git status" burns tokens and
  returns a hallucinated result; the deterministic tool would be free and correct.

## Law 6 — Deterministic work is always implemented in Go
**(P5)** If it can be a program, it is a Go tool — git, Jira, builds, screenshots, image diff, OCR,
DRC/ERC, STL export, log parsing, indexing, scheduling, locking, merging.

- **Why:** correctness, reproducibility, and zero token cost for the 95%.
- **Prevents:** non-determinism and cost in the mechanical majority of the work.
- **Trade-offs:** more Go to write and maintain (paid back immediately in cost + reliability).
- **Failure example:** a "creepage looks fine" judgment by a model instead of a measured
  `kicad-cli`/DRU check ships an unsafe mains board — both wrong and dangerous
  ([10_Hardware](10_Hardware.md)).

## Law 7 — Unavailable tests never block a merge
**(P7)** A check that can't run for an environmental reason (no hardware/device, visual impossible,
human-only) is **deferred** with a follow-up, never a blocker.

- **Why:** keep the KPI (completed issues) flowing; don't hold branches hostage to absent hardware.
- **Prevents:** long-lived open branches; stalled throughput; pressure to fake a pass.
- **Trade-offs:** some verification is genuinely deferred; deferrals must be tracked and honestly
  labeled (never a green check, [06_QA](06_QA.md)).
- **Failure example:** an issue sits open for days because the ESP32 board is unplugged, while its
  code was correct and could have merged with the on-hardware check deferred.

## Law 8 — Tiny branches only
**(P8)** Work happens on small, short-lived `agent/*` branches off `development`.

- **Why:** small diffs merge fast and rarely conflict; failures are isolated.
- **Prevents:** long-lived divergent branches; large painful merges; entangled changes.
- **Trade-offs:** large tasks must be split ([17_RepairLoop](17_RepairLoop.md)).
- **Failure example:** a multi-day mega-branch conflicts with a dozen merges and an owner commit,
  costing more to reconcile than it did to write.

## Law 9 — Immediate merge after automated verification
**(P8)** Once the runnable gates pass, merge `--no-ff` into `development` right away and delete the
branch.

- **Why:** minimizes divergence; makes each success immediately available to the next issue.
- **Prevents:** merge backlogs; stale branches; "review later" rot.
- **Trade-offs:** requires trustworthy automated gates ([06_QA](06_QA.md), [18](18_PluginContract.md))
  and serialized merges (Law 11 corollary).
- **Failure example:** ten verified branches wait to merge, drift from `development`, and each now
  needs re-verification — throughput collapses.

## Law 10 — One worker owns one resource
**(P8)** Exclusive ownership of an issue, a working tree/branch, a device, and the merge slot is
enforced by the Lock Manager. No sharing of a mutable resource.

- **Why:** the foundation of zero-conflict parallelism.
- **Prevents:** two workers corrupting one tree; two claims on one issue; two runs driving one board.
- **Trade-offs:** some serialization (the merge lock serializes integration per repo). V1 enforces
  this with three hard locks — issue, device, merge ([15_LockManager](15_LockManager.md)); finer
  advisory locks are deferred to Future expansion.
- **Failure example:** two agents `git checkout` different branches in the *same* shared clone and
  overwrite each other's working tree (an actual V1 incident this law exists to kill).

## Law 11 — Every merge updates `development` immediately and only via `--no-ff`
**(P3/P8)** `development` advances exclusively by non-fast-forward merges of verified branches, under
the merge lock; never by a direct commit; `staging`/`main` only by promotion.

- **Why:** a single, linear, auditable integration trunk; no half-merges (atomic under the lock).
- **Prevents:** direct commits to protected branches; concurrent racing merges; lost merge context.
- **Trade-offs:** merges are serialized per repo (short holds, [15_LockManager](15_LockManager.md) §9).
- **Failure example:** a stray direct commit on `development`/`staging`/`main` bypasses verification
  and has to be reconciled back out (why the branch guard blocks it).

## Law 12 — Feature completion is prioritised over worker utilisation
**(P7)** The KPI is closed, production-ready Jira issues — not busy agents.

- **Why:** aligns the whole system with delivered value, not activity.
- **Prevents:** make-work; spawning agents to look busy; optimizing the wrong metric.
- **Trade-offs:** idle slots are acceptable and expected when the backlog is clear.
- **Failure example:** a scheduler tuned for "max concurrent agents" spawns redundant workers that
  conflict and burn tokens while closing *fewer* issues.

## Law 13 — Token efficiency is a first-class design goal
**(P5/P7)** Minimizing tokens-per-completed-issue is a primary objective, not an afterthought.

- **Why:** cost is dominated by avoidable token spend; cheap operation is a feature.
- **Prevents:** whole-repo prompts; redundant reasoning; deterministic work done by a model.
- **Trade-offs:** requires disciplined context assembly (P9) and up-front tool building (Law 6).
- **Failure example:** sending an entire repository into a prompt "to be safe" turns a $0.02 issue
  into a $2 issue and often *lowers* quality by drowning the signal.

## Law 14 — Small, curated context only
**(P9)** Every prompt contains only: task, acceptance criteria, relevant files, architecture summary,
recent decisions, current failures. Never the whole repository.

- **Why:** smaller context is cheaper, faster, and usually *more* accurate.
- **Prevents:** token waste; context dilution; leaking unrelated code into reasoning.
- **Trade-offs:** needs a good Brain (index + dependency graph) to pick the right slice
  ([04_ProjectBrain](04_ProjectBrain.md)).
- **Failure example:** omitting the "current failures" slice makes a Developer repeat a known
  dead-end; including the whole repo makes it miss the one file that mattered.

## Law 15 — The owner may commit at any time
**(P3/owner)** Human commits are authoritative; the engine synchronizes with them and never
overwrites them.

- **Why:** the owner is a first-class contributor working alongside the engine.
- **Prevents:** the engine clobbering human work; force-pushes; history rewrites on shared branches.
- **Trade-offs:** in-flight issues may need a refresh/rebase when `development` moves
  ([07_Git](07_Git.md), [16_WorkerStateMachine](16_WorkerStateMachine.md)).
- **Failure example:** the engine force-pushes `development` and erases an owner commit made minutes
  earlier.

## Law 16 — The architecture remains portable
**(P10)** The engine core is project-agnostic; new projects/types are added by config + plugins only,
never by editing core.

- **Why:** one engine serves every current and future project (MyOTGO is "just a config").
- **Prevents:** MyOTGO-specific logic leaking into core; a fork per project.
- **Trade-offs:** capabilities must fit the plugin contract ([18_PluginContract](18_PluginContract.md)).
- **Failure example:** an `if project == "myotgo"` branch in the Orchestrator makes project #2 require
  a code change — the portability promise is broken.

## Law 17 — The architecture remains simple
**(P6)** Prefer the smallest design that satisfies the laws. Remove before adding. No unnecessary
services, agents, prompts, or context.

- **Why:** simplicity is reliability and maintainability; complexity is where bugs and cost hide.
- **Prevents:** service sprawl; role-agent explosion (the V1 failure mode); speculative machinery.
- **Trade-offs:** occasionally a simple design does a little less; that's usually the right call.
- **Failure example:** twelve long-lived role agents coordinating over Confluence produce more
  coordination overhead and conflicts than four disposable workers + Go (the reason V2 exists).

## Law 18 — Determinism owns the critical sections; AI is never in the loop of a lock, merge, or gate
**(P5/P8)** Locking, merging, and PASS/FAIL gate decisions are pure Go. A model never decides a lock,
performs a merge decision mechanically, or renders a gate result.

- **Why:** safety and correctness of the invariants must not depend on a probabilistic output.
- **Prevents:** a hallucinated "it's fine" corrupting a resource or shipping a bad merge/board.
- **Trade-offs:** the model may *recommend* (e.g. how to resolve a semantic conflict), but Go
  *decides and executes* ([15_LockManager](15_LockManager.md), [07_Git](07_Git.md)).
- **Failure example:** letting a model "grant the lock" or "call the DRC passed" reintroduces exactly
  the non-determinism these laws exist to remove.

## Law 19 — Restart safety is a core invariant
**(P1/NFR-8)** The system must survive crashes and reboots. After any restart the engine **restores
Assignments and Execution State, continues unfinished work, never restarts completed work, and never
loses progress.**

- **Why:** a local, long-running, unattended engine *will* be killed (crash, power loss, reboot,
  laptop sleep); correctness must not depend on staying up.
- **Prevents:** duplicated/redone work after a restart; lost in-flight progress; half-merges; orphaned
  locks/worktrees; re-processing an already-closed issue.
- **How it holds:** all durable state lives outside any process — Jira (work), Git (code + partial
  commits), the Knowledge Brain, and the Execution State (`state.db`) on the SSD, fsynced. On startup
  the reconciler ([15_LockManager](15_LockManager.md) §9) reaps stale locks, cleans orphans, rebuilds
  Assignments from `state.db`, reconciles against Jira/Git, and resumes each at its last **stable**
  state ([16_WorkerStateMachine](16_WorkerStateMachine.md)); completed work (merged + closed) is never
  re-entered. Merges are atomic under the merge lock, so a crash never leaves a half-merge (I-3).
- **Trade-offs:** every state transition must externalize its progress before it counts (workers stay
  disposable, A-4); the engine does a bit more bookkeeping in exchange for crash-proof resume.
- **Failure example:** an engine that held Assignment state only in memory would, after a reboot,
  either redo a merged issue (wasting tokens, risking a double-merge) or lose a half-finished branch —
  exactly what this law forbids.

---

## Compliance

- Every other document in `docs/` must be consistent with these laws; a conflict is resolved in favor
  of the law (or the law is changed deliberately via an ACP — [21_ImplementationRoadmap](21_ImplementationRoadmap.md)).
- New plugins and features are reviewed against Laws 1–19 before merge.
- The Architecture Review ([README](../README.md) → review) checks the spec for law violations,
  contradictions, and drift.
