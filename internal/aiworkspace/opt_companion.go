package aiworkspace

import (
	"context"
	"errors"
)

// These optimizers need embeddings / a vector DB, which live in the local companion daemon — not the
// single-dep core. They register so they're visible + configurable, but are marked RequiresCompanion:
// the service routes their execution to the companion (POST /optimize) and returns a clear "requires
// local companion" error when none is connected. Their local Optimize() is only a safety fallback.
var errCompanionRequired = errors.New("requires a local companion (embeddings/vector DB run off-core)")

func init() {
	Register(semanticDedupOptimizer{})
	Register(ragOptimizer{})
	Register(embeddingOptimizer{})
}

type semanticDedupOptimizer struct{}

func (semanticDedupOptimizer) Meta() OptimizerMeta {
	return OptimizerMeta{
		ID: "semantic-dedup", Name: "Semantic Deduplicator", Category: CatContent, Version: "1",
		Description:       "Removes near-duplicate passages by meaning (embeddings), not just exact text.",
		Kinds:             []string{"text", "markdown"},
		RequiresCompanion: true,
		ConfigSchema: []FieldSpec{
			{Key: "threshold", Label: "Similarity threshold (0–1)", Type: "string", Default: "0.9"},
		},
	}
}
func (semanticDedupOptimizer) Optimize(context.Context, OptimizeInput) (OptimizeOutput, error) {
	return OptimizeOutput{}, errCompanionRequired
}

type ragOptimizer struct{}

func (ragOptimizer) Meta() OptimizerMeta {
	return OptimizerMeta{
		ID: "rag", Name: "RAG Optimizer", Category: CatContext, Version: "1",
		Description:       "Selects only the most relevant chunks for a query via retrieval, shrinking context.",
		Kinds:             []string{"text"},
		RequiresCompanion: true,
		ConfigSchema: []FieldSpec{
			{Key: "topK", Label: "Chunks to keep (top-K)", Type: "int", Default: 8},
		},
	}
}
func (ragOptimizer) Optimize(context.Context, OptimizeInput) (OptimizeOutput, error) {
	return OptimizeOutput{}, errCompanionRequired
}

type embeddingOptimizer struct{}

func (embeddingOptimizer) Meta() OptimizerMeta {
	return OptimizerMeta{
		ID: "embedding", Name: "Embedding Optimizer", Category: CatCache, Version: "1",
		Description:       "Deduplicates and caches embeddings so repeated content isn't re-embedded.",
		Kinds:             []string{"text"},
		RequiresCompanion: true,
		ConfigSchema:      []FieldSpec{},
	}
}
func (embeddingOptimizer) Optimize(context.Context, OptimizeInput) (OptimizeOutput, error) {
	return OptimizeOutput{}, errCompanionRequired
}
