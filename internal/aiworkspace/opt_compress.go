package aiworkspace

import (
	"context"
	"regexp"
	"strings"
)

func init() { Register(compressOptimizer{}) }

// compressOptimizer is a safe general text compactor: trims trailing whitespace and collapses blank-line
// runs; "aggressive" also collapses repeated interior spaces/tabs (outside fenced code blocks).
type compressOptimizer struct{}

var multiSpace = regexp.MustCompile(`[ \t]{2,}`)

func (compressOptimizer) Meta() OptimizerMeta {
	return OptimizerMeta{
		ID: "compress", Name: "Compression Engine", Category: CatContent, Version: "1",
		Description: "Trims trailing whitespace and collapses blank-line runs to cut tokens with no meaning loss.",
		Kinds:       []string{"text", "markdown", "log"},
		ConfigSchema: []FieldSpec{
			{Key: "maxBlank", Label: "Max consecutive blank lines", Type: "int", Default: 1},
			{Key: "aggressive", Label: "Collapse repeated spaces (skips code fences)", Type: "bool", Default: false},
		},
	}
}

func (o compressOptimizer) Optimize(_ context.Context, in OptimizeInput) (OptimizeOutput, error) {
	cfg := mergeConfig(o.Meta(), in.Config)
	maxBlank := cfgInt(cfg, "maxBlank", 1)
	aggressive := cfgBool(cfg, "aggressive", false)

	lines := splitLines(string(in.Content))
	inFence := false
	for i, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "```") {
			inFence = !inFence
		}
		ln = trimTrailing(ln)
		if aggressive && !inFence {
			// Preserve leading indentation; collapse only interior runs.
			indent := ln[:len(ln)-len(strings.TrimLeft(ln, " \t"))]
			ln = indent + multiSpace.ReplaceAllString(strings.TrimLeft(ln, " \t"), " ")
		}
		lines[i] = ln
	}
	lines = collapseBlankRuns(lines, maxBlank)
	return result(in.Content, strings.Join(lines, "\n")), nil
}
