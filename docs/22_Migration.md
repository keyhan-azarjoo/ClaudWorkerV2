# 22 — Migration

The Migration phase is the **first execution phase of every project** onboarded to ClaudWorker V2. It
prepares the entire environment — code understanding, Jira hygiene, eligibility, Knowledge Brain,
deterministic validations, dashboard — **before the first Assignment starts**. No coding begins until
migration completes and the owner approves the report.

> **Reusable and project-agnostic (P10).** Migration is engine machinery driven by config + plugins;
> nothing in it is MyOTGO-specific. Onboarding project #2 runs the identical pipeline against a
> different `cwv2.yaml`.

> **Mostly deterministic (P5).** Every step that can be a program is Go (repo scan, build/test/lint,
> plugin discovery, dependency graph, device discovery, duplicate detection by similarity). AI is used
> **only** where genuine reasoning is required (e.g. proposing acceptance criteria, judging whether two
> issues are truly duplicates in ambiguous cases, drafting a split). Everything else costs zero tokens.

## Where it sits

Migration is a distinct engine command and phase:

```
cwv2 migrate --project <name>     # runs the pipeline, produces the report, applies nothing
        │
        ▼  owner reviews report → approves
cwv2 migrate --apply --project <name>   # applies ONLY approved, reversible changes (Jira field writes, brain init)
        │
        ▼
cwv2 serve --project <name>       # first Assignment may now start
```

The Scheduler ([03_Workflow](03_Workflow.md)) refuses to admit any issue for a project until its
migration is **approved** (`engine.db.projects.migrated_at` set). This is a hard gate.

## Migration goals

1. Understand the repository/repositories and produce the initial **project profile**.
2. Analyze the entire Jira backlog and produce a **migration report** (no destructive action).
3. Introduce and recommend the **ClaudeWorker eligibility** field per issue.
4. Propose **task normalization** (splits, separations, acceptance criteria, dependencies, links).
5. Classify **deferred work** so environmental blockers don't stall development.
6. Initialize the **Knowledge Brain**.
7. Run all deterministic **build validations**.
8. Create the initial **dashboard state**.
9. Produce a final report for **human review**; apply nothing until approved.

## 1. Repository analysis (deterministic)

Analyze each repo declared in config ([13_Config](13_Config.md)) and produce the **project profile**:

- **Structure** — directory tree, entry points, monorepo vs multi-repo, module boundaries.
- **Languages** — detected by extension/marker scan with proportions.
- **Frameworks** — from manifests (`pubspec.yaml`, `*.csproj`, `package.json`, `platformio.ini`,
  `*.kicad_pcb`, `*.scad`/`*.FCStd`, Dockerfiles, IaC).
- **Build systems** — how each repo builds/tests/lints (commands, flavors).
- **Plugins** — which plugin(s) bind to each repo/component via `Detect` ([18_PlugInContract](18_PlugInContract.md)).
- **CI** — existing CI config discovered (workflows, runners) — recorded, not required.
- **Git strategy** — branches present, default/integration branch (expects `development`), protection,
  hooks, commit-identity conventions ([07_Git](07_Git.md)).

Output: `project_profile.json` (+ a human-readable summary in the report). Zero tokens.

## 2. Jira analysis (deterministic + minimal reasoning)

Analyze **every** issue in the configured project and flag, per issue, any of:

| Flag | How detected |
|---|---|
| **Duplicate** | high title/description similarity (deterministic embedding-lite/shingling); ambiguous pairs confirmed by a single reasoning check |
| **Abandoned** | no activity for > `abandonedDays` (config), not Done |
| **Obsolete** | references removed features/files (cross-checked against the repo index) |
| **Completed-but-open** | linked PR/commit merged or AC met in code, yet status not Done |
| **Very large** | description/scope/estimate exceeds `largeThreshold`; or plan-preview touches many modules |
| **Mixed** | multiple unrelated concerns / multiple repos in one issue |
| **Unclear acceptance criteria** | no AC field/section, or AC not checkable |
| **Missing priority** | priority unset |
| **Missing labels** | required labels (per config) absent |

