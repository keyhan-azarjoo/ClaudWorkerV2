# S4 — Prompt-Size Analysis (Knowledge Brain)

Quantifies the core value of a deterministic Knowledge Brain: it turns an unbounded, growing
knowledge corpus into a small, relevant, budgeted prompt slice — at **zero model-token cost**.

## Method

`Brain.Growth(Selector)` measures, from live store data:
- **CorpusBytes** — rendered size of *all active* current entries (what a naive "dump everything"
  prompt would cost).
- **PromptBytes / PromptEntries** — the deterministic slice `SelectContext` actually returns under the
  entry + byte budget.
- **ReductionRatio** = `1 − PromptBytes / CorpusBytes`.

Reproducible via `TestPromptReductionOnLargeCorpus` and `BenchmarkSelectContext` (fixed clock, seeded
corpus — no randomness).

## Measured results

500-entry active corpus, `Selector{Keywords:[git,merge], MaxEntries:8, MaxBytes:4096}`:

| Metric | Value |
|---|---|
| Active corpus | 33,780 B |
| Selected prompt slice | 534 B (8 entries) |
| **Reduction** | **98.42 %** |
| Selection latency | ~0.54 ms / call (500 entries) |
| Allocations | ~3.5 k / call, ~414 KB transient |

The prompt slice stays bounded (≤ `MaxEntries`, ≤ `MaxBytes`) **regardless of corpus growth** — a
10× larger Brain yields the same-sized prompt, just higher reduction. This is the property that keeps
worker context "small" (a core V2 principle) as the project's knowledge accumulates.

## Budget semantics

- `MaxEntries` and `MaxBytes` are hard caps; the byte cap counts *rendered* bytes, so the number
  reflects what actually reaches the model.
- If the single most-relevant entry alone exceeds `MaxBytes`, it is still returned (an empty knowledge
  slice is worse than one over-budget slice) — verified by `TestMaxBytesAlwaysIncludesTopEntry`.
- Selection is deterministic: identical inputs → byte-identical prompt (`TestSelectContextIsDeterministic`),
  so prompt size is reproducible, not probabilistic.

## Complexity

`SelectContext` is `O(N·T)` for N candidate entries and T query terms (tokenise + overlap), plus an
`O(N log N)` sort. No embeddings, no model calls, no network. For MemoryStore the hot path is pure
CPU; for FileStore it reads each id's history once (bounded by corpus size) — acceptable for the
knowledge scale (hundreds–low-thousands of entries) and cacheable later if ever needed (not needed
now — see TECHNICAL_DEBT_S4.md).

## Token cost

**Zero.** Assembling the knowledge slice never calls a model (Law 18 / P9). Tokens are spent only
when the assembled prompt is later handed to the one disposable Worker (S5+), and this analysis shows
that prompt is minimal by construction.
