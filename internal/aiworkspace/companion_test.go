package aiworkspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatedOptimizersRegistered(t *testing.T) {
	for _, id := range []string{"semantic-dedup", "rag", "embedding"} {
		o, ok := GetOptimizer(id)
		if !ok {
			t.Fatalf("gated optimizer %q not registered", id)
		}
		if !o.Meta().RequiresCompanion {
			t.Fatalf("%q should be marked RequiresCompanion", id)
		}
	}
}

func TestCompanionAbsentByDefault(t *testing.T) {
	svc := New(t.TempDir())
	st := svc.CompanionStatus()
	if st["present"] != false || st["configured"] != false {
		t.Fatalf("fresh companion should be absent+unconfigured: %+v", st)
	}
}

func TestCompanionRejectsNonLoopback(t *testing.T) {
	svc := New(t.TempDir())
	if _, err := svc.CompanionConnect("http://example.com:9000"); err == nil {
		t.Fatal("connecting to a non-loopback host must be rejected")
	}
}

func TestGatedOptimizerRequiresCompanion(t *testing.T) {
	svc := New(t.TempDir())
	_, err := svc.RunOptimizer(context.Background(), "rag", "text", "some content", nil)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "companion") {
		t.Fatalf("gated optimizer without a companion should error about the companion, got %v", err)
	}
	// The failed run is recorded with error health.
	for _, o := range svc.OptimizersList() {
		if o["meta"].(OptimizerMeta).ID == "rag" {
			if o["stats"].(OptimizerStats).Health != "error" {
				t.Fatal("failed gated run should mark health=error")
			}
		}
	}
}

// TestCompanionConnectProbeAndRoute — a fake loopback daemon makes the companion present and routes a
// gated optimizer's execution to it. httptest binds 127.0.0.1, satisfying the loopback guard.
func TestCompanionConnectProbeAndRoute(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/capabilities", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"capabilities": []string{"optimize", "embed"}})
	})
	mux.HandleFunc("/optimize", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"content": "companion-optimized", "notes": []string{"ran on companion"}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := New(t.TempDir())
	st, err := svc.CompanionConnect(srv.URL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if st["present"] != true {
		t.Fatalf("companion should be present after connecting to a healthy daemon: %+v", st)
	}
	caps, _ := st["capabilities"].([]string)
	if len(caps) == 0 {
		t.Fatal("expected capabilities from the daemon")
	}

	res, err := svc.RunOptimizer(context.Background(), "semantic-dedup", "text", "big redundant content", nil)
	if err != nil {
		t.Fatalf("gated run via companion: %v", err)
	}
	if res["output"] != "companion-optimized" {
		t.Fatalf("expected companion output, got %v", res["output"])
	}

	// Disconnect returns to absent.
	svc.CompanionDisconnect()
	if svc.CompanionStatus()["present"] != false {
		t.Fatal("companion should be absent after disconnect")
	}
}
