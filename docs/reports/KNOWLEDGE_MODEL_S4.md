# S4 — Knowledge Model (Knowledge Brain)

Authoritative description of the `internal/knowledge` data model. Implements docs/04_ProjectBrain.md
(Knowledge Brain) and docs/21 S4, with the 8 owner-approved modifications.

## What it is / is NOT

The Knowledge Brain stores **durable engineering knowledge only** — architecture, patterns,
standards, decisions, rules, glossary. It is **not** an AI memory system: no embeddings, no vector
DB, no semantic search, no chat history. It never stores **execution state** (that is the Assignment
Store, S3). It spends **zero model tokens** — every operation is deterministic Go (Law 18, P9).

AI may **propose** knowledge (a `Draft` entry) but never asserts knowledge as truth; a human
approves a draft into `Active` (P4).

## Entry — one immutable version

The nine knowledge fields (owner spec) plus one sanctioned format-metadata field:

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Stable forever. Identifies the entry across all its versions. Charset `[A-Za-z0-9._-]`. |
| `version` | int | 1,2,3…; the version *within* the id's history. |
| `category` | string | **Documented-vocabulary string, not an enum** (mod 1). Any non-empty value; a project adds its own with no code change. `RecommendedCategories` is guidance only. |
| `title` | string | Short human label. |
| `body` | string | The knowledge text. |
| `source` | enum | Provenance, one of **human, architecture, acp, documentation, code, plugin, migration** (mod 3) — for future trust weighting. |
| `status` | enum | **active, deprecated, archived, draft** (mod 4). Controls prompt eligibility. |
| `created_at` | RFC3339 | When the id first appeared (v1); preserved across versions. |
| `updated_at` | RFC3339 | When *this* version was written. |
| `schema_version` | int | Record FORMAT metadata (mirrors S3 `spec_version`); enables deterministic migration. NOT a knowledge field. |

## Identity & versioning (mod 2)

- An `id` is created once (`Create`) and is **never duplicated** — a second `Create` with the same id
  fails.
- Evolving an entry (`Update`, `Deprecate`, `Archive`, `Restore`) appends a **new version under the
  same id**. History belongs to the id.
- The **current** entry is *derived*: the highest-version record for the id. There is no mutable
  "current" pointer to corrupt.

## Storage — append-only (mod 6)

`FileStore` writes one JSON-Lines file per id (`knowledge/entries/<id>.jsonl`): each version is a
single appended line (`O_APPEND` + `fsync`). A prior line is **never rewritten or deleted** — so the
full history is durable and git-diffable, and a crash mid-append cannot corrupt earlier versions.
`MemoryStore` is the second implementation (tests / decoupling proof). Both satisfy the append-only
`Store` interface; the Brain never knows which backs it (matches S3's storage inversion).

Format-version mismatches are handled exactly as S3: `migrate()` upgrades legacy records
deterministically and **refuses** a newer/unknown format rather than silently ignoring it.

## Lifecycle API (`Brain`)

`Create` · `Update(Change)` · `Deprecate` · `Archive` · `Restore` · `Propose` (Draft) · `Get`
(current) · `List` (current of each) · `History` · `Categories` (live vocabulary) · `Growth`.
Timestamps come from an injectable clock so records are deterministic under test.

## Prompt Builder — deterministic (mod 5, mod 7)

`SelectContext(Selector)` returns the knowledge slice for a prompt. Selection is a **pure function**:
same Brain + same `Selector` ⇒ byte-identical prompt. Scoring inputs:

1. **Status** — active only, unless `IncludeNonActive` is set (mod 4).
2. **Category filters** — optional include-only set.
3. **Keyword relevance** — distinct query-term overlap (word-boundary, case-insensitive) over
   title+body+category.
4. **Explicit project context** — extra terms (file paths, module/symbol names) scored like keywords.
5. **Max entry count** and **6. Max byte budget** — hard caps; ties break by (score, category, id) for
   a total, deterministic order.

**Future search plugins (mod 7):** selection is split into `candidates` (deterministic filter) →
`rank` (deterministic default score). A future embeddings/vector plugin would replace `rank` over the
same candidate set. The **core never depends on it** and works fully standalone — no plugin, no core
change required today.
