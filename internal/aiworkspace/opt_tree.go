package aiworkspace

import (
	"context"
	"path"
	"strings"
)

func init() { Register(treeOptimizer{}) }

// treeOptimizer compacts a file/directory listing (one path per line): it drops ignored directories and
// caps the number of entries shown per directory, summarizing the remainder ("… +N more").
type treeOptimizer struct{}

func (treeOptimizer) Meta() OptimizerMeta {
	return OptimizerMeta{
		ID: "tree", Name: "Directory Tree Optimizer", Category: CatContent, Version: "1",
		Description: "Prunes noisy directories and caps entries per folder in file listings.",
		Kinds:       []string{"tree"},
		ConfigSchema: []FieldSpec{
			{Key: "maxPerDir", Label: "Max entries shown per directory", Type: "int", Default: 20},
			{Key: "ignore", Label: "Ignored path segments (comma-separated)", Type: "string", Default: "node_modules,.git,dist,build,.next,vendor,__pycache__,.venv,coverage"},
		},
	}
}

func (o treeOptimizer) Optimize(_ context.Context, in OptimizeInput) (OptimizeOutput, error) {
	cfg := mergeConfig(o.Meta(), in.Config)
	maxPer := cfgInt(cfg, "maxPerDir", 20)
	if maxPer < 1 {
		maxPer = 1
	}
	ignore := map[string]bool{}
	for _, s := range strings.Split(cfgString(cfg, "ignore", ""), ",") {
		if s = strings.TrimSpace(s); s != "" {
			ignore[s] = true
		}
	}

	lines := splitLines(string(in.Content))
	perDir := map[string]int{}
	out := make([]string, 0, len(lines))
	prunedDirs, cappedDirs := 0, 0
	seenIgnoredMsg := map[string]bool{}
	seenCapMsg := map[string]bool{}

	for _, ln := range lines {
		raw := strings.TrimSpace(ln)
		if raw == "" {
			out = append(out, ln)
			continue
		}
		// Ignore if any path segment is in the ignore set; collapse the WHOLE ignored subtree (keyed by
		// the path up to and including the ignored segment) to a single line — so many siblings under one
		// ignored root produce one summary, never one-per-parent.
		segs := strings.Split(strings.Trim(raw, "/"), "/")
		ignoredAt := -1
		for i, seg := range segs {
			if ignore[seg] {
				ignoredAt = i
				break
			}
		}
		if ignoredAt >= 0 {
			root := strings.Join(segs[:ignoredAt+1], "/")
			if !seenIgnoredMsg[root] {
				out = append(out, indentOf(ln)+root+"/ … (pruned)")
				seenIgnoredMsg[root] = true
				prunedDirs++
			}
			continue
		}
		if strings.Contains(raw, "/") {
			dir := path.Dir(raw)
			perDir[dir]++
			if perDir[dir] > maxPer {
				if !seenCapMsg[dir] {
					out = append(out, indentOf(ln)+"… +more in "+dir)
					seenCapMsg[dir] = true
					cappedDirs++
				}
				continue
			}
		}
		out = append(out, ln)
	}
	notes := []string{}
	if prunedDirs > 0 {
		notes = append(notes, "pruned "+pluralN(prunedDirs, "ignored dir"))
	}
	if cappedDirs > 0 {
		notes = append(notes, "capped "+pluralN(cappedDirs, "large dir"))
	}
	return result(in.Content, strings.Join(out, "\n"), notes...), nil
}

// indentOf returns the leading whitespace of a line (to keep summary lines aligned).
func indentOf(s string) string {
	return s[:len(s)-len(strings.TrimLeft(s, " \t"))]
}
