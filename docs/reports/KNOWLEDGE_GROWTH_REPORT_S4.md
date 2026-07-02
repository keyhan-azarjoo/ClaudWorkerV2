# S4 — Knowledge Growth Report (mod 8)

The Knowledge Growth Report tracks how the Knowledge Brain accumulates knowledge over time and how
effectively the deterministic Prompt Builder condenses it. It is produced from live store data by
`Brain.Growth(sel)` and surfaced by `cwv2 knowledge growth`.

## Metrics (all derived, none estimated)

| Field | Meaning |
|---|---|
| `entries` | distinct ids (knowledge items) |
| `active` | ids whose current status is `active` |
| `versions` | total version records across all ids |
| `avg_versions` | `versions / entries` — how much knowledge is revised vs added |
| `by_category` / `by_status` / `by_source` | distribution of current entries |
| `corpus_bytes` | rendered size of all active entries (naive "dump-everything" prompt cost) |
| `prompt_bytes` / `prompt_entries` | size of the deterministic slice for the given `Selector` |
| `reduction_ratio` | `1 − prompt_bytes / corpus_bytes` — condensation power |
| `duplicate_ids` | always `0` — ids are unique by construction (mod 2), so duplication is impossible, not merely rare |

## Why each metric matters

- **entries / versions / avg_versions** — growth vs churn. Rising `avg_versions` means knowledge is
  being *refined* (good); rising `entries` means new knowledge is being *captured*.
- **by_status** — health of the pipeline: `draft` = AI proposals awaiting human approval; `deprecated`/
  `archived` = retired-but-retained history (nothing deleted, mod 6).
- **by_source** — provenance mix, the input to future trust weighting (mod 3).
- **reduction_ratio** — the headline: how small the prompt stays as the corpus grows. It should trend
  **up** as the Brain accumulates, because the budgeted slice size is fixed while the corpus expands.
- **duplicate_ids** — a structural guarantee, reported so the invariant is visible and auditable.

## Reference figures (from tests)

- 500-entry active corpus → prompt slice 534 B of 33,780 B ⇒ **reduction 0.9842** (`TestPromptReductionOnLargeCorpus`).
- Small smoke corpus (2 entries, 1 active, 3 versions) ⇒ `entries=2, versions=3, active=1, duplicate_ids=0`
  (verified via `cwv2 knowledge growth`).

## Usage

```
cwv2 knowledge growth --config cwv2.yaml \
  --keywords "git,merge" --categories "rule,standard" \
  --max-entries 8 --max-bytes 4096
```

The report reflects the *same* `Selector` the engine would use to build a real prompt, so
`reduction_ratio` is a true measurement of that prompt, not a hypothetical.
