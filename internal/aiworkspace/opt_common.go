package aiworkspace

import (
	"fmt"
	"regexp"
	"strings"
)

// ansiRe matches ANSI/VT100 escape sequences (colors, cursor moves) found in logs/terminal output.
var ansiRe = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")

// stripANSI removes ANSI escape sequences.
func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// lastAfterCR keeps only the text after the final carriage return on a line (collapses progress-bar
// spam that overwrites itself with \r).
func lastAfterCR(s string) string {
	if i := strings.LastIndexByte(s, '\r'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// result builds an OptimizeOutput and measures token savings via the heuristic counter.
func result(before []byte, after string, notes ...string) OptimizeOutput {
	return OptimizeOutput{
		Content:      []byte(after),
		TokensBefore: EstimateTokens(string(before)),
		TokensAfter:  EstimateTokens(after),
		Notes:        notes,
	}
}

// splitLines splits on \n keeping content (no trailing empty from a final newline duplicated).
func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Split(s, "\n")
}

// trimTrailing removes trailing spaces/tabs from a line.
func trimTrailing(s string) string {
	return strings.TrimRight(s, " \t")
}

// collapseBlankRuns collapses runs of blank lines to at most maxBlank.
func collapseBlankRuns(lines []string, maxBlank int) []string {
	if maxBlank < 0 {
		maxBlank = 0
	}
	out := make([]string, 0, len(lines))
	blank := 0
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			blank++
			if blank > maxBlank {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, ln)
	}
	return out
}

func pluralN(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}
