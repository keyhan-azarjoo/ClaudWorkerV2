package knowledge

import "sort"

// GrowthStats is the deterministic data behind the Knowledge Growth Report (S4 deliverable). Every
// field is derived from the store — nothing is estimated. Prompt-size figures are measured against a
// caller-supplied Selector so the report reflects a real prompt slice, not a guess.
type GrowthStats struct {
	Entries        int            `json:"entries"`         // distinct ids
	Active         int            `json:"active"`          // ids whose current status is active
	Versions       int            `json:"versions"`        // total version records across all ids
	AvgVersions    float64        `json:"avg_versions"`    // versions / entries
	ByCategory     map[string]int `json:"by_category"`     // current-entry count per category
	ByStatus       map[string]int `json:"by_status"`       // current-entry count per status
	BySource       map[string]int `json:"by_source"`       // current-entry count per source
	CorpusBytes    int            `json:"corpus_bytes"`    // rendered bytes of ALL active current entries
	PromptBytes    int            `json:"prompt_bytes"`    // rendered bytes of the selected prompt slice
	PromptEntries  int            `json:"prompt_entries"`  // entries in the selected slice
	ReductionRatio float64        `json:"reduction_ratio"` // 1 - prompt/corpus (0 if corpus empty)
	DuplicateIDs   int            `json:"duplicate_ids"`   // always 0 — ids are unique by construction (invariant 2)
}

// Growth computes the report against sel (the Selector the engine would use to build a prompt). The
// reduction ratio quantifies how much the deterministic Prompt Builder shrinks the full active
// corpus down to the relevant slice — the core value proposition of a deterministic Knowledge Brain.
func (b *Brain) Growth(sel Selector) (GrowthStats, error) {
	ids, err := b.store.IDs()
	if err != nil {
		return GrowthStats{}, err
	}
	st := GrowthStats{
		Entries:    len(ids),
		ByCategory: map[string]int{},
		ByStatus:   map[string]int{},
		BySource:   map[string]int{},
	}
	for _, id := range ids {
		hist, ok, err := b.store.History(id)
		if err != nil {
			return GrowthStats{}, err
		}
		if !ok {
			continue
		}
		st.Versions += len(hist)
		cur := hist[len(hist)-1]
		st.ByCategory[cur.Category]++
		st.ByStatus[string(cur.Status)]++
		st.BySource[string(cur.Source)]++
		if cur.Status == StatusActive {
			st.Active++
			st.CorpusBytes += len(renderEntry(cur))
		}
	}
	if st.Entries > 0 {
		st.AvgVersions = float64(st.Versions) / float64(st.Entries)
	}

	selected, err := b.SelectContext(sel)
	if err != nil {
		return GrowthStats{}, err
	}
	st.PromptEntries = len(selected)
	for _, e := range selected {
		st.PromptBytes += len(renderEntry(e))
	}
	if st.CorpusBytes > 0 {
		st.ReductionRatio = 1 - float64(st.PromptBytes)/float64(st.CorpusBytes)
		if st.ReductionRatio < 0 {
			st.ReductionRatio = 0
		}
	}
	return st, nil
}

// Categories returns the distinct categories currently in use, sorted — the LIVE vocabulary (which
// may include project-added categories beyond RecommendedCategories, invariant 1).
func (b *Brain) Categories() ([]string, error) {
	all, err := b.List()
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, e := range all {
		set[e.Category] = true
	}
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out, nil
}
