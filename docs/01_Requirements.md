# 01 — Requirements

Requirements are numbered and testable. `FR` = functional, `NFR` = non-functional. Each maps to
one or more principles from [00_Vision](00_Vision.md) (P1–P10).

## Actors

- **Owner** — the human developer. May commit to any repo at any time. Sets the backlog in Jira.
- **Engine** — the local Go daemon (`cwv2`). Orchestrates everything.
- **Worker** — a short-lived `claude -p` process spawned by the Engine for one reasoning step.
- **Jira** — external system of record for work.
- **GitHub** — external system of record for code.
- **Project Brain** — local durable knowledge store owned by the Engine.

## Functional requirements

### Task intake (Jira-first — P2)

- **FR-1** The Engine polls Jira on an interval for issues in a configured "ready" state assigned to
  the Engine (or in a configured queue), ordered by a deterministic priority.
- **FR-2** The Engine maintains **no** independent backlog. All work state that must survive is
  reflected back to Jira (status, comments, worklog). Local DB is a **cache/coordination** layer
  only (see [12_Database](12_Database.md)), never an authority on *what work exists*.
- **FR-3** The Engine claims **one issue at a time per worker** and records ownership so no two
  workers ever touch the same issue (P8).
- **FR-4** The Engine transitions Jira status as the issue moves through the workflow
  (see [03_Workflow](03_Workflow.md)) and posts a concise result comment on completion.

### Code handling (Git-first — P3)

- **FR-5** Before any work, the Engine fetches and fast-forwards `development`; every worker branch
  is created **from the newest `development`** (P3).
- **FR-6** Work happens on **tiny, short-lived branches**; after automated verification the branch is
  merged to `development` and **deleted** (P8, [07_Git](07_Git.md)).
- **FR-7** The Engine detects owner commits and **re-synchronizes** in-flight work (rebase/refresh)
  without ever overwriting owner work (P3).
- **FR-8** Merges are serialized through a single **Integrator** path; a merge that would conflict is
  resolved by refreshing from `development` and re-running verification, never by force.

### Reasoning (workers — P4, P5, P9)

- **FR-9** Reasoning steps are performed by ephemeral workers spawned as `claude -p` with
  `--output-format json` and a **minimal curated prompt** (P9). No worker receives the whole repo.
- **FR-10** Workers are one of exactly four types: **Manager, Developer, QA, Integrator**
  (see [05_Workers](05_Workers.md)). No other long-lived agents exist.
- **FR-11** Every worker returns **structured output** (JSON conforming to a declared schema); the
  Engine parses it deterministically. Free-form prose is only for human-facing comments.
- **FR-12** Anything a worker discovers that must persist (decisions, file facts, failures,
  deferrals) is written to the **Project Brain** via deterministic tools, not left in a session.

### Deterministic operations (P5)

- **FR-13** The Engine provides a **deterministic toolbelt** in Go covering at minimum: git ops,
  Jira API, branch/merge/rebase, build, flutter/xcode/gradle commands, adb, simctl, screenshot
  capture, image comparison, OCR, DRC/ERC, STL export, log parsing, file indexing, dependency graph,
  scheduling, polling.
- **FR-14** Workers invoke deterministic operations **only** by calling these tools; they never
  re-implement them in reasoning. A tool call spends **zero** model tokens on the operation itself.
- **FR-15** Each tool has a stable typed interface, is independently runnable from the CLI
  (`cwv2 tool <name> …`) for testing, and returns structured results.

### QA (P7, human-like)

- **FR-16** QA prefers **human-like, visual** verification: launch the app, navigate, click, fill
  forms, pair devices, screenshot, compare, read logs, repeat until the acceptance criteria pass
  (see [06_QA](06_QA.md)).
- **FR-17** Headless/automated tests are used only when visual testing is impossible.
- **FR-18** A test that cannot run (hardware absent, no device, human-only) **must not block** the
  issue. It is marked **Deferred**, a follow-up issue is created, and the current issue can still
  merge (P7).
- **FR-19** QA failures return structured, actionable failures that feed the next Developer step
  (bounded retry loop).

### Knowledge (Project Brain — P4)

- **FR-20** The Project Brain persistently stores: architecture summary, decisions/ADRs, a file &
  symbol index, dependency graph, per-issue history, known failures, and deferrals
  (see [04_ProjectBrain](04_ProjectBrain.md)).
