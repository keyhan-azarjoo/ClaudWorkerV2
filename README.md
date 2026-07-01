# ClaudWorker V2

A **local-first, Jira-first, Git-first** autonomous software-engineering platform.

ClaudWorker V2 is a complete greenfield redesign of ClaudWorker V1. It runs on a laptop, uses
**Jira as the single source of truth** for work, **Git/GitHub as the single source of truth** for
code, and a persistent **Project Brain** as the single source of truth for knowledge. Claude
workers are **disposable** and used **only for reasoning**. Every deterministic operation (git,
Jira API, builds, screenshots, image diff, OCR, DRC/ERC, STL export, log parsing, scheduling …)
is implemented in **Go** so no tokens are ever spent on work a program can do.

> ⚠️ **This repository is design-first.** Implementation does **not** begin until the architecture
> in [`docs/`](docs/) is frozen and internally consistent. The architecture is the source of truth.

## Status

| Phase | State |
|---|---|
| Repo created (`myotgo/ClaudWorkerV2`, private) | ✅ |
| Architecture docs (`docs/00`–`docs/14`) | 🏗️ in progress |
| Architecture frozen | ⛔ not yet |
| Implementation | ⛔ blocked on freeze |

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
| 04 | [Project Brain](docs/04_ProjectBrain.md) | Durable knowledge store |
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

## License

See [LICENSE](LICENSE).
