package aiworkspace

import (
	"context"
	"strings"
)

func init() { Register(gitdiffOptimizer{}) }

// gitdiffOptimizer shrinks a unified diff: it keeps file headers, hunk headers (@@) and changed (+/-)
// lines, drops noise (index/mode/similarity), and keeps at most N context lines around each change.
type gitdiffOptimizer struct{}

func (gitdiffOptimizer) Meta() OptimizerMeta {
	return OptimizerMeta{
		ID: "gitdiff", Name: "Git Diff Optimizer", Category: CatContent, Version: "1",
		Description: "Trims unified diffs to the changed lines plus minimal context — big savings on large PRs.",
		Kinds:       []string{"gitdiff"},
		ConfigSchema: []FieldSpec{
			{Key: "contextLines", Label: "Context lines to keep around changes", Type: "int", Default: 0},
			{Key: "dropIndexLines", Label: "Drop index/mode/similarity lines", Type: "bool", Default: true},
		},
	}
}

func isNoise(line string) bool {
	for _, p := range []string{"index ", "old mode ", "new mode ", "similarity index ", "rename from ", "rename to ", "dissimilarity index "} {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

func (o gitdiffOptimizer) Optimize(_ context.Context, in OptimizeInput) (OptimizeOutput, error) {
	cfg := mergeConfig(o.Meta(), in.Config)
	ctxN := cfgInt(cfg, "contextLines", 0)
	dropIdx := cfgBool(cfg, "dropIndexLines", true)

	lines := splitLines(string(in.Content))
	// Mark which lines to keep. Change lines (+/-) and structural headers always kept; context lines
	// kept only within ctxN of a change.
	keep := make([]bool, len(lines))
	isChange := func(s string) bool {
		return (strings.HasPrefix(s, "+") && !strings.HasPrefix(s, "+++")) ||
			(strings.HasPrefix(s, "-") && !strings.HasPrefix(s, "---"))
	}
	isHeader := func(s string) bool {
		return strings.HasPrefix(s, "diff --git") || strings.HasPrefix(s, "@@") ||
			strings.HasPrefix(s, "+++") || strings.HasPrefix(s, "---") ||
			strings.HasPrefix(s, "new file") || strings.HasPrefix(s, "deleted file") ||
			strings.HasPrefix(s, "Binary files")
	}
	for i, ln := range lines {
		if dropIdx && isNoise(ln) {
			continue
		}
		if isHeader(ln) || isChange(ln) {
			keep[i] = true
		}
	}
	if ctxN > 0 {
		for i, ln := range lines {
			if !isChange(ln) {
				continue
			}
			for d := 1; d <= ctxN; d++ {
				if i-d >= 0 {
					keep[i-d] = keep[i-d] || !isNoiseCtx(lines[i-d], dropIdx)
				}
				if i+d < len(lines) {
					keep[i+d] = keep[i+d] || !isNoiseCtx(lines[i+d], dropIdx)
				}
			}
		}
	}
	out := make([]string, 0, len(lines))
	dropped := 0
	for i, ln := range lines {
		if keep[i] {
			out = append(out, ln)
		} else if strings.TrimSpace(ln) != "" {
			dropped++
		}
	}
	notes := []string{"dropped " + pluralN(dropped, "context/noise line")}
	return result(in.Content, strings.Join(out, "\n"), notes...), nil
}

// isNoiseCtx keeps a context line unless it's diff noise we're dropping.
func isNoiseCtx(line string, dropIdx bool) bool {
	if dropIdx && isNoise(line) {
		return false
	}
	return true
}
