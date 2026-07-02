package knowledge

import (
	"strings"
	"testing"
)

// seed builds a small deterministic corpus for prompt tests.
func seed(t *testing.T) *Brain {
	t.Helper()
	b := New(NewMemoryStore(), WithClock(fixedClock()))
	_, _ = b.Create("git-branch", "rule", "Branch discipline", "always create your own git branch off development", SourceHuman, StatusActive)
	_, _ = b.Create("merge-noff", "standard", "Merge policy", "merge with no-ff into development", SourceHuman, StatusActive)
	_, _ = b.Create("go-idiom", "pattern", "Error wrapping", "wrap errors with fmt.Errorf and %w", SourceCode, StatusActive)
	_, _ = b.Create("old-rule", "rule", "Legacy rule", "deprecated branch naming about git", SourceHuman, StatusActive)
	_, _ = b.Deprecate("old-rule") // now non-active
	return b
}

func TestSelectContextActiveOnlyByDefault(t *testing.T) {
	b := seed(t)
	got, err := b.SelectContext(Selector{Keywords: []string{"git"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range got {
		if e.Status != StatusActive {
			t.Errorf("non-active entry %q in default selection", e.ID)
		}
		if e.ID == "old-rule" {
			t.Error("deprecated old-rule must be excluded by default (invariant 4)")
		}
	}
}

func TestSelectContextIncludeNonActiveWhenRequested(t *testing.T) {
	b := seed(t)
	got, _ := b.SelectContext(Selector{Keywords: []string{"git"}, IncludeNonActive: true})
	found := false
	for _, e := range got {
		if e.ID == "old-rule" {
			found = true
		}
	}
	if !found {
		t.Error("old-rule should appear when IncludeNonActive is set")
	}
}

func TestSelectContextRanksByRelevance(t *testing.T) {
	b := seed(t)
	// "git" + "branch" match git-branch most strongly.
	got, _ := b.SelectContext(Selector{Keywords: []string{"git", "branch"}})
	if len(got) == 0 || got[0].ID != "git-branch" {
		t.Fatalf("top result = %v, want git-branch", ids(got))
	}
}

func TestSelectContextIsDeterministic(t *testing.T) {
	b := seed(t)
	sel := Selector{Keywords: []string{"git", "merge", "error"}, MaxEntries: 3}
	first, _ := b.SelectContext(sel)
	for i := 0; i < 20; i++ {
		again, _ := b.SelectContext(sel)
		if strings.Join(ids(first), ",") != strings.Join(ids(again), ",") {
			t.Fatalf("non-deterministic order: %v vs %v", ids(first), ids(again))
		}
		if RenderContext(first) != RenderContext(again) {
			t.Fatal("RenderContext not byte-stable")
		}
	}
}

func TestSelectContextCategoryFilter(t *testing.T) {
	b := seed(t)
	got, _ := b.SelectContext(Selector{Categories: []string{"pattern"}})
	if len(got) != 1 || got[0].ID != "go-idiom" {
		t.Errorf("category filter = %v, want [go-idiom]", ids(got))
	}
}

func TestSelectContextMaxEntries(t *testing.T) {
	b := seed(t)
	got, _ := b.SelectContext(Selector{MaxEntries: 2})
	if len(got) != 2 {
		t.Errorf("MaxEntries=2 returned %d", len(got))
	}
}

func TestSelectContextMaxBytes(t *testing.T) {
	b := seed(t)
	// budget for roughly one entry
	one := len(renderEntry(mustGet(t, b, "git-branch")))
	got, _ := b.SelectContext(Selector{Keywords: []string{"git", "merge", "error"}, MaxBytes: one + 5})
	total := 0
	for _, e := range got {
		total += len(renderEntry(e))
	}
	if len(got) == 0 {
		t.Fatal("MaxBytes produced empty selection")
	}
	if len(got) > 1 && total > one+5 {
		t.Errorf("byte budget exceeded: %d > %d", total, one+5)
	}
}

func TestMaxBytesAlwaysIncludesTopEntry(t *testing.T) {
	b := seed(t)
	// tiny budget below any single entry → still return the single most-relevant one.
	got, _ := b.SelectContext(Selector{Keywords: []string{"git", "branch"}, MaxBytes: 1})
	if len(got) != 1 || got[0].ID != "git-branch" {
		t.Errorf("tiny budget = %v, want [git-branch]", ids(got))
	}
}

func TestGrowthStats(t *testing.T) {
	b := seed(t)
	// bump one entry to 3 versions total
	body := "wrap errors with fmt.Errorf and %w always"
	_, _ = b.Update("go-idiom", Change{Body: &body})

	st, err := b.Growth(Selector{Keywords: []string{"git"}, MaxEntries: 1})
	if err != nil {
		t.Fatal(err)
	}
	if st.Entries != 4 {
		t.Errorf("entries = %d, want 4", st.Entries)
	}
	// versions: git-branch 1, merge-noff 1, go-idiom 2, old-rule 2 = 6
	if st.Versions != 6 {
		t.Errorf("versions = %d, want 6", st.Versions)
	}
	if st.Active != 3 {
		t.Errorf("active = %d, want 3 (old-rule deprecated)", st.Active)
	}
	if st.DuplicateIDs != 0 {
		t.Errorf("duplicate ids = %d, want 0 by construction", st.DuplicateIDs)
	}
	if st.PromptEntries != 1 {
		t.Errorf("prompt entries = %d, want 1", st.PromptEntries)
	}
	// prompt (1 entry) is smaller than the full active corpus (3 entries) → positive reduction.
	if st.ReductionRatio <= 0 {
		t.Errorf("reduction ratio = %v, want > 0", st.ReductionRatio)
	}
}

// BenchmarkSelectContext measures the deterministic selection hot path over a realistic corpus.
func BenchmarkSelectContext(b *testing.B) {
	brain := New(NewMemoryStore(), WithClock(fixedClock()))
	for i := 0; i < 500; i++ {
		id := "entry-" + itoa(i)
		_, _ = brain.Create(id, RecommendedCategories[i%len(RecommendedCategories)], "Title "+itoa(i),
			"knowledge body about git branch merge development testing deployment number "+itoa(i), SourceHuman, StatusActive)
	}
	sel := Selector{Keywords: []string{"git", "merge", "deployment"}, MaxEntries: 8, MaxBytes: 4096}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := brain.SelectContext(sel); err != nil {
			b.Fatal(err)
		}
	}
}

// TestPromptReductionOnLargeCorpus documents the reduction ratio the report cites.
func TestPromptReductionOnLargeCorpus(t *testing.T) {
	brain := New(NewMemoryStore(), WithClock(fixedClock()))
	for i := 0; i < 500; i++ {
		_, _ = brain.Create("e"+itoa(i), "rule", "T"+itoa(i),
			"body about git branch merge development number "+itoa(i), SourceHuman, StatusActive)
	}
	st, err := brain.Growth(Selector{Keywords: []string{"git", "merge"}, MaxEntries: 8, MaxBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if st.ReductionRatio < 0.9 {
		t.Errorf("reduction ratio = %.4f, want >= 0.9 on a 500-entry corpus", st.ReductionRatio)
	}
	t.Logf("corpus=%dB prompt=%dB entries=%d/%d reduction=%.4f",
		st.CorpusBytes, st.PromptBytes, st.PromptEntries, st.Active, st.ReductionRatio)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [12]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}

func ids(es []*Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.ID
	}
	return out
}

func mustGet(t *testing.T, b *Brain, id string) *Entry {
	e, ok, err := b.Get(id)
	if err != nil || !ok {
		t.Fatalf("Get %q: ok=%v err=%v", id, ok, err)
	}
	return e
}
