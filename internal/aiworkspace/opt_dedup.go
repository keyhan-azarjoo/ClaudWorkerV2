package aiworkspace

import (
	"context"
	"strings"
)

func init() { Register(dedupOptimizer{}) }

// dedupOptimizer removes duplicate lines or paragraphs, keeping the first occurrence. Useful for merged
// logs, repeated boilerplate, and concatenated docs.
type dedupOptimizer struct{}

func (dedupOptimizer) Meta() OptimizerMeta {
	return OptimizerMeta{
		ID: "dedup", Name: "Duplicate Detector", Category: CatContent, Version: "1",
		Description: "Removes repeated lines or paragraphs (first occurrence kept) to cut redundant tokens.",
		Kinds:       []string{"text", "log", "markdown"},
		ConfigSchema: []FieldSpec{
			{Key: "mode", Label: "Granularity", Type: "select", Default: "lines", Options: []string{"lines", "paragraphs"}},
			{Key: "caseInsensitive", Label: "Case-insensitive match", Type: "bool", Default: false},
			{Key: "ignoreWhitespace", Label: "Ignore whitespace differences", Type: "bool", Default: true},
		},
	}
}

func (o dedupOptimizer) Optimize(_ context.Context, in OptimizeInput) (OptimizeOutput, error) {
	cfg := mergeConfig(o.Meta(), in.Config)
	mode := cfgString(cfg, "mode", "lines")
	ci := cfgBool(cfg, "caseInsensitive", false)
	iw := cfgBool(cfg, "ignoreWhitespace", true)

	norm := func(s string) string {
		if iw {
			s = strings.Join(strings.Fields(s), " ")
		}
		if ci {
			s = strings.ToLower(s)
		}
		return s
	}

	var units []string
	var sep string
	if mode == "paragraphs" {
		units = strings.Split(strings.ReplaceAll(string(in.Content), "\r\n", "\n"), "\n\n")
		sep = "\n\n"
	} else {
		units = splitLines(string(in.Content))
		sep = "\n"
	}

	seen := map[string]bool{}
	out := make([]string, 0, len(units))
	dropped := 0
	for _, u := range units {
		key := norm(u)
		if strings.TrimSpace(key) == "" { // never dedupe blank separators
			out = append(out, u)
			continue
		}
		if seen[key] {
			dropped++
			continue
		}
		seen[key] = true
		out = append(out, u)
	}
	unit := "line"
	if mode == "paragraphs" {
		unit = "paragraph"
	}
	notes := []string{"removed " + pluralN(dropped, "duplicate "+unit)}
	return result(in.Content, strings.Join(out, sep), notes...), nil
}
