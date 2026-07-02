# S4 — Architecture Compliance Report (Knowledge Brain)

Verifies `internal/knowledge` against the frozen architecture (v2.1.0) and the 8 owner-approved S4
modifications. Verdict: **COMPLIANT**.

## System Laws / Principles

| Ref | Requirement | How S4 complies |
|---|---|---|
| Law 18 | AI only behind the Worker port; everything else deterministic Go | The Brain is pure Go. No AI, no network, no tokens. `Propose` merely records a Draft for human approval. |
| P9 | Prompts assembled deterministically, zero tokens | `SelectContext`/`RenderContext` are pure functions; identical inputs → byte-identical prompt (proved by `TestSelectContextIsDeterministic`). |
| P4 | Knowledge is permanent; workers disposable; AI proposes, never asserts | Append-only history; `Propose` → `Draft`, excluded from prompts until a human `Restore`s it. |
| Law 19 | Never lose/redo durable state; validate persisted format | Append-only JSONL survives restart (`TestFileStorePersistsAcrossReopen`); `migrate()` refuses newer formats (`TestStoreRejectsNewerFormat`). |
| docs/04 | Knowledge Brain ≠ Execution State; the two never mix | The package stores only knowledge; execution state stays in `internal/assignment`. Nothing cross-imports. |
| docs/04 | Authored knowledge is git-diffable | One append-only JSONL file per id under `knowledge/entries/` — line-oriented, diffs cleanly. |
| Law 17 | Interface only with ≥2 implementations | `Store` has two real impls (FileStore, MemoryStore); no speculative interfaces. |

## The 8 approved modifications

| # | Modification | Evidence |
|---|---|---|
| 1 | Category = documented-vocabulary string, not enum | `Category string`; validation requires only non-empty; `RecommendedCategories` is guidance. `TestCustomCategoryAccepted`. |
| 2 | Stable ids; Update = new version; no duplicate ids | `Create` rejects existing id (`TestCreateRejectsDuplicateID`); `Update` appends under same id (`TestUpdateAppendsVersionPreservingHistory`). |
| 3 | Source ∈ 7 provenance values | `Source` enum + `Valid()`; invalid rejected (`TestValidationRejectsBadFields`). |
| 4 | Status ∈ {active,deprecated,archived,draft}; prompts active-only unless requested | `Status` enum; `candidates` filters to active unless `IncludeNonActive` (`TestSelectContextActiveOnlyByDefault`, `...IncludeNonActive...`). |
| 5 | Deterministic Prompt Builder over the six inputs | `Selector` fields = status, categories, keywords, context, max-entries, max-bytes; pure scoring. Tests for each dimension. |
| 6 | Append-only storage; current derived | JSONL append (`TestFileStoreIsAppendOnly`); `Get` derives current = highest version. |
| 7 | Future search plugins optional; core independent | `candidates`/`rank` split documented; no plugin dependency in core. |
| 8 | Knowledge Growth Report | `Growth(Selector)` + `KNOWLEDGE_GROWTH_REPORT_S4.md`; `TestGrowthStats`. |

## Boundaries respected

- **No new external dependency** (still only `gopkg.in/yaml.v3` module-wide).
- **enginehome** gained one field (`KnowledgeEntries`) under the existing `knowledge/` dir — no layout
  redesign.
- **CLI** `cwv2 knowledge` mirrors the existing git/jira/assignment toolbelt shape (JSON output, shared
  `emit`/`emitErr`), spending zero tokens.

## Gates

- Unit + integration: `go test -race ./...` — all 10 packages PASS.
- Architecture-compliance: this report.
- Performance: see PROMPT_SIZE_ANALYSIS_S4.md (selection is O(entries) with no I/O in the hot path for
  MemoryStore; FileStore reads are bounded by corpus size).
