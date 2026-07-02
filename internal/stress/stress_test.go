package stress

import "testing"

func TestLargeBacklogRecoversDeterministically(t *testing.T) {
	cfg := Config{Issues: 100, RestartAfter: 30}
	rep, err := Run(t.TempDir(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Restarted {
		t.Error("expected a mid-run restart")
	}
	// Every issue reached a terminal state (done or failed) exactly once — no work lost or redone.
	if rep.Terminal != cfg.Issues {
		t.Fatalf("terminal=%d, want %d (done=%d failed=%d deferred=%d)", rep.Terminal, cfg.Issues, rep.Done, rep.Failed, rep.Deferred)
	}
	// Merge conflicts (index %7) produced failures; the rest completed.
	if rep.Failed == 0 || rep.Done == 0 {
		t.Errorf("expected a mix of done/failed, got done=%d failed=%d", rep.Done, rep.Failed)
	}
}

func TestDeterministicAcrossRuns(t *testing.T) {
	cfg := Config{Issues: 60, RestartAfter: 20}
	a, err := Run(t.TempDir(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Run(t.TempDir(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if a.Signature != b.Signature {
		t.Fatalf("non-deterministic outcome: %q vs %q", a.Signature, b.Signature)
	}
}

func TestNoRestartAlsoCompletes(t *testing.T) {
	rep, err := Run(t.TempDir(), Config{Issues: 40})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Terminal != 40 {
		t.Errorf("terminal=%d, want 40", rep.Terminal)
	}
}
