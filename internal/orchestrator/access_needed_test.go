package orchestrator

import "testing"

func TestDetectRequest(t *testing.T) {
	// ACCESS marker with a REAL path → access kind + clean path + why.
	need, kind, res, detail := detectRequest("blah\nACCESS-REQUEST: /Users/me/firmware — need the ESP32 repo\nx")
	if !need || kind != "access" || res != "/Users/me/firmware" || detail != "need the ESP32 repo" {
		t.Fatalf("access marker: need=%v kind=%q res=%q detail=%q", need, kind, res, detail)
	}

	// Placeholder path embedded in a JSON result line → access kind, EMPTY resource (no garbage prefill).
	need, kind, res, _ = detectRequest(`{"ok":false,"summary":"ACCESS-REQUEST: /<firmware repo + a board> — build it.","files":[]}`)
	if !need || kind != "access" || res != "" {
		t.Fatalf("placeholder must give empty resource: need=%v kind=%q res=%q", need, kind, res)
	}

	// APPROVAL marker (hardware/action) → approval kind, no resource, the whole line as detail.
	need, kind, res, detail = detectRequest("prep done\nAPPROVAL-REQUEST: flash the prepared firmware to the connected ESP32 board")
	if !need || kind != "approval" || res != "" || detail == "" {
		t.Fatalf("approval marker: need=%v kind=%q res=%q detail=%q", need, kind, res, detail)
	}

	// Heuristics: hardware signal → approval; repo signal → access.
	if n, k, _, _ := detectRequest("I need to power-cycle the physical board"); !n || k != "approval" {
		t.Fatalf("hardware heuristic should be approval, got need=%v kind=%q", n, k)
	}
	if n, k, _, _ := detectRequest("the plan doc is outside my sandbox"); !n || k != "access" {
		t.Fatalf("repo heuristic should be access, got need=%v kind=%q", n, k)
	}
	// Normal failure → no request.
	if n, _, _, _ := detectRequest("the build failed with a compile error"); n {
		t.Fatal("a build failure should not raise a request")
	}
}
