package resource

import "testing"

// TestSetPausedExcludesFromSelection guards the operator pause (V1 parity): a paused account must
// never be selected for work, its availability reads "paused", and resuming restores selection.
func TestSetPausedExcludesFromSelection(t *testing.T) {
	m := New()
	m.Register(Resource{ID: "a", Kind: KindClaudeAccount, Name: "a", Health: HealthHealthy})

	if _, ok := m.Select(Filter{Kind: KindClaudeAccount}); !ok {
		t.Fatal("healthy account should be selectable")
	}

	if !m.SetPaused("a", true) {
		t.Fatal("SetPaused should find the account")
	}
	if m.SetPaused("missing", true) {
		t.Error("SetPaused should return false for unknown id")
	}
	if !m.IsPaused("a") {
		t.Error("IsPaused should report true after pause")
	}
	if _, ok := m.Select(Filter{Kind: KindClaudeAccount}); ok {
		t.Fatal("paused account must NOT be selected")
	}
	if av, _ := m.AvailabilityOf("a"); av != Paused {
		t.Errorf("availability = %q, want paused", av)
	}

	m.SetPaused("a", false)
	if _, ok := m.Select(Filter{Kind: KindClaudeAccount}); !ok {
		t.Fatal("resumed account should be selectable again")
	}
}
