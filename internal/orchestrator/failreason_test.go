package orchestrator

import (
	"strings"
	"testing"
)

func TestSimpleFailReason(t *testing.T) {
	cases := []struct {
		in, wantContains string
	}{
		{"Failed to authenticate: OAuth session expired and could not be refreshed", "logged in"},
		{"You've hit your session limit · resets 4:40pm", "usage limit"},
		{"the Pi is offline and I have no physical access to power-cycle it", "physical device"},
		{"no ESP32/serial device or firmware source available to flash", "firmware"},
		{"the plan doc is outside my sandbox; this is a backend-only repo", "other repos"},
		{"merge conflict — the branch overlaps changes another agent pushed", "merge conflict"},
		{`{"ok":false,"summary":"could not complete","files":[]}`, "nothing to merge"},
		{"project is deactivated: no active repo", "turned off"},
	}
	for _, c := range cases {
		got := strings.ToLower(simpleFailReason(c.in, ""))
		if !strings.Contains(got, c.wantContains) {
			t.Errorf("simpleFailReason(%.40q) = %q, want it to contain %q", c.in, got, c.wantContains)
		}
	}
	// Fallback: unknown output → the technical reason.
	if got := simpleFailReason("some unrecognised output", "merge lease held"); got != "merge lease held" {
		t.Fatalf("fallback should return the technical reason, got %q", got)
	}
	// Empty everything → a sane default.
	if got := simpleFailReason("", ""); got == "" {
		t.Fatal("expected a non-empty default reason")
	}
}
