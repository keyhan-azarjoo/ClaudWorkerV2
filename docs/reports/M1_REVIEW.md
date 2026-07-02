# M1 Review — ClaudWorker V2 (S0–S4)

Complete engineering review of the platform after Milestone M1. Goal: confirm the foundation is
strong, small, and simple enough to carry the rest of V2 — **not** to add features. No S5. No
architecture changes.

Reviewed at `development@bda09f4`, spec frozen **v2.1.0**. Working tree clean; `gofmt`/`go vet`/
`go test -race ./...` all green.

---

## 1. Executive summary

S0–S4 is a **healthy, minimal, deterministic-first foundation**. Ten packages, one external
dependency (`gopkg.in/yaml.v3`), ~4.0k lines of production Go with ~2.2k lines of tests, average
cyclomatic complexity **4.04**, no import cycles, and a clean layered dependency graph. The two hard
architectural separations — **Knowledge Brain ⟂ Execution State** and **AI-only-behind-a-port** — are
observably intact (the `knowledge` package imports no execution code; the engine imports no AI).
Restart safety is proven by tests against real durable storage. No blocking defects were found and no
refactor is required: the code is already at or near its simplest correct form. Recommendations below
are small and mostly deferrable to the phases that need them.

**Verdict: proceed to S5 after review. Foundation is sound.**

### Scorecard

| Dimension | Score | Basis |
|---|---|---|
| Architecture compliance | **9.5 / 10** | Full compliance with frozen arch, Laws, roadmap; one doc-vs-impl format nuance (JSONL vs "markdown+index"), justified. |
| Maintainability | **9 / 10** | Small single-purpose files (avg 87 LOC), consistent idioms, strong tests (87% on new pkg). |
| Simplicity | **9 / 10** | No speculative abstraction; interfaces only at ≥2 impls; state minimised. Minor repeated CLI plumbing. |
| Complexity (control) | **9 / 10** | Avg CC 4.04; only CLI switch-dispatchers exceed 20 (flat, low cognitive load). |
| Performance | **9 / 10** | Startup <10 ms; selection 0.5 ms/500; persistence bound by fsync (~3.5 ms, correct). |
| Token efficiency | **10 / 10** | Zero tokens in all deterministic paths; knowledge slice byte-budgeted (98.4% reduction). |
| Technical debt | **9 / 10** | All deferrals justified with triggers; none overdue. One (D3) is a planned S5 wiring. |

---

## 2. Architecture compliance

Method: read every package; cross-check against docs/02, docs/04, docs/16, docs/19 (System Laws),
docs/21 (roadmap).

| Check | Result |
|---|---|
| Frozen architecture (v2.1.0) | **Compliant.** Package boundaries match docs/02; engine-home layout matches docs/04; Assignment lifecycle matches docs/16. |
| System Laws | **Compliant.** Law 4 (disposable workers — Worker is a port, owns nothing). Law 17 (interface only ≥2 impls — `Store` ×2 in both `assignment` and `knowledge`; no others). Law 18 (AI only behind Worker port — verified: `git`, `jira`, `assignment`, `knowledge` contain no model calls). Law 19 (never lose/redo durable state; validate persisted format — `migrate()` in both stores, restart tests green). |
| Roadmap order | **Compliant.** S0→S4 built in sequence; ACP-0001 (Assignment Engine before DB) honoured; S5 not started. |
| Deterministic-first | **Compliant.** All work outside the (not-yet-built) Worker is deterministic Go. Knowledge selection is a pure function. |

**Deviations found: 1 (minor, justified).**
- **D-A1 — storage form.** docs/04 describes authored knowledge as "git-diffable markdown + an index
  row in knowledge.db". The implementation uses append-only **JSONL** (one file per id). Rationale:
  JSONL is git-diffable *and* natively append-only (matching approved mod 6), carries the structured
  fields (source/status/version) that free markdown cannot, and avoids introducing a DB at S4. This
  satisfies the *intent* (durable, diffable, human-readable, append-only) and is recorded in
  KNOWLEDGE_MODEL_S4.md. No behavioural conflict; no action required. If a `knowledge.db` index is
  later wanted for scale, it is additive (see D1 in debt).

