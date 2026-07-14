package aiworkspace

import (
	"context"
	"fmt"
	"path"
	"strings"
)

func init() {
	Register(largeFileOptimizer{})
	Register(generatedFilter{})
	Register(binaryFilter{})
}

// largeFileOptimizer truncates very long content to a head + tail with an elision marker (keeps the
// useful ends, drops the bulky middle).
type largeFileOptimizer struct{}

func (largeFileOptimizer) Meta() OptimizerMeta {
	return OptimizerMeta{
		ID: "largefile", Name: "Large File Optimizer", Category: CatContent, Version: "1",
		Description: "Truncates long content to head + tail with an elision marker.",
		Kinds:       []string{"text", "log"},
		ConfigSchema: []FieldSpec{
			{Key: "headLines", Label: "Head lines to keep", Type: "int", Default: 40},
			{Key: "tailLines", Label: "Tail lines to keep", Type: "int", Default: 20},
		},
	}
}

func (o largeFileOptimizer) Optimize(_ context.Context, in OptimizeInput) (OptimizeOutput, error) {
	cfg := mergeConfig(o.Meta(), in.Config)
	head := cfgInt(cfg, "headLines", 40)
	tail := cfgInt(cfg, "tailLines", 20)
	if head < 0 {
		head = 0
	}
	if tail < 0 {
		tail = 0
	}
	lines := splitLines(string(in.Content))
	if len(lines) <= head+tail+1 {
		return result(in.Content, string(in.Content), "under threshold — unchanged"), nil
	}
	omitted := len(lines) - head - tail
	out := make([]string, 0, head+tail+1)
	out = append(out, lines[:head]...)
	out = append(out, fmt.Sprintf("… %s omitted …", pluralN(omitted, "line")))
	out = append(out, lines[len(lines)-tail:]...)
	return result(in.Content, strings.Join(out, "\n"), "kept head+tail"), nil
}

// generatedFilter drops generated/lock/minified files from a path listing (one path per line).
type generatedFilter struct{}

func (generatedFilter) Meta() OptimizerMeta {
	return OptimizerMeta{
		ID: "generated", Name: "Generated File Filter", Category: CatFilter, Version: "1",
		Description: "Removes generated/lock/minified files (lockfiles, *.min.js, *.map, *.pb.go, …) from a file listing.",
		Kinds:       []string{"tree"},
		ConfigSchema: []FieldSpec{
			{Key: "extra", Label: "Extra patterns (comma-separated, substring match)", Type: "string", Default: ""},
		},
	}
}

var generatedNames = []string{
	"package-lock.json", "yarn.lock", "pnpm-lock.yaml", "go.sum", "cargo.lock", "composer.lock", "poetry.lock", "gemfile.lock",
}
var generatedSuffix = []string{".min.js", ".min.css", ".map", ".pb.go", ".generated.go", ".g.dart", ".lock", "-lock.json"}

func (o generatedFilter) Optimize(_ context.Context, in OptimizeInput) (OptimizeOutput, error) {
	cfg := mergeConfig(o.Meta(), in.Config)
	var extra []string
	for _, s := range strings.Split(cfgString(cfg, "extra", ""), ",") {
		if s = strings.TrimSpace(s); s != "" {
			extra = append(extra, strings.ToLower(s))
		}
	}
	isGenerated := func(p string) bool {
		lp := strings.ToLower(strings.TrimSpace(p))
		base := strings.ToLower(path.Base(strings.Trim(lp, "/")))
		for _, n := range generatedNames {
			if base == n {
				return true
			}
		}
		for _, sfx := range generatedSuffix {
			if strings.HasSuffix(lp, sfx) {
				return true
			}
		}
		for _, e := range extra {
			if strings.Contains(lp, e) {
				return true
			}
		}
		return false
	}
	var out []string
	dropped := 0
	for _, ln := range splitLines(string(in.Content)) {
		if isGenerated(ln) {
			dropped++
			continue
		}
		out = append(out, ln)
	}
	notes := []string{}
	if dropped > 0 {
		notes = append(notes, "dropped "+pluralN(dropped, "generated file"))
	}
	return result(in.Content, strings.Join(out, "\n"), notes...), nil
}

// binaryFilter replaces binary-looking content with a compact placeholder (NUL byte or a high ratio of
// non-printable bytes ⇒ binary). Text passes through unchanged.
type binaryFilter struct{}

func (binaryFilter) Meta() OptimizerMeta {
	return OptimizerMeta{
		ID: "binary", Name: "Binary Filter", Category: CatFilter, Version: "1",
		Description:  "Replaces binary content with a short placeholder so it never wastes context tokens.",
		Kinds:        []string{"text"},
		ConfigSchema: []FieldSpec{},
	}
}

func looksBinary(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	nonPrint := 0
	n := len(b)
	if n > 8192 {
		n = 8192
	}
	for i := 0; i < n; i++ {
		c := b[i]
		if c == 0 {
			return true
		}
		if c < 9 || (c > 13 && c < 32) {
			nonPrint++
		}
	}
	return float64(nonPrint)/float64(n) > 0.3
}

func (binaryFilter) Optimize(_ context.Context, in OptimizeInput) (OptimizeOutput, error) {
	if looksBinary(in.Content) {
		placeholder := fmt.Sprintf("[binary content omitted: %d bytes]", len(in.Content))
		return result(in.Content, placeholder, "binary detected — replaced with placeholder"), nil
	}
	return result(in.Content, string(in.Content), "not binary — unchanged"), nil
}
