package secrets

import "testing"

func TestEnvKey(t *testing.T) {
	cases := map[string]string{
		"jira_token": "JIRA_TOKEN",
		"tg-chat":    "TG_CHAT",
		"Mixed.Case": "MIXED_CASE",
	}
	for in, want := range cases {
		if got := envKey(in); got != want {
			t.Errorf("envKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveViaEnv(t *testing.T) {
	t.Setenv("JIRA_TOKEN", "s3cr3t")
	r := &Resolver{env: envLookup} // env-only resolver
	v, err := r.Resolve("jira_token")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if v != "s3cr3t" {
		t.Errorf("value = %q, want s3cr3t", v)
	}
	if !r.CanResolve("jira_token") {
		t.Error("CanResolve should be true")
	}
	if r.CanResolve("nope_missing") {
		t.Error("CanResolve should be false for missing secret")
	}
}

func TestResolveOrderPrefersFirstProvider(t *testing.T) {
	r := &Resolver{
		keychain: func(string) (string, bool) { return "", false },
		azure:    func(string) (string, bool) { return "from-azure", true },
		env:      func(string) (string, bool) { return "from-env", true },
	}
	v, err := r.Resolve("x")
	if err != nil {
		t.Fatal(err)
	}
	if v != "from-azure" {
		t.Errorf("expected azure to win over env, got %q", v)
	}
}

func TestResolveNotFound(t *testing.T) {
	r := &Resolver{env: func(string) (string, bool) { return "", false }}
	if _, err := r.Resolve("missing"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