No other deviations. `staging`/`main` untouched; identity is `keyhanazarjoo` throughout.

---

## 3. Complexity review

| Metric | Value | Note |
|---|---|---|
| Packages | 10 (+1 cmd) | Flat `internal/` layout, no sub-nesting. |
| External deps | **1** (`gopkg.in/yaml.v3`) | Everything else is stdlib. |
| Import graph | Acyclic, layered | Leaves: `config, enginehome, git, jira, knowledge, logging, secrets`. `assignment`→{git,jira}. `doctor`→{config,enginehome,secrets}. `cmd`→all. |
| Coupling | Low | **`knowledge` depends only on stdlib** — zero coupling to execution state (confirms docs/04 split). |
| Production LOC | ~3,994 | Across 46 files. |
| Test LOC | ~2,166 | Test:code ≈ 0.54. |
| Avg file size | ~87 LOC | Small, single-purpose. |
| Largest file | `internal/jira/jira.go` (454) | A toolbelt client; cohesive, not tangled. |
| Avg cyclomatic complexity | **4.04** | Healthy. |
| Functions CC > 10 (non-test) | `cmdKnowledge`(49), `cmdGit`(44), `cmdJira`(32), `doctor.Run`(17), `config.Validate`(16), `config.ApplyDefaults`(14), `cmdAssignment`(13), `validateID`(12), `Brain.Growth`(11), `jira.do`(11) | All are flat `switch`/validation ladders — high branch count, low cognitive complexity. |
| Exported symbols | knowledge 94, jira 74, git 64, assignment 57 | Counts include struct fields/consts; toolbelt surfaces, not god-objects. |

**Growing too quickly?** No. `knowledge` (811 LOC) is the largest new package and is fully partitioned
into model/store/brain/prompt/growth. Nothing is trending toward a monolith. The CLI (`cmd/cwv2`) grows
one file per subsystem, which is linear and expected.

### Duplicated logic / concepts (candidates, not defects)
1. **Store pattern** (`migrate()` + `FileStore`/`MemoryStore` + reject-newer-format) exists in *both*
   `assignment` and `knowledge`. This is duplicated **concept**, not code (different types, different
   record shapes). Unifying via a generic store would **couple the two packages** to a shared module,
   trading independence for ~120 LOC. **Recommendation: keep separate** — package independence is worth
   more than the LOC here (see §5).
2. **CLI plumbing** (flag parse → `config.Load` → `enginehome.For`) repeats in `cmdAssignment`,
   `cmdKnowledge`, `cmdJira`. A tiny `withConfig()` helper could remove ~15 lines total. Low value,
   low risk; deferrable (§5, S-2).
3. **Atomic-write/fsync** appears in `assignment.FileStore` (temp+rename) and `knowledge.FileStore`
   (O_APPEND). Different strategies for different needs (overwrite vs append) — **not** true duplication.

---

## 4. Token review

| Claim | Verified |
|---|---|
| AI never performs deterministic work | ✅ No model call exists anywhere in S0–S4. AI is a not-yet-implemented port (`Worker`). |
| Prompt sizes minimal | ✅ `SelectContext` is byte/entry-budgeted; 500-entry corpus → 534 B slice (98.4% reduction). |
| Knowledge Brain deterministic | ✅ Pure functions; `TestSelectContextIsDeterministic` (20× byte-identical). |
| No unnecessary prompt growth | ✅ Slice size is fixed by budget regardless of corpus size. |
| No hidden token consumption | ✅ The `cwv2` binary makes no network AI call; only git/jira HTTP (deterministic). |

**Estimated tokens for a typical Assignment (projected, S5 not yet built):** the deterministic engine
spends **0 tokens** assembling context. The single Worker call will receive: task (summary+AC)
~150–400 tok + knowledge slice (≤4 KB budget) ~500–1,000 tok + relevant files (S5) ~1–4 k tok + last
failure text ~100–500 tok ≈ **~2–6 k input tokens per attempt**, dominated by source files, not by the
Knowledge Brain. The Brain's contribution is capped and reproducible. This is the intended profile.

---

## 5. Simplification review