Output: the **migration report** — a per-issue table of flags + recommendations. **No issue is ever
auto-deleted or auto-closed** — the report *recommends*; the owner decides.

## 3. ClaudeWorker eligibility (new official field)

Migration introduces the official Jira field **`ClaudeWorker`** on every issue, indicating whether
autonomous workers may process it. Recommended value per issue (never blindly `Enable`):

| Value | Meaning | Typical recommendation trigger |
|---|---|---|
| **Enable** | autonomous workers may fully process it | clear AC, single concern, plugin-supported, no blockers |
| **Disable** | never autonomous | policy-sensitive, deploy/promotion, money/secrets, owner-only |
| **Manual Only** | a human drives; engine may assist read-only | needs human judgment/customer/design |
| **Needs Review** | eligibility undecided; owner must set it | ambiguous, large, mixed, unclear AC, newly split |

Rules:
- Default for anything ambiguous/large/mixed/unclear = **Needs Review** (safe default).
- Issues touching deploy/promotion, payments, secrets, or owner-gated actions = **Disable** or
  **Manual Only** (aligns with never-spend / no-solo-deploy owner rules).
- The Scheduler only ever claims issues whose `ClaudeWorker == Enable`
  ([15_LockManager](15_LockManager.md) §3, [08_Jira](08_Jira.md)). Migration **must never** set the
  whole backlog to Enable.
- The field is created (as a select field) and populated only on `--apply`, after approval. Its value
  is cached in `state.db.issues_cache.claudeworker` ([12_Database](12_Database.md)).

## 4. Task normalization (proposals only)

For flagged issues, migration **proposes** (does not apply until approved):

- **Split** very large issues into linked sub-issues ([20_DecisionEngine](20_DecisionEngine.md) SPLIT
  rules; [17_RepairLoop](17_RepairLoop.md)).
- **Separate** mixed issues by concern/repo.
- **Suggest acceptance criteria** where missing/unclear — deterministic per-plugin AC
  (`GenerateAcceptanceCriteria`, [18_PlugInContract](18_PlugInContract.md)) plus a reasoning pass for
  domain-specific AC; written as a **proposed** comment.
- **Detect dependencies** between issues (shared files/modules via the dependency graph; explicit
  "blocked by" language) and **link related issues** in Jira.

All proposals appear in the report with the exact Jira mutation they would make; the owner approves per
item or in bulk. Newly created splits/separations get `ClaudeWorker = Needs Review`.

## 5. Deferred work classification (deterministic)

Detect issues that **cannot currently be completed** for environmental reasons and classify them
**Deferred** instead of letting them block development (Law 7):

| Blocker | Detection |
|---|---|
| **Missing hardware** | required board/device not discoverable (§7 device discovery) |
| **Missing credentials** | required secret/vault entry unresolved for the issue's system |
| **Waiting for customer** | label/status/description signals external dependency |
| **Waiting for design** | design asset/spec referenced but absent |
| **Waiting for owner** | owner action/decision required |

Deferred issues get a recorded deferral (Execution State) + reusable how-to (Knowledge Brain) + a
linked follow-up, and are set to **Manual Only** or **Needs Review** as appropriate — they remain
visible on the dashboard but never stall the ready queue.

## 6. Project Brain initialization (Knowledge Brain)

Migration creates the initial **Knowledge Brain** ([04_ProjectBrain](04_ProjectBrain.md)) — an empty
Execution State is created alongside but holds no history yet. Populate:

- **Architecture** — `knowledge/architecture.md` (short) synthesized from the profile (deterministic
  skeleton; a single reasoning pass may refine the prose).
- **Frameworks** — recorded from detection.
- **Coding Standards** — `knowledge/conventions.md` from linters/formatters/config + any repo
  `CONTRIBUTING`/style files.
