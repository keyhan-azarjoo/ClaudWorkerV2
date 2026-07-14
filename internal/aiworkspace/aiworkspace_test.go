package aiworkspace

import "testing"

// TestProviderLifecycle covers add → default promotion → enable/disable → remove without touching the
// keychain (no keys added), so it runs anywhere.
func TestProviderLifecycle(t *testing.T) {
	dir := t.TempDir()
	svc := New(dir)

	// First provider becomes default automatically.
	p1, err := svc.AddProvider("ollama", "", "")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !p1.IsDefault {
		t.Fatal("first provider should be default")
	}
	if p1.Name != "Ollama (local)" || p1.BaseURL != "http://localhost:11434" {
		t.Fatalf("kind defaults not applied: %+v", p1)
	}

	// Second provider, then make it default.
	p2, _ := svc.AddProvider("anthropic", "Claude", "")
	svc.SetDefault(p2.ID)
	list := svc.providers.load()
	for _, p := range list {
		if p.ID == p2.ID && !p.IsDefault {
			t.Fatal("setDefault did not stick")
		}
		if p.ID == p1.ID && p.IsDefault {
			t.Fatal("old default not cleared")
		}
	}

	// Disabling the default moves default elsewhere.
	svc.SetEnabled(p2.ID, false)
	if !anyDefault(svc.providers.load()) {
		t.Fatal("no default after disabling the default provider")
	}

	// Removing the default promotes a remaining enabled provider.
	svc.RemoveProvider(p2.ID)
	rest := svc.providers.load()
	if len(rest) != 1 || !rest[0].IsDefault {
		t.Fatalf("remove should leave one provider that is default: %+v", rest)
	}
}

// TestPublicViewMasksSecrets ensures the client-facing view never leaks the keychain reference.
func TestPublicViewMasksSecrets(t *testing.T) {
	ps := []Provider{{
		ID: "p1", Kind: "openai", Name: "OpenAI", Enabled: true,
		Accounts: []Account{{ID: "a1", Label: "work", SecretRef: "cw-aiw-p1-a1", KeyHint: "1234"}},
	}}
	view := publicView(ps)
	acct := view[0]["accounts"].([]map[string]any)[0]
	if _, leaked := acct["secretRef"]; leaked {
		t.Fatal("secretRef must not be exposed to the client")
	}
	if acct["hasKey"] != true {
		t.Fatal("hasKey should be true when a SecretRef exists")
	}
	if acct["keyHint"] != "1234" {
		t.Fatalf("keyHint should pass through for masked display, got %v", acct["keyHint"])
	}
}

// TestUsageSummaryEmpty — a fresh store reports honest zeros with a 14-point sparkline window.
func TestUsageSummaryEmpty(t *testing.T) {
	svc := New(t.TempDir())
	s := svc.UsageSummary()
	if s.Events != 0 || s.TodayTokens != 0 || s.MonthTokens != 0 {
		t.Fatalf("empty store should be all zero: %+v", s)
	}
	if len(s.Days) != 14 {
		t.Fatalf("expected 14-day sparkline window, got %d", len(s.Days))
	}
}

// TestUsageRecordRollsUp — a recorded event lands in today + month totals and the sparkline tail.
func TestUsageRecordRollsUp(t *testing.T) {
	svc := New(t.TempDir())
	svc.RecordUsage(UsageEvent{InputTok: 100, OutputTok: 50, SavedTok: 30})
	s := svc.UsageSummary()
	if s.Events != 1 || s.TodayTokens != 150 || s.MonthTokens != 150 || s.TodaySaved != 30 {
		t.Fatalf("rollup wrong: %+v", s)
	}
	if s.Days[len(s.Days)-1].Tokens != 150 {
		t.Fatalf("today should be the last sparkline point: %+v", s.Days[len(s.Days)-1])
	}
}

// TestHeuristicTokens — the estimator is monotonic and non-zero for real text.
func TestHeuristicTokens(t *testing.T) {
	if EstimateTokens("") != 0 {
		t.Fatal("empty string is 0 tokens")
	}
	small := EstimateTokens("hello world")
	big := EstimateTokens("hello world this is a much longer sentence with more tokens in it")
	if small == 0 || big <= small {
		t.Fatalf("estimator not monotonic: small=%d big=%d", small, big)
	}
}
