package runtimeadapter

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"claudworker/internal/assignment"
	"claudworker/internal/orchestrator"
	"claudworker/internal/runtime"
)

func assignmentOK() assignment.WorkerResult { return assignment.WorkerResult{OK: true} }

// writeFakeClaude writes an executable stand-in for the Claude Code CLI — real process, stdin/stdout,
// exit codes — but ZERO tokens.
func writeFakeClaude(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func devInput(account string) orchestrator.DevInput {
	return orchestrator.DevInput{Issue: "SCRUM-1", Summary: "do work", AcceptanceCriteria: "- done", Account: account}
}

func TestSuccessRunsInWorktree(t *testing.T) {
	// success: edit a file in CWD (the worktree) + return a contract-valid result.
	inner := `{\"ok\":true,\"summary\":\"did it\"}`
	bin := writeFakeClaude(t, `cat >/dev/null; echo hi > worker_out.txt; printf '%s' '{"result":"`+inner+`"}'`)
	wt := t.TempDir()
	w := New(bin, map[string]Account{"a": {ID: "a"}})

	res, err := w.Develop(context.Background(), wt, devInput("a"))
	if err != nil || !res.OK {
		t.Fatalf("develop = %+v err=%v", res, err)
	}
	if _, err := os.Stat(filepath.Join(wt, "worker_out.txt")); err != nil {
		t.Errorf("worker did not run in the worktree (CWD): %v", err)
	}
	if s := w.Snapshot(); len(s.Recent) != 1 || s.Recent[0].Class != ClassSuccess || s.Recent[0].TokenEstimate == 0 {
		t.Errorf("metrics = %+v", s.Recent)
	}
}

func TestSemanticDecline(t *testing.T) {
	bin := writeFakeClaude(t, `cat >/dev/null; printf '%s' '{"result":"{\"ok\":false,\"notes\":\"cannot\"}"}'`)
	w := New(bin, nil)
	res, err := w.Develop(context.Background(), t.TempDir(), devInput(""))
	if err != nil {
		t.Fatalf("semantic must not be an error: %v", err)
	}
	if res.OK || w.Snapshot().Recent[0].Class != ClassSemantic {
		t.Errorf("expected semantic decline, got %+v", res)
	}
}

func TestRateLimitCoolsAccountAndReturnsToPolicy(t *testing.T) {
	bin := writeFakeClaude(t, `cat >/dev/null; echo "rate limit exceeded (429)" >&2; exit 1`)
	cooled := ""
	w := New(bin, map[string]Account{"a": {ID: "a"}})
	w.Cooldown = func(account string, d time.Duration) { cooled = account }

	res, err := w.Develop(context.Background(), t.TempDir(), devInput("a"))
	if err == nil {
		t.Fatal("rate limit must return an error to the Policy Engine")
	}
	if res.OK || cooled != "a" {
		t.Errorf("account not cooled on rate limit: cooled=%q", cooled)
	}
	s := w.Snapshot()
	if s.Cooldowns != 1 || s.FailoverEvents != 1 || s.Recent[0].Class != ClassRateLimit {
		t.Errorf("snapshot = %+v", s)
	}
}

func TestAuthFails(t *testing.T) {
	bin := writeFakeClaude(t, `cat >/dev/null; echo "unauthorized (401) not logged in" >&2; exit 1`)
	w := New(bin, nil)
	_, err := w.Develop(context.Background(), t.TempDir(), devInput("a"))
	if err == nil || w.Snapshot().Recent[0].Class != ClassAuthentication {
		t.Errorf("expected authentication class, got %v / %+v", err, w.Snapshot().Recent)
	}
}

func TestInfrastructureIsRetried(t *testing.T) {
	bin := writeFakeClaude(t, `cat >/dev/null; echo "connection refused" >&2; exit 1`)
	w := New(bin, nil)
	w.MaxInfraRetries = 2
	_, err := w.Develop(context.Background(), t.TempDir(), devInput("a"))
	if err == nil {
		t.Fatal("infra failure should surface after retries")
	}
	if r := w.Snapshot().Recent[0]; r.Class != ClassInfrastructure || r.Retries != 2 {
		t.Errorf("infra retry metrics = %+v", r)
	}
}

func TestCliFailureIsRuntimeFailure(t *testing.T) {
	bin := writeFakeClaude(t, `cat >/dev/null; echo "panic: boom" >&2; exit 2`)
	w := New(bin, nil)
	_, err := w.Develop(context.Background(), t.TempDir(), devInput("a"))
	if err == nil || w.Snapshot().Recent[0].Class != ClassRuntimeFailure {
		t.Errorf("expected runtime_failure, got %v / %+v", err, w.Snapshot().Recent)
	}
}

func TestTimeout(t *testing.T) {
	bin := writeFakeClaude(t, `cat >/dev/null; sleep 5; printf '%s' '{"result":"late"}'`)
	w := New(bin, nil)
	w.Timeout = 50 * time.Millisecond
	w.MaxInfraRetries = 0
	start := time.Now()
	_, err := w.Develop(context.Background(), t.TempDir(), devInput("a"))
	if err == nil || w.Snapshot().Recent[0].Class != ClassTimeout {
		t.Errorf("expected timeout, got %v / %+v", err, w.Snapshot().Recent)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("process not killed on timeout")
	}
}

func TestCancellation(t *testing.T) {
	bin := writeFakeClaude(t, `cat >/dev/null; sleep 5`)
	w := New(bin, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	_, err := w.Develop(ctx, t.TempDir(), devInput("a"))
	if err == nil || w.Snapshot().Recent[0].Class != ClassCancellation {
		t.Errorf("expected cancellation, got %v / %+v", err, w.Snapshot().Recent)
	}
}

func TestClassifyTable(t *testing.T) {
	okResp := runtime.Response{Result: assignmentOK()}
	if c := classify(context.Background(), context.Background(), nil, okResp); c != ClassSuccess {
		t.Errorf("ok → %s", c)
	}
	if c := classify(context.Background(), context.Background(), nil, runtime.Response{}); c != ClassSemantic {
		t.Errorf("not-ok → %s", c)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if c := classify(canceled, canceled, context.Canceled, runtime.Response{}); c != ClassCancellation {
		t.Errorf("canceled → %s", c)
	}
}
