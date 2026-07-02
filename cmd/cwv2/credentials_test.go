package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/myotgo/ClaudWorkerV2/internal/config"
)

// TestCredHealthNeverExposesValues is the security guard: the credential health view must never
// contain a secret VALUE, nor a "value"/"masked" field, in either snapshot() or validate().
func TestCredHealthNeverExposesValues(t *testing.T) {
	const secret = "supersecretvalue-should-never-appear-123"
	t.Setenv("CWV2_CREDENTIAL_KEYS", "TEST_SECRET_X, ATLASSIAN_TOKEN")
	t.Setenv("TEST_SECRET_X", secret)
	t.Setenv("ATLASSIAN_TOKEN", secret)

	ch := newCredHealth(config.Config{}, nil)

	for _, out := range []any{ch.snapshot(), ch.validate(context.Background())} {
		b, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		if strings.Contains(s, secret) {
			t.Fatalf("secret value leaked in credentials output: %s", s)
		}
		if strings.Contains(s, `"value"`) || strings.Contains(s, `"masked"`) {
			t.Fatalf("forbidden value/masked field present: %s", s)
		}
		// The name is fine to show; the status must be reported.
		if !strings.Contains(s, "TEST_SECRET_X") || !strings.Contains(s, "Resolved") {
			t.Errorf("expected name + Resolved status in output: %s", s)
		}
	}
}