- **Repository Structure** — module map + file/symbol index + dependency graph (deterministic).
- **Known Technologies** — languages, build systems, toolchains, devices.
- **Plugins** — the bound plugin set + versions.
- **Project Summary** — a concise overview for the report and for the Architecture-Summary prompt
  slice.

Indexers are deterministic; the only tokens spent here are an optional short refinement of the
architecture prose.

## 7. Build validation (deterministic, zero tokens unless reasoning)

Run every deterministic validation the project supports (via plugins,
[18_PlugInContract](18_PlugInContract.md)), recording results in the report:

- **Build** each repo/module.
- **Tests** (fast suites).
- **Linters** / formatters.
- **Static analysis**.
- **Plugin discovery** — confirm each repo binds to a plugin and its required toolchains are present
  (`doctor` probes).
- **Dependency graph** — build and store.
- **Device discovery** — enumerate attached phones/sims/ESP32 boards (drives which QA rungs are
  possible vs deferred; feeds device locks).

Nothing here consumes AI tokens. Failures are reported as **current environment state**, not as work to
do — they inform eligibility/deferral, and the owner decides follow-ups.

## 8. Dashboard initialization

Create the **first dashboard state** ([09_Dashboard](09_Dashboard.md)) from current reality only:

- Present the project profile, migration report summary, eligibility distribution, deferrals,
  discovered devices, and validation results.
- **No historical assumptions** — no fabricated runs/metrics; the Execution State starts empty. The
  dashboard shows the project *as it is now*.

## 9. Human review (hard gate)

Migration ends by producing a **final migration report**:

- Project profile + summary.
- Per-issue flags, eligibility recommendations, and proposed normalizations (with exact Jira
  mutations).
- Deferred-work classification.
- Knowledge Brain contents preview.
- Build/validation results and device inventory.

**Nothing changes until the owner approves.** On approval, `cwv2 migrate --apply` performs only the
**approved, reversible** actions: creating/populating the `ClaudeWorker` field, adding proposed AC
comments, creating approved splits/links, and finalizing the Knowledge Brain. It **never** deletes or
closes issues. After apply, `migrated_at` is set and the Scheduler may begin.

## Idempotency, safety & re-runs

- **Idempotent:** re-running `migrate` refreshes the profile/report without duplicating field writes,
  comments, or links (checks existing state first).
- **Non-destructive:** no delete/close/force ever; only additive, reversible Jira writes on `--apply`.
- **Reversible:** every applied change is logged (`state.db.events`) with enough detail to reverse it;
  the Knowledge Brain init is rebuildable.
- **Re-migration:** a project can be re-migrated after big changes; it diffs against the stored profile
  and reports what changed.

## Migration as the first phase (summary)

```
repo analysis ─┐
jira analysis ─┤
eligibility  ─┤
normalization ─┼─▶ migration report ─▶ OWNER APPROVES ─▶ apply (reversible) ─▶ migrated_at set
deferred work ─┤                                                                   │
brain init    ─┤                                                                   ▼
build valid.  ─┤                                                          Scheduler admits work
dashboard init ┘                                                          (only ClaudeWorker=Enable)
```

## Invariants (migration-specific)

- **MG-1** No Assignment starts for a project until migration is approved (`migrated_at` set).
- **MG-2** Migration never deletes or closes a Jira issue; all applied changes are additive and
  reversible.
- **MG-3** Eligibility is never blanket-Enabled; ambiguous issues default to Needs Review; sensitive
  ones to Disable/Manual Only.
- **MG-4** Every deterministic step spends zero tokens; AI is used only for genuine reasoning
  (AC drafting, ambiguous-duplicate judgment, prose refinement).
- **MG-5** Migration is project-agnostic; only config + plugins differ across projects (P10).
- **MG-6** The initial dashboard/Execution State contains no fabricated history — current state only.
