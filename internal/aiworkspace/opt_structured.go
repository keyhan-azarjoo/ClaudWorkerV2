package aiworkspace

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"
)

func init() {
	Register(jsonOptimizer{})
	Register(yamlOptimizer{})
	Register(xmlOptimizer{})
}

// jsonOptimizer minifies JSON losslessly (json.Compact — no reordering); on invalid JSON it falls back
// to trailing-whitespace + blank-line trimming so it never corrupts input.
type jsonOptimizer struct{}

func (jsonOptimizer) Meta() OptimizerMeta {
	return OptimizerMeta{
		ID: "json", Name: "JSON Optimizer", Category: CatContent, Version: "1",
		Description: "Minifies JSON (removes insignificant whitespace) with no key reordering; safe fallback on invalid JSON.",
		Kinds:       []string{"json"},
		ConfigSchema: []FieldSpec{
			{Key: "minify", Label: "Minify (compact)", Type: "bool", Default: true},
		},
	}
}

func (o jsonOptimizer) Optimize(_ context.Context, in OptimizeInput) (OptimizeOutput, error) {
	cfg := mergeConfig(o.Meta(), in.Config)
	if cfgBool(cfg, "minify", true) {
		var buf bytes.Buffer
		if err := json.Compact(&buf, in.Content); err == nil {
			return result(in.Content, buf.String(), "minified JSON"), nil
		}
	}
	// Fallback: not valid JSON → conservative whitespace cleanup.
	lines := splitLines(string(in.Content))
	for i := range lines {
		lines[i] = trimTrailing(lines[i])
	}
	return result(in.Content, strings.Join(collapseBlankRuns(lines, 1), "\n"), "invalid JSON — whitespace-trimmed only"), nil
}

// yamlOptimizer strips full-line comments and redundant whitespace from YAML (never reformats structure,
// never touches inline # which may be inside a value).
type yamlOptimizer struct{}

func (yamlOptimizer) Meta() OptimizerMeta {
	return OptimizerMeta{
		ID: "yaml", Name: "YAML Optimizer", Category: CatContent, Version: "1",
		Description: "Removes full-line comments and blank/trailing whitespace from YAML — structure preserved.",
		Kinds:       []string{"yaml"},
		ConfigSchema: []FieldSpec{
			{Key: "stripComments", Label: "Remove full-line comments", Type: "bool", Default: true},
			{Key: "maxBlank", Label: "Max consecutive blank lines", Type: "int", Default: 1},
		},
	}
}

func (o yamlOptimizer) Optimize(_ context.Context, in OptimizeInput) (OptimizeOutput, error) {
	cfg := mergeConfig(o.Meta(), in.Config)
	strip := cfgBool(cfg, "stripComments", true)
	var out []string
	dropped := 0
	for _, ln := range splitLines(string(in.Content)) {
		if strip && strings.HasPrefix(strings.TrimSpace(ln), "#") {
			dropped++
			continue
		}
		out = append(out, trimTrailing(ln))
	}
	out = collapseBlankRuns(out, cfgInt(cfg, "maxBlank", 1))
	notes := []string{}
	if dropped > 0 {
		notes = append(notes, "removed "+pluralN(dropped, "comment line"))
	}
	return result(in.Content, strings.Join(out, "\n"), notes...), nil
}

// xmlOptimizer strips XML/HTML comments and collapses inter-tag whitespace.
type xmlOptimizer struct{}

var (
	xmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	interTagWS = regexp.MustCompile(`>\s+<`)
)

func (xmlOptimizer) Meta() OptimizerMeta {
	return OptimizerMeta{
		ID: "xml", Name: "XML Optimizer", Category: CatContent, Version: "1",
		Description: "Removes XML comments and whitespace between tags.",
		Kinds:       []string{"xml", "html"},
		ConfigSchema: []FieldSpec{
			{Key: "stripComments", Label: "Remove comments", Type: "bool", Default: true},
			{Key: "collapseWhitespace", Label: "Collapse whitespace between tags", Type: "bool", Default: true},
		},
	}
}

func (o xmlOptimizer) Optimize(_ context.Context, in OptimizeInput) (OptimizeOutput, error) {
	cfg := mergeConfig(o.Meta(), in.Config)
	text := string(in.Content)
	notes := []string{}
	if cfgBool(cfg, "stripComments", true) {
		if n := len(xmlComment.FindAllString(text, -1)); n > 0 {
			text = xmlComment.ReplaceAllString(text, "")
			notes = append(notes, "removed "+pluralN(n, "comment"))
		}
	}
	if cfgBool(cfg, "collapseWhitespace", true) {
		text = interTagWS.ReplaceAllString(text, "><")
	}
	return result(in.Content, text, notes...), nil
}