- **FR-21** The Brain is **deterministically maintained** by Go (indexers, parsers) and *augmented*
  by workers; it is rebuildable from the repo + Jira at any time.
- **FR-22** Prompt assembly reads **from the Brain**, selecting only the slice relevant to the
  current issue (P9).

### Control & observability

- **FR-23** A **local dashboard** shows live per-issue progress, worker activity, token/cost, usage
  guard state, blockers/"needs-human", and controls (run/pause/resume/stop) — see
  [09_Dashboard](09_Dashboard.md). Bound to localhost by default.
- **FR-24** All actions are logged locally with enough detail to reconstruct a run.
- **FR-25** When the Engine is blocked needing a human, it flags the issue (`needs-human`) and can
  notify the owner (e.g. Telegram) without blocking other issues.

### Portability (P10)

- **FR-26** Onboarding a new project requires **only** a config file (+ optionally a plugin); no
  engine source changes ([13_Config](13_Config.md), [11_Plugins](11_Plugins.md)).
- **FR-27** Project-type-specific behavior (Flutter, .NET, web, ESP32, PCB, 3D) lives entirely in
  **plugins**; the engine core is project-agnostic.

## Non-functional requirements

- **NFR-1 (Local-first, P1)** The Engine runs fully on macOS on the laptop with no required remote
  service beyond Jira/GitHub/Anthropic APIs. No VPS required, ever.
- **NFR-2 (Cost, P5/P7)** Token spend per completed issue must be materially lower than V1.
  Deterministic work costs zero tokens. The engine honors a **usage guard**: pause spawning at a
  configured plan-usage threshold, resume below it; never force pay-as-you-go/override.
- **NFR-3 (Never spend money without consent)** No action incurs new billable spend unless the owner
  has consented. Free-tier/flat-subscription paths are preferred.
- **NFR-4 (Simplicity)** The whole engine is a **single Go binary** plus a config; minimal external
  runtime deps (git, the `claude` CLI, and per-plugin toolchains that are only needed when that
  plugin is used).
- **NFR-5 (No conflicts, P8)** Under concurrency, the system guarantees no two workers modify the
  same issue, branch, or working tree. Verified by the locking model in [07_Git](07_Git.md).
- **NFR-6 (Safety)** Secrets are loaded from keychain/vault at runtime, never logged, never
  committed. Destructive git ops (`reset --hard`, `stash`, cross-branch checkout) are forbidden on
  shared trees; each worker uses its own worktree.
- **NFR-7 (Determinism/reproducibility)** Given the same Jira issue + repo state, the deterministic
  pipeline produces the same branches, builds, and checks. The Brain is rebuildable.
- **NFR-8 (Recoverability)** A crash mid-issue leaves the system in a recoverable state: locks
  expire/roll back, partial branches are detectable and cleanable, no half-merges to `development`.
- **NFR-9 (Observability)** Every run is traceable end-to-end from Jira issue → branch → checks →
  merge via logs + dashboard.
- **NFR-10 (Portability, P10)** No hard-coded MyOTGO assumptions in engine core; all such details
  live in config/plugins.
- **NFR-11 (Owner coexistence)** The owner can commit at any time; the engine detects and adapts,
  never overwrites, and never blocks the owner.

## Explicit constraints (from owner rules)

- **C-1** V2 must not modify V1 (`Claud_worker_agent/`, `myotgo/claudworker_myotgo`). Separate repo,
  separate state.
- **C-2** Commit/branch/PR identity is always `keyhanazarjoo <keyhanazarjoo@gmail.com>` — no
  `Co-Authored-By: Claude`, no org author. Remote ops via GitHub user `keyhan-azarjoo`.
- **C-3** Never commit directly to `development`/`staging`/`main`; `development` receives code only
  via `merge --no-ff` from a worker branch; staging/main only via promotion.
- **C-4** Prefer real devices/human-like QA over simulators where the owner's rules require it.
- **C-5** Keep everything local; do not send work to cloud Claude routines or claude.ai.
- **C-6** Everything for V2 (repo clone, engine home, brain, logs, artifacts) lives on the external
  SSD (`/Volumes/Extreme SSD`).

## Out of scope (v2.0)

- Multi-tenant / multi-user hosting of the engine.
- A remote web UI exposed to the public internet (a private tunnel may be added later).
- Auto-provisioning cloud build infrastructure (Mac Mini is used on demand, not required).