Questioned every subsystem against "can it be smaller / merge / lose an interface / lose state / lose
config / replace abstraction with deterministic code":

| Question | Finding |
|---|---|
| Can packages merge? | Not beneficially. Each has a distinct responsibility and clean boundary; merging (e.g. `git`+`jira` into a "toolbelt") would create a larger, less cohesive unit. **Keep as-is.** |
| Can interfaces disappear? | The two `Store` interfaces each have 2 real impls (Law 17 satisfied) and are load-bearing for restart tests. **Keep.** No other interfaces exist to remove. |
| Can state disappear? | Already minimised in S3 (Assignment = 3 execution fields + 1 metadata). Knowledge persists only authored knowledge; "current version" is *derived*, not stored. **Nothing to remove.** |
| Can config disappear? | Config is validated and used; no dead fields observed in S0–S4 scope. (A full field-usage audit is worthwhile once more subsystems consume it — S-3.) |
| Can deterministic code replace abstraction? | Already the default. The future-search seam (mod 7) is a *documented function split*, not an interface — correct. |

**Refactors performed this review: none required.** The one consistency fix (SpecVersion 2.0.0→2.1.0)
was already applied during S4. Applying further refactors now would add risk without reducing
complexity, violating "refactor only if it reduces complexity."

**Optional micro-simplifications (deferred, low priority):**
- **S-1** — none of the CC-heavy CLI dispatchers warrant table-driving yet; a `switch` is the simplest
  correct form. Revisit only if a 4th toolbelt lands.
- **S-2** — extract a `withConfig(args) (*Config, Layout, error)` CLI helper (~-15 LOC) when the next
  config-bearing command is added.
- **S-3** — run a config field-usage audit at the S6/S7 boundary (Decision Engine/Locks) to drop any
  never-read fields.

---

## 6. Restart review (deterministic recovery)

Simulated via existing tests + code reading:

| Failure point | Recovery mechanism | Evidence |
|---|---|---|
| Crash during Assignment (mid-develop) | Last stable state persisted before each transition; `Resume` re-drives from it; disposable worker re-runs safely | `TestRestartResumeFromDisk` (new FileStore over same dir → resumes to Done, no completed work redone) |
| Crash during merge | `handOffToMerge` is idempotent (fetch → `--no-ff`; identical merge is a no-op); bounded retry | `engine.go` + retry tests |
| Crash during knowledge update | Append-only single-line `O_APPEND`+`fsync`; a half-written line is the only loss and it is the *newest* version, never a prior one | `TestFileStoreIsAppendOnly`, `TestFileStorePersistsAcrossReopen` |
| Crash during persistence | Assignment uses temp+fsync+**rename** (atomic); knowledge uses atomic line append | store tests |
| Crash during restart | `List`/`History` load through `migrate()`; a newer/unknown format **aborts** rather than silently proceeding | `TestResumeRejectsNewerFormat`, `TestStoreRejectsNewerFormat` |

**Result: deterministic recovery holds at every simulated point.** No data-loss or redo path found.
Caveat (documented, not a defect): multi-entry knowledge *batches* are not transactional (debt D4) —
irrelevant until a batch importer exists.

---

## 7. Performance review (measure, don't optimise)

| Operation | Measurement | Note |
|---|---|---|
| Startup (`cwv2 version`) | **<10 ms** | Static Go binary; no init-time work. |
| Knowledge selection | **~0.50 ms** over 500 entries (~3.5 k allocs) | Linear scan + sort; fine to low-thousands of entries. |
| Persistence (atomic save) | **~3.48 ms/op** | Dominated by `fsync` on the SSD — this is durability, not waste. |
| Git operations | subprocess-bound (~tens of ms/call) | Cost is `exec` of the real `git` binary, not our code. |
| Jira operations | network-bound | Deterministic HTTP; token never logged. |

