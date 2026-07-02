package knowledge

import (
	"sort"
	"strings"
	"unicode"
)

// Selector is the deterministic input to the Prompt Builder. Given the same Selector and the same
// Brain contents, SelectContext ALWAYS returns the same entries in the same order (P9): selection is
// pure Go — no embeddings, no vectors, no AI ranking, zero model tokens (invariant 4/5, Law 18).
type Selector struct {
	Keywords         []string // relevance terms (e.g. from the issue summary + acceptance criteria)
	Context          []string // explicit project context (e.g. file paths, module/symbol names)
	Categories       []string // include-only category filter; empty = all categories
	IncludeNonActive bool     // false (default) → active entries only; true → include any status
	MaxEntries       int      // cap on selected entries; <=0 = unlimited
	MaxBytes         int      // cap on total rendered bytes; <=0 = unlimited
}

// scored pairs an entry with its deterministic relevance score.
type scored struct {
	e     *Entry
	score int
}

// SelectContext returns the knowledge entries for a prompt, most-relevant first, within the entry
// and byte budgets. It is the deterministic core; a future optional search plugin could re-rank the
// SAME candidate set (see candidates/rank split) without the core ever depending on it (invariant 7).
func (b *Brain) SelectContext(sel Selector) ([]*Entry, error) {
	cands, err := b.candidates(sel)
	if err != nil {
		return nil, err
	}
	ranked := rank(cands, sel)
	return budget(ranked, sel), nil
}

// candidates gathers the current entries eligible under the status + category filters. This is the
// pluggable seam: it produces the set; ranking decides order. A search plugin would replace rank,
// not candidates, and the core default (rank) stands alone.
func (b *Brain) candidates(sel Selector) ([]*Entry, error) {
	all, err := b.List()
	if err != nil {
		return nil, err
	}
	cats := toSet(sel.Categories)
	out := make([]*Entry, 0, len(all))
	for _, e := range all {
		if !sel.IncludeNonActive && e.Status != StatusActive {
			continue // invariant 4: active-only unless explicitly requested
		}
		if len(cats) > 0 && !cats[normalise(e.Category)] {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// rank scores each candidate by keyword+context term overlap and returns a fully deterministic
// ordering: score desc, then category asc, then id asc (ids are unique, so the order is total).
func rank(cands []*Entry, sel Selector) []*Entry {
	terms := terms(sel.Keywords, sel.Context)
	scoredList := make([]scored, len(cands))
	for i, e := range cands {
		scoredList[i] = scored{e: e, score: relevance(e, terms)}
	}
	sort.SliceStable(scoredList, func(i, j int) bool {
		a, b := scoredList[i], scoredList[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.e.Category != b.e.Category {
			return a.e.Category < b.e.Category
		}
		return a.e.ID < b.e.ID
	})
	out := make([]*Entry, len(scoredList))
	for i, s := range scoredList {
		out[i] = s.e
	}
	return out
}

// budget trims a ranked list to the entry-count and byte budgets. Byte cost is the entry's rendered
// size, so the budget matches what actually reaches the prompt.
func budget(ranked []*Entry, sel Selector) []*Entry {
	out := make([]*Entry, 0, len(ranked))
	total := 0
	for _, e := range ranked {
		if sel.MaxEntries > 0 && len(out) >= sel.MaxEntries {
			break
		}
		size := len(renderEntry(e))
		if sel.MaxBytes > 0 && total+size > sel.MaxBytes {
			if len(out) == 0 {
				// Always include at least the single most-relevant entry, even if it alone
				// exceeds MaxBytes — an empty context is worse than one over-budget slice.
				out = append(out, e)
			}
			break
		}
		out = append(out, e)
		total += size
	}
	return out
}

// relevance counts how many query terms appear in the entry's searchable text. Word-boundary
// matching keeps it stable and avoids substring false-positives ("cat" in "concatenate").
func relevance(e *Entry, terms map[string]bool) int {
	if len(terms) == 0 {
		return 0
	}
	haystack := tokenize(e.Title + " " + e.Body + " " + e.Category)
	seen := map[string]bool{}
	for _, w := range haystack {
		if terms[w] {
			seen[w] = true
		}
	}
	return len(seen) // distinct matched terms; repetition doesn't inflate the score
}

// terms builds the lower-cased query-term set from keywords + context.
func terms(groups ...[]string) map[string]bool {
	out := map[string]bool{}
	for _, g := range groups {
		for _, raw := range g {
			for _, w := range tokenize(raw) {
				out[w] = true
			}
		}
	}
	return out
}

// tokenize splits text into lower-cased word tokens on non-alphanumeric boundaries.
func tokenize(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func toSet(items []string) map[string]bool {
	if len(items) == 0 {
		return nil
	}
	out := make(map[string]bool, len(items))
	for _, i := range items {
		out[normalise(i)] = true
	}
	return out
}

// RenderContext renders selected entries into the deterministic knowledge slice of a prompt. Same
// entries → identical string (byte-for-byte), so prompts are reproducible.
func RenderContext(entries []*Entry) string {
	if len(entries) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## Knowledge\n")
	for _, e := range entries {
		sb.WriteString(renderEntry(e))
	}
	return sb.String()
}

// renderEntry is the canonical single-entry rendering (also the unit of the byte budget).
func renderEntry(e *Entry) string {
	var sb strings.Builder
	sb.WriteString("\n### [")
	sb.WriteString(e.Category)
	sb.WriteString("] ")
	sb.WriteString(e.Title)
	sb.WriteString("\n")
	sb.WriteString(strings.TrimRight(e.Body, "\n"))
	sb.WriteString("\n")
	return sb.String()
}
