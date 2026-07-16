package orchestrator

import "testing"

func TestAccessNeeded(t *testing.T) {
	// Explicit marker → needed + the exact resource.
	if need, res := accessNeeded("blah\nACCESS-REQUEST: /Users/me/firmware — need the ESP32 repo\nmore"); !need || res != "/Users/me/firmware — need the ESP32 repo" {
		t.Fatalf("marker not parsed: need=%v res=%q", need, res)
	}
	// Heuristic signals → needed, resource unknown.
	for _, s := range []string{"the plan doc is outside my sandbox", "no ESP32/serial device available", "this is a backend-only repo, other repos not present"} {
		if need, _ := accessNeeded(s); !need {
			t.Fatalf("expected access-needed for %q", s)
		}
	}
	// Normal failure → not an access problem.
	if need, _ := accessNeeded("the build failed with a compile error"); need {
		t.Fatal("a build failure should not be classified as needing access")
	}
}