**Bottlenecks (documented, NOT to be optimised now):**
- **fsync (~3.5 ms/persist)** is the largest deterministic cost. Correct for crash-safety; only revisit
  if per-assignment write volume becomes high (it won't at current design).
- **git subprocess spawn** dominates git-op latency. Acceptable; batching is a far-future concern.
- Knowledge selection allocations (~3.5 k/call) are trivial at expected scale; add a current-version
  cache only if a corpus reaches ~10 k entries (debt D1). **No premature optimisation.**

---

## 8. Code quality review

| Aspect | Assessment |
|---|---|
| Naming | Consistent and intention-revealing (`ClaimAndRun`, `SelectContext`, `handOffToMerge`, `migrate`). Enum values are lower-case string consts across both stores. |
| Consistency | Both stores follow the same `Store`+File+Memory+`migrate` shape; both CLIs use `emit`/`emitErr` JSON; both use injectable seams (clock, logger) for testability. |
| Cohesion | High — each file is single-purpose; each package one responsibility. |
| Unnecessary abstractions | None found. No interface without ≥2 impls; no manager/factory/registry cruft. |
| Future maintainability | Strong — small files, heavy doc-comments tying code to laws/principles, 87%/70%+ coverage on core packages. |

**Refactors: none** (would not reduce complexity). Quality is release-grade.

---

## 9. Technical debt review

Re-confirmed each deferred item from TECHNICAL_DEBT_S4.md:

| Item | Status | Decision |
|---|---|---|
| D1 no current-version cache | Justified | **Keep deferred** — negligible at current scale; trigger = ~10 k entries. |
| D2 exact-match relevance | Justified | **Keep deferred** — deterministic by design; revisit via optional plugin (mod 7). |
| D3 Prompt Builder not yet consumed | Planned | **Resolve in S5** — wire `KnowledgeContext`. Not overdue (S5 is next). |
| D4 no multi-entry batch atomicity | Justified | **Keep deferred** — no batch writer exists yet. |
| D5 sort-on-read | Justified | **Keep deferred** — trivial cost, adds robustness. |
| D6 single-process locking | Justified | **Keep deferred** — matches single-engine model; Lock Manager is S7. |

Nothing is deferred without a clear reason; nothing needs implementing now. **No debt is overdue.**

---

## 10. Future readiness

Does S0–S4 form a stable base for the remaining subsystems?

| Upcoming | Ready? | Why |
|---|---|---|
| **Workers (S5)** | ✅ | `Worker` port + `WorkerInput`/`WorkerResult` exist; `KnowledgeContext` field is waiting; `SelectContext`/`RenderContext` produce the slice. Only wiring remains. |
| **QA (S8)** | ✅ | Assignment lifecycle already has a `StateQA` handoff seam (`handOffToQA`) that a real QA subsystem slots into. |
| **Decision Engine (S6)** | ✅ | Retry/attempt state and terminal-state logic are in place; the engine's `drive` loop is the extension point. |
| **Dashboard (S10)** | ✅ | All state is inspectable via JSON CLIs (`assignment list`, `knowledge list/growth`); a read-only dashboard can consume these. |
| **Plugins (S11)** | ✅ (seam ready) | Repo `plugin` config field exists; knowledge search-plugin seam documented; no core dependency to unwind later. |

**Missing-but-fundamental? None found.** The two structural bets (state minimisation + Knowledge/
Execution separation) are the ones hardest to retrofit, and both are already correct. Everything else
is additive.

---

## 11. Recommendations

**Before S5 (optional, low-risk):** none required. May apply S-2 (CLI `withConfig` helper) if desired,
but it is cosmetic.

**During S5:** resolve D3 by wiring `SelectContext`→`WorkerInput.KnowledgeContext`; this is the single
most valuable next step and validates the Prompt Builder end-to-end.

**At S6/S7 boundary:** run S-3 (config field-usage audit) to prune any unread config.

**Ongoing:** keep the "one pattern, reused" discipline (Store shape, CLI shape, injectable seams);
resist unifying `assignment` and `knowledge` stores despite the conceptual overlap — package
independence is the higher-value property.

**Do not:** add a database, add embeddings/vector search to core, or table-drive the CLI, until a
concrete trigger (documented above) is hit.

---

### Conclusion
The foundation is small, deterministic, well-tested, and architecturally faithful. It is strong enough
to support Workers, QA, the Decision Engine, the Dashboard, and Plugins without rework. **Approved to
continue to S5 pending this review; no simplification is blocking.**
