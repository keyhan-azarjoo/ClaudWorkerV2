# ClaudWorker V2

A **local-first, Jira-first, Git-first** autonomous software-engineering platform.

ClaudWorker V2 is a complete greenfield redesign of ClaudWorker V1. It runs on a laptop, uses
**Jira as the single source of truth** for work, **Git/GitHub as the single source of truth** for
code, and a persistent **Project Brain** as the single source of truth for knowledge. Claude
workers are **disposable** and used **only for reasoning**. Every deterministic operation (git,
Jira API, builds, screenshots, image diff, OCR, DRC/ERC, STL export, log parsing, scheduling …)
is implemented in **Go** so no tokens are ever spent on work a program can do.

> 🧊 **Architecture FROZEN at v2.1.0** (see [SPEC_VERSION.md](SPEC_VERSION.md)). The architecture in
> [`docs/`](docs/) is the source of truth. Implementation targets exactly this version and follows the
> [Implementation Roadmap](docs/21_ImplementationRoadmap.md) strictly in order. Changes are made only
> via an approved [Architecture Change Proposal](ACP_TEMPLATE.md); see
> [ACP-0001](docs/acp/ACP-0001-assignment-engine-before-database.md) (Assignment Engine before DB).

## Status

| Phase | State |
|---|---|
| Repo created (`myotgo/ClaudWorkerV2`, private) | ✅ |
| Architecture docs (`docs/00`–`docs/22` + review) | ✅ complete |
| Architecture frozen | 🧊 **v2.1.0** (2026-07-02; ACP-0001) |
| Implementation | ▶️ **S0–S3 complete** (foundations + Git/Jira toolbelt + Assignment Engine + **State Store**) — S4 (Knowledge Brain) next. Reports: [S2](docs/reports/S2_REPORT.md), [S3 persistence](docs/reports/PERSISTENCE_REVIEW_S3.md). |

### Build & run (S0–S1)

```
go build -o bin/cwv2 ./cmd/cwv2
./bin/cwv2 version
./bin/cwv2 doctor --config configs/cwv2.example.yaml

# Deterministic Git toolbelt (JSON output) — workers call these, never git directly
./bin/cwv2 git commit       --repo <path> --message "msg"
./bin/cwv2 git branch-create --repo <path> --name agent/KEY-slug --base development
./bin/cwv2 git worktree-add  --repo <path> --path <wt> --branch agent/KEY-slug --base development
./bin/cwv2 git merge         --repo <path> --branch agent/KEY-slug --message "merge"
./bin/cwv2 git help

# Deterministic Jira toolbelt (JSON output) — needs base_url + auth secrets in config
./bin/cwv2 jira health      --config configs/cwv2.example.yaml
./bin/cwv2 jira search      --config configs/cwv2.example.yaml --jql 'project = SCRUM'
./bin/cwv2 jira help

go test ./...
```

Every subsystem spends **zero tokens**. Per the [roadmap](docs/21_ImplementationRoadmap.md), each must
pass unit + integration + architecture-compliance + performance gates before the next; S0 and S1 have.

## Relationship to V1

V1 (`myotgo/claudworker_myotgo`, local `Claud_worker_agent/`) **remains the production system**
until V2 is finished and validated. **V2 never modifies V1.** No V1 code is copied in; V2 is
designed from first principles and only learns from V1's operational lessons.

## The one-paragraph architecture

A single Go binary (`cwv2`) runs as a local daemon. It **polls Jira** for issues that are ready to
work, **claims** one at a time under a lock, and drives it through a fixed state machine:
**Manager → Developer → QA → (loop on failure) → Integrator → Done**. Each stage that needs
*reasoning* spawns a short-lived `claude -p` worker with a **small, curated prompt** (task,
acceptance criteria, relevant files, architecture summary, recent decisions, current failures) and
a set of **deterministic tools** it can call back into the Go engine to run. Everything the worker
learns is written back to the **Project Brain**. Code always starts from the newest `development`
branch, work happens on **tiny short-lived branches** that are **merged immediately** after
automated verification, and the branch is deleted. Unavailable tests (hardware, human QA) never
block a merge — they are **deferred** with follow-up issues.

## Documents

Read them in order. Each is complete and they are mutually consistent.

| # | Doc | What it defines |
|---|---|---|
| 00 | [Vision](docs/00_Vision.md) | Purpose, principles, non-goals, KPI |
| 01 | [Requirements](docs/01_Requirements.md) | Functional + non-functional requirements |
| 02 | [Architecture](docs/02_Architecture.md) | Components, boundaries, data flow, tech choices |
| 03 | [Workflow](docs/03_Workflow.md) | Issue lifecycle state machine |
| 04 | [Project Brain](docs/04_ProjectBrain.md) | Knowledge Brain (persistent) + Execution State (temporary) |
| 05 | [Workers](docs/05_Workers.md) | The 4 worker types + prompt contracts |
| 06 | [QA](docs/06_QA.md) | Human-like + headless QA, deferral |
| 07 | [Git](docs/07_Git.md) | Branch/merge/lock model |
| 08 | [Jira](docs/08_Jira.md) | Jira as source of truth, field mapping |
| 09 | [Dashboard](docs/09_Dashboard.md) | Local control panel |
| 10 | [Hardware](docs/10_Hardware.md) | Firmware/ESP32/PCB/3D pipelines |
| 11 | [Plugins](docs/11_Plugins.md) | Per-project-type capability packs |
| 12 | [Database](docs/12_Database.md) | Engine state schema |
| 13 | [Config](docs/13_Config.md) | The only project-specific surface |
| 14 | [Deployment](docs/14_Deployment.md) | Install, run, operate, portability |
| 15 | [Lock Manager](docs/15_LockManager.md) | Deterministic locking: scopes, fencing, deadlock/crash recovery |
| 16 | [Worker State Machine](docs/16_WorkerStateMachine.md) | Assignment lifecycle (Idle→…→Done/Blocked/Cancelled/Failed) |
| 17 | [Repair Loop](docs/17_RepairLoop.md) | The universal Observe→Analyse→Repair→Verify loop, per domain |
| 18 | [Plugin Contract](docs/18_PluginContract.md) | Uniform capability interface every plugin implements |
| 19 | [System Laws](docs/19_SystemLaws.md) | The 19 immutable laws the whole system obeys (incl. restart safety) |
| 20 | [Decision Engine](docs/20_DecisionEngine.md) | Deterministic decisions: retry/repair/escalate/split/defer/merge/abort |
| 21 | [Implementation Roadmap](docs/21_ImplementationRoadmap.md) | Construction manual: subsystem order, milestones, rollback |
| 22 | [Migration](docs/22_Migration.md) | First-phase onboarding of any existing project |
| — | [Architecture Review](docs/ARCHITECTURE_REVIEW.md) | Pre-freeze review + freeze recommendation |

## License

See [LICENSE](LICENSE).
