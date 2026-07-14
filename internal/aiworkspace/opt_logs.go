package aiworkspace

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

func init() {
	Register(logOptimizer{})
	Register(stacktraceOptimizer{})
	Register(terminalOptimizer{})
}

// logOptimizer strips ANSI codes and collapses runs of identical log lines into "<line> (×N)".
type logOptimizer struct{}

func (logOptimizer) Meta() OptimizerMeta {
	return OptimizerMeta{
		ID: "log", Name: "Log Optimizer", Category: CatContent, Version: "1",
		Description: "Removes ANSI colors and collapses repeated identical log lines.",
		Kinds:       []string{"log"},
		ConfigSchema: []FieldSpec{
			{Key: "stripAnsi", Label: "Strip ANSI color codes", Type: "bool", Default: true},
			{Key: "collapseRepeats", Label: "Collapse repeated lines", Type: "bool", Default: true},
		},
	}
}

func (o logOptimizer) Optimize(_ context.Context, in OptimizeInput) (OptimizeOutput, error) {
	cfg := mergeConfig(o.Meta(), in.Config)
	stripA := cfgBool(cfg, "stripAnsi", true)
	collapse := cfgBool(cfg, "collapseRepeats", true)

	lines := splitLines(string(in.Content))
	for i := range lines {
		if stripA {
			lines[i] = stripANSI(lines[i])
		}
		lines[i] = trimTrailing(lines[i])
	}
	if !collapse {
		return result(in.Content, strings.Join(lines, "\n")), nil
	}
	var out []string
	i := 0
	saved := 0
	for i < len(lines) {
		j := i + 1
		for j < len(lines) && lines[j] == lines[i] && strings.TrimSpace(lines[i]) != "" {
			j++
		}
		n := j - i
		if n > 1 {
			out = append(out, fmt.Sprintf("%s  (×%d)", lines[i], n))
			saved += n - 1
		} else {
			out = append(out, lines[i])
		}
		i = j
	}
	notes := []string{}
	if saved > 0 {
		notes = append(notes, "collapsed "+pluralN(saved, "repeated line"))
	}
	return result(in.Content, strings.Join(out, "\n"), notes...), nil
}

// stacktraceOptimizer condenses stack traces: keeps the top N frames and drops vendor/stdlib frames,
// leaving a marker for the omitted ones. Non-frame lines (the error message) are always kept.
type stacktraceOptimizer struct{}

var frameRe = regexp.MustCompile(`^\s*(at\s|File\s+"|from\s|#\d+\s)`)
var vendorRe = regexp.MustCompile(`node_modules|site-packages|/usr/lib|/usr/local/|GOROOT|runtime/|\.cargo/`)

func (stacktraceOptimizer) Meta() OptimizerMeta {
	return OptimizerMeta{
		ID: "stacktrace", Name: "Stack Trace Optimizer", Category: CatContent, Version: "1",
		Description: "Keeps the top frames and prunes vendor/stdlib frames from long stack traces.",
		Kinds:       []string{"stacktrace", "log"},
		ConfigSchema: []FieldSpec{
			{Key: "keepFrames", Label: "Top frames to keep", Type: "int", Default: 8},
			{Key: "dropVendor", Label: "Drop vendor/stdlib frames", Type: "bool", Default: true},
		},
	}
}

func (o stacktraceOptimizer) Optimize(_ context.Context, in OptimizeInput) (OptimizeOutput, error) {
	cfg := mergeConfig(o.Meta(), in.Config)
	keep := cfgInt(cfg, "keepFrames", 8)
	dropVendor := cfgBool(cfg, "dropVendor", true)

	var out []string
	frameCount := 0
	dropped := 0
	pendingMarker := false
	for _, ln := range splitLines(string(in.Content)) {
		if frameRe.MatchString(ln) {
			frameCount++
			if frameCount > keep || (dropVendor && vendorRe.MatchString(ln)) {
				dropped++
				pendingMarker = true
				continue
			}
			out = append(out, trimTrailing(ln))
			continue
		}
		// Non-frame line: flush a marker for any dropped frames, then keep it.
		if pendingMarker {
			out = append(out, fmt.Sprintf("    … %s omitted", pluralN(dropped, "frame")))
			pendingMarker = false
			dropped = 0
		}
		frameCount = 0
		out = append(out, trimTrailing(ln))
	}
	if pendingMarker {
		out = append(out, fmt.Sprintf("    … %s omitted", pluralN(dropped, "frame")))
	}
	return result(in.Content, strings.Join(out, "\n")), nil
}

// terminalOptimizer strips ANSI and collapses carriage-return progress spam (keeps the final state of
// each self-overwriting line).
type terminalOptimizer struct{}

func (terminalOptimizer) Meta() OptimizerMeta {
	return OptimizerMeta{
		ID: "terminal", Name: "Terminal Output Optimizer", Category: CatContent, Version: "1",
		Description: "Strips ANSI codes and progress-bar carriage-return spam from captured terminal output.",
		Kinds:       []string{"terminal", "log"},
		ConfigSchema: []FieldSpec{
			{Key: "stripAnsi", Label: "Strip ANSI codes", Type: "bool", Default: true},
		},
	}
}

func (o terminalOptimizer) Optimize(_ context.Context, in OptimizeInput) (OptimizeOutput, error) {
	cfg := mergeConfig(o.Meta(), in.Config)
	stripA := cfgBool(cfg, "stripAnsi", true)
	text := string(in.Content)
	if stripA {
		text = stripANSI(text)
	}
	lines := splitLines(text)
	for i := range lines {
		lines[i] = trimTrailing(lastAfterCR(lines[i]))
	}
	return result(in.Content, strings.Join(collapseBlankRuns(lines, 1), "\n")), nil
}
