package knowledge

import (
	"testing"
	"time"
)

// fixedClock returns a deterministic, advancing clock so timestamps are reproducible in tests.
func fixedClock() func() time.Time {
	t := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	return func() time.Time {
		cur := t
		t = t.Add(time.Second)
		return cur
	}
}

// brainFactories runs every Brain test against BOTH store implementations, proving the lifecycle is
// storage-agnostic (mirrors the S3 Store contract test).
func brainFactories(t *testing.T) map[string]func() *Brain {
	return map[string]func() *Brain{
		"file": func() *Brain {
			s, err := NewFileStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			return New(s, WithClock(fixedClock()))
		},
		"memory": func() *Brain { return New(NewMemoryStore(), WithClock(fixedClock())) },
	}
}

func TestCreateGetList(t *testing.T) {
	for name, make := range brainFactories(t) {
		t.Run(name, func(t *testing.T) {
			b := make()
			e, err := b.Create("arch-overview", "architecture", "Overview", "local-first engine", SourceHuman, StatusActive)
			if err != nil {
				t.Fatal(err)
			}
			if e.Version != 1 || e.CreatedAt == "" || e.CreatedAt != e.UpdatedAt {
				t.Fatalf("v1 record = %+v", e)
			}
			got, ok, err := b.Get("arch-overview")
			if err != nil || !ok {
				t.Fatalf("Get: ok=%v err=%v", ok, err)
			}
			if got.Title != "Overview" || got.Status != StatusActive {
				t.Errorf("Get = %+v", got)
			}
			list, _ := b.List()
			if len(list) != 1 {
				t.Errorf("List len = %d, want 1", len(list))
			}
		})
	}
}

func TestCreateRejectsDuplicateID(t *testing.T) {
	for name, make := range brainFactories(t) {
		t.Run(name, func(t *testing.T) {
			b := make()
			if _, err := b.Create("k", "rule", "A", "x", SourceHuman, StatusActive); err != nil {
				t.Fatal(err)
			}
			if _, err := b.Create("k", "rule", "B", "y", SourceHuman, StatusActive); err == nil {
				t.Error("second Create with same id must fail (ids are never duplicated)")
			}
			// the original is untouched
			got, _, _ := b.Get("k")
			if got.Title != "A" || got.Version != 1 {
				t.Errorf("duplicate Create mutated original: %+v", got)
			}
		})
	}
}

func TestUpdateAppendsVersionPreservingHistory(t *testing.T) {
	for name, make := range brainFactories(t) {
		t.Run(name, func(t *testing.T) {
			b := make()
			orig, _ := b.Create("k", "standard", "T1", "body1", SourceHuman, StatusActive)
			title := "T2"
			body := "body2"
			up, err := b.Update("k", Change{Title: &title, Body: &body})
			if err != nil {
				t.Fatal(err)
			}
			if up.Version != 2 {
				t.Errorf("update version = %d, want 2", up.Version)
			}
			if up.CreatedAt != orig.CreatedAt {
				t.Errorf("CreatedAt changed on update: %s -> %s", orig.CreatedAt, up.CreatedAt)
			}
			if up.UpdatedAt == orig.UpdatedAt {
				t.Errorf("UpdatedAt not advanced")
			}
			// carry-forward: category/source/status unchanged
			if up.Category != "standard" || up.Source != SourceHuman || up.Status != StatusActive {
				t.Errorf("carry-forward failed: %+v", up)
			}
			// history preserved: both versions present, ascending
			hist, ok, _ := b.History("k")
			if !ok || len(hist) != 2 || hist[0].Version != 1 || hist[1].Version != 2 {
				t.Fatalf("history = %+v", hist)
			}
			if hist[0].Title != "T1" || hist[1].Title != "T2" {
				t.Errorf("history content wrong: %q,%q", hist[0].Title, hist[1].Title)
			}
			// current == highest version
			cur, _, _ := b.Get("k")
			if cur.Version != 2 || cur.Title != "T2" {
				t.Errorf("current = %+v", cur)
			}
		})
	}
}

func TestLifecycleStatusTransitions(t *testing.T) {
	for name, make := range brainFactories(t) {
		t.Run(name, func(t *testing.T) {
			b := make()
			_, _ = b.Create("k", "rule", "T", "x", SourceHuman, StatusActive)
			if e, _ := b.Deprecate("k"); e.Status != StatusDeprecated || e.Version != 2 {
				t.Errorf("deprecate = %+v", e)
			}
			if e, _ := b.Archive("k"); e.Status != StatusArchived || e.Version != 3 {
				t.Errorf("archive = %+v", e)
			}
			if e, _ := b.Restore("k"); e.Status != StatusActive || e.Version != 4 {
				t.Errorf("restore = %+v", e)
			}
			// full history retained: 4 versions, nothing deleted
			hist, _, _ := b.History("k")
			if len(hist) != 4 {
				t.Errorf("history len = %d, want 4 (nothing deleted)", len(hist))
			}
		})
	}
}

func TestProposeCreatesDraftExcludedFromPrompt(t *testing.T) {
	for name, make := range brainFactories(t) {
		t.Run(name, func(t *testing.T) {
			b := make()
			d, err := b.Propose("ai-idea", "pattern", "Idea", "maybe useful", SourceCode)
			if err != nil {
				t.Fatal(err)
			}
			if d.Status != StatusDraft {
				t.Fatalf("propose status = %s, want draft", d.Status)
			}
			// a draft is invisible to the default prompt selector
			sel := Selector{Keywords: []string{"useful"}}
			got, _ := b.SelectContext(sel)
			if len(got) != 0 {
				t.Errorf("draft leaked into prompt: %+v", got)
			}
			// approval = restore → now selectable
			_, _ = b.Restore("ai-idea")
			got, _ = b.SelectContext(sel)
			if len(got) != 1 {
				t.Errorf("approved entry not selected: %+v", got)
			}
		})
	}
}

func TestValidationRejectsBadFields(t *testing.T) {
	b := New(NewMemoryStore(), WithClock(fixedClock()))
	cases := []struct {
		name           string
		id, cat, title string
		src            Source
		st             Status
	}{
		{"empty id", "", "rule", "T", SourceHuman, StatusActive},
		{"bad id char", "a/b", "rule", "T", SourceHuman, StatusActive},
		{"empty category", "k", "", "T", SourceHuman, StatusActive},
		{"empty title", "k", "rule", "", SourceHuman, StatusActive},
		{"bad source", "k", "rule", "T", Source("bogus"), StatusActive},
		{"bad status", "k", "rule", "T", SourceHuman, Status("bogus")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := b.Create(c.id, c.cat, c.title, "b", c.src, c.st); err == nil {
				t.Errorf("expected validation error for %s", c.name)
			}
		})
	}
}

func TestCustomCategoryAccepted(t *testing.T) {
	// invariant 1: a project may introduce a category outside RecommendedCategories with no code change.
	b := New(NewMemoryStore(), WithClock(fixedClock()))
	if _, err := b.Create("k", "deployment-runbook", "T", "x", SourceHuman, StatusActive); err != nil {
		t.Fatalf("custom category rejected: %v", err)
	}
	cats, _ := b.Categories()
	if len(cats) != 1 || cats[0] != "deployment-runbook" {
		t.Errorf("live categories = %v", cats)
	}
}
