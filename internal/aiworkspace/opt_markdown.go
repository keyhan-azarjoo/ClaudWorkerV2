package aiworkspace

import (
	"context"
	"regexp"
	"strings"
)

func init() { Register(markdownOptimizer{}) }

// markdownOptimizer compacts Markdown: strips HTML comments, trims trailing whitespace, collapses blank
// runs, and optionally removes consecutive duplicate lines. Lossless to the rendered document.
type markdownOptimizer struct{}

var htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)

func (markdownOptimizer) Meta() OptimizerMeta {
	return OptimizerMeta{
		ID: "markdown", Name: "Markdown Optimizer", Category: CatContent, Version: "1",
		Description: "Strips HTML comments and redundant whitespace from Markdown docs to shrink context.",
		Kinds:       []string{"markdown"},
		ConfigSchema: []FieldSpec{
			{Key: "stripComments", Label: "Remove HTML comments", Type: "bool", Default: true},
			{Key: "maxBlank", Label: "Max consecutive blank lines", Type: "int", Default: 1},
			{Key: "dedupeConsecutive", Label: "Drop consecutive duplicate lines", Type: "bool", Default: false},
		},
	}
}

func (o markdownOptimizer) Optimize(_ context.Context, in OptimizeInput) (OptimizeOutput, error) {
	cfg := mergeConfig(o.Meta(), in.Config)
	text := string(in.Content)
	notes := []string{}

	if cfgBool(cfg, "stripComments", true) {
		if n := len(htmlComment.FindAllString(text, -1)); n > 0 {
			text = htmlComment.ReplaceAllString(text, "")
			notes = append(notes, "removed "+pluralN(n, "HTML comment"))
		}
	}

	lines := splitLines(text)
	for i := range lines {
		lines[i] = trimTrailing(lines[i])
	}
	if cfgBool(cfg, "dedupeConsecutive", false) {
		out := lines[:0:0]
		var prev string
		var first = true
		dropped := 0
		for _, ln := range lines {
			if !first && ln == prev && strings.TrimSpace(ln) != "" {
				dropped++
				continue
			}
			out = append(out, ln)
			prev = ln
			first = false
		}
		lines = out
		if dropped > 0 {
			notes = append(notes, "dropped "+pluralN(dropped, "duplicate line"))
		}
	}
	lines = collapseBlankRuns(lines, cfgInt(cfg, "maxBlank", 1))
	return result(in.Content, strings.Join(lines, "\n"), notes...), nil
}
