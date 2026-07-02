# S4 — Technical Debt Report (Knowledge Brain)

Honest inventory of shortcuts and deferrals in `internal/knowledge`. None block S4 acceptance; each
has a rationale and a trigger for when it must be paid.

## Accepted / by-design (not debt)

- **JSONL over a database.** Append-only JSONL is the correct fit for a small, git-diffable,
  append-only knowledge corpus (docs/04). A DB is unnecessary at this scale. *Trigger to revisit:*
  corpus size or query needs outgrow linear scans (see below).
- **Deterministic keyword scoring, no embeddings.** Mandated (mod 5/7). Semantic search is explicitly
  a *future optional plugin*, not core.

## Real debt (deferred, with triggers)

| # | Item | Impact now | Trigger to pay it |
|---|---|---|---|
| D1 | `List`/`SelectContext` read every id's full history each call (no in-memory index/cache). | Negligible at hundreds–low-thousands of entries (~0.54 ms/500). | If a corpus reaches ~10k+ entries or selection shows up in profiling, add a cached current-version index (invalidate on Append). |
| D2 | Relevance is exact word-boundary overlap — no stemming, synonyms, or phrase/field weighting. | Good precision, modest recall; fine for deterministic selection. | When recall gaps appear in real prompts, add deterministic term expansion (still no AI) or the optional ranker plugin (mod 7). |
| D3 | Prompt Builder slices are not yet *consumed* — `WorkerInput.KnowledgeContext` is still empty; wiring lands in S5 (Workers). | No functional gap in S4; the builder is unit-proven in isolation. | S5 must call `SelectContext`/`RenderContext` and populate `KnowledgeContext`. |
| D4 | No cross-file transaction: a multi-entry batch could partially apply if the process dies mid-batch. | Each *entry* append is atomic (O_APPEND+fsync); only multi-entry *batches* aren't. | If batch imports (e.g. migration, docs/22) need all-or-nothing, add a batch/journal wrapper. |
| D5 | `FileStore.History` sorts by version on every read (robustness against manual edits). | Trivial cost; correct. | Drop the sort only if profiling ever flags it (unlikely). |
| D6 | Concurrency is per-process (`sync.Mutex`); no cross-process file locking. | Correct for the single-engine model (one engine owns a project home). | If two engine processes ever share one project home, add advisory file locks. |

## Explicitly NOT debt

- **Draft approval is manual** — intentional (P4: AI proposes, human approves), not a missing feature.
- **No delete** — intentional (mod 6, "never delete"); `Archive` is the retirement path.

## Test debt

None identified: unit + integration cover model, lifecycle, append-only durability, format
migration/rejection, determinism, category/status/byte/entry budgets, and the growth metrics. All run
under `-race`.
