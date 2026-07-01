# 00 — Vision

## Purpose

Build the best autonomous software-engineering platform that can be operated from a single laptop,
for **any** kind of project, at **near-zero token cost**, that behaves like the world's best senior
software engineer: it finishes features quickly, keeps the codebase clean, never fights itself,
merges continuously, and asks a human only when a human is genuinely required.

ClaudWorker V2 turns a backlog of **Jira issues** into **merged, production-ready code** with
minimal human involvement and minimal spend, by combining:

- **Deterministic Go** for everything a program can do (the 95%).
- **Claude reasoning** for the small part that genuinely needs judgment (the 5%).

## What it must eventually support

One engine, many project types — selected purely by **configuration + a plugin**, never by
changing engine source:

- General software projects
- Flutter apps
- .NET services / APIs
- Websites / frontends
- AI / ML projects
- Firmware & ESP32
- PCB projects
- 3D design
- Hardware & manufacturing

## The KPI

> **The single primary KPI is: the number of Jira issues taken to *production-ready, merged, and
> closed*.**

Explicitly **not** the KPI:

- Claude token usage or spend (should be *minimized*, not maximized).
- Worker utilization or "keeping agents busy".
- Number of running agents.
- Lines of code, commits, or branches.

Everything in this architecture optimizes for *completed features per dollar per hour*, where the
dollar is dominated by "did we avoid spending tokens on things Go can do".

## Core principles (binding)

These ten principles are mandatory and govern every design decision. Any later decision that
violates one is a defect.

1. **Local first.** The engine runs on the laptop. A VPS may be *added* later but must **never be
   required**. No cloud service is a hard dependency.
2. **Jira first.** Jira is the single source of truth for *work*. No local task list, no duplicated
   task system, no second backlog.
3. **Git first.** GitHub is the single source of truth for *code*. Every worker always starts from
   the newest `development`. Workers never continue on stale code.
4. **Project Brain.** No AI session is the source of truth for *knowledge*. The Project Brain is.
   Workers are disposable; knowledge is permanent.
5. **Zero-token deterministic operations.** Every deterministic task is implemented in Go. Claude
   is never used to do what Go can do (git, Jira API, builds, flutter/xcode/adb/simctl, screenshots,
   image diff, OCR, DRC/ERC, STL export, log parsing, indexing, dependency graphs, scheduling,
   polling …). AI is used **only** for reasoning.
6. **Simplicity.** The architecture is as small as possible. No unnecessary services, agents,
   prompts, or context. When in doubt, remove it.
7. **Feature completion.** Optimize for completed production-ready Jira issues — see KPI above.
8. **No conflicts.** Workers never fight each other. Enforced by locking, ownership, tiny branches,
   and immediate merge.
9. **Small context.** Every prompt contains only: task, acceptance criteria, relevant files,
   architecture summary, recent decisions, current failures. Never the whole repository.
10. **Portable.** The engine works for any project; only configuration changes. No source edits per
    project.

## Design tie-breakers

When two designs both satisfy the principles, prefer, in order:

1. The one that spends **fewer tokens**.
2. The one that is **simpler** (fewer moving parts, less prompt, less state).
3. The one that is **more deterministic** (Go over AI, data over prose).
4. The one that **merges sooner** (smaller unit of work).
5. The one that is **more portable** (config over code).

## Non-goals

- Not a chat product; there is no conversational assistant surface for end users.
- Not a cloud SaaS; there is no requirement for hosted infrastructure.
- Not a replacement for a human owner. The owner may commit at any time; the engine **synchronizes
  with**, and never overwrites, human work.
- Not a maximizer of agent activity. Idle is fine; an idle engine that has completed the backlog is
  a *success*, not a waste.
- Not MyOTGO-specific. MyOTGO is simply the first project configuration.

## The bet

The V1 fleet proved that many long-lived role agents on a shared subscription produce conflicts,
stale-code work, token waste, and coordination overhead. V2's bet is the inverse: **a mostly
deterministic Go engine with a handful of disposable, tightly-scoped reasoning workers** will
finish more issues, more reliably, for far less money, and be dramatically easier to maintain.

## What "done" means for V2 itself

V2 is finished when: (a) all 15 architecture docs are frozen and consistent; (b) the engine can
take a real Jira issue from *ready* to *closed/merged* on the MyOTGO project without human help for
the common case; (c) it demonstrably spends fewer tokens per completed issue than V1; and (d) a
second, non-MyOTGO project can be onboarded by writing only a config + (if needed) a plugin.
