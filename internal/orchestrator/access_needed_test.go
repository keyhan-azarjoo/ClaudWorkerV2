package orchestrator

import "testing"

func TestAccessNeeded(t *testing.T) {
	// Explicit marker with a REAL path → clean path + the "why" as detail.
	need, res, detail := accessNeeded("blah\nACCESS-REQUEST: /Users/me/firmware — need the ESP32 repo\nmore")
	if !need || res != "/Users/me/firmware" || detail != "need the ESP32 repo" {
		t.Fatalf("marker not parsed cleanly: need=%v res=%q detail=%q", need, res, detail)
	}

	// The real-world bug: a PLACEHOLDER path + why, embedded in a JSON result line. resource must be
	// empty (not the garbage string) so the UI doesn't prefill a non-path.
	need, res, detail = accessNeeded(`{"ok":false,"summary":"ACCESS-REQUEST: /<ESP32 extension firmware repo + a board> — required to build+deploy on real hardware.","files":[]}`)
	if !need {
		t.Fatal("should still be recognised as needing access")
	}
	if res != "" {
		t.Fatalf("placeholder path must yield an EMPTY resource, got %q", res)
	}
	if detail == "" || detail[0] == '/' {
		t.Fatalf("detail should be the human 'why', got %q", detail)
	}

	// A repo URL is a valid clean resource.
	if _, r, _ := accessNeeded("ACCESS-REQUEST: https://github.com/x/y.git — the firmware repo"); r != "https://github.com/x/y.git" {
		t.Fatalf("URL resource not parsed: %q", r)
	}

	// Heuristic signals → needed, resource unknown.
	for _, s := range []string{"the plan doc is outside my sandbox", "no ESP32/serial device available"} {
		if n, _, _ := accessNeeded(s); !n {
			t.Fatalf("expected access-needed for %q", s)
		}
	}
	// Normal failure → not an access problem.
	if n, _, _ := accessNeeded("the build failed with a compile error"); n {
		t.Fatal("a build failure should not be classified as needing access")
	}
}
