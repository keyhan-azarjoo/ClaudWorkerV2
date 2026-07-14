package aiworkspace

import "strings"

// TokenCounter estimates a token count for text. The core ships only a fast heuristic (no tokenizer
// dependency); an exact tokenizer can be injected later (the future local companion) behind this
// interface without changing callers.
type TokenCounter interface {
	Count(text string) int
}

// heuristicCounter approximates tokens as ~4 chars/token with a small word-boundary correction. This is
// deliberately an ESTIMATE — usage/savings figures derived from it are labelled as estimates in the UI.
type heuristicCounter struct{}

func (heuristicCounter) Count(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	chars := len([]rune(text))
	words := len(strings.Fields(text))
	// Blend char/4 and word*1.3; clamp to at least the word count.
	est := (chars/4 + (words*13)/10) / 2
	if est < words {
		est = words
	}
	return est
}

// DefaultCounter is the process-wide token estimator.
var DefaultCounter TokenCounter = heuristicCounter{}

// EstimateTokens is a convenience wrapper over DefaultCounter.
func EstimateTokens(text string) int { return DefaultCounter.Count(text) }
