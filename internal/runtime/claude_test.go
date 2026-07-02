package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeFakeClaude writes an executable shell script that behaves like `claude --output-format json`
// for tests — real process execution and stdin/stdout, but ZERO tokens.
func writeFakeClaude(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClaudeRuntimeSuccess(t *testing.T) {
	// Emit an envelope whose result is a contract-valid WorkerResult JSON string.
	inner := `{\"ok\":true,\"summary\":\"added\",\"files\":[{\"path\":\"hello.txt\",\"content\":\"hi\"}]}`
	bin := writeFakeClaude(t, `cat >/dev/null; printf '%s' '{"result":"`+inner+`"}'`)
	w := ClaudeWorkerRuntime{Bin: bin, Args: []string{}}

	resp, err := w.Run(context.Background(), sampleInput())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !resp.Result.OK || resp.Result.Summary != "added" {
		t.Errorf("result = %+v", resp.Result)
	}
	if len(resp.Result.Files) != 1 || resp.Result.Files[0].Path != "hello.txt" {
		t.Errorf("files = %+v", resp.Result.Files)
	}
	if resp.PromptBytes == 0 || resp.CompletionBytes == 0 {
		t.Errorf("sizes not measured: %+v", resp)
	}
	if w.Name() != "claude" {
		t.Errorf("name = %q", w.Name())
	}
}

func TestClaudeRuntimeStripsFence(t *testing.T) {
	// result wrapped in a ```json fence must still parse.
	inner := "```json\\n{\\\"ok\\\":true,\\\"summary\\\":\\\"fenced\\\"}\\n```"
	bin := writeFakeClaude(t, `cat >/dev/null; printf '%s' '{"result":"`+inner+`"}'`)
	w := ClaudeWorkerRuntime{Bin: bin, Args: []string{}}
	resp, err := w.Run(context.Background(), sampleInput())
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Result.OK || resp.Result.Summary != "fenced" {
		t.Errorf("fenced result not parsed: %+v", resp.Result)
	}
}

func TestClaudeRuntimeContractViolationIsSemanticFailure(t *testing.T) {
	// result is not a WorkerResult object → OK=false, but NOT a runtime error (the process ran fine).
	bin := writeFakeClaude(t, `cat >/dev/null; printf '%s' '{"result":"I cannot do this"}'`)
	w := ClaudeWorkerRuntime{Bin: bin, Args: []string{}}
	resp, err := w.Run(context.Background(), sampleInput())
	if err != nil {
		t.Fatalf("contract violation must not be a runtime error: %v", err)
	}
	if resp.Result.OK {
		t.Error("expected OK=false on contract violation")
	}
}

func TestClaudeRuntimeNonZeroExitIsError(t *testing.T) {
	bin := writeFakeClaude(t, `cat >/dev/null; echo "boom" >&2; exit 1`)
	w := ClaudeWorkerRuntime{Bin: bin, Args: []string{}}
	if _, err := w.Run(context.Background(), sampleInput()); err == nil {
		t.Error("expected runtime error on non-zero exit")
	}
}

func TestClaudeRuntimeHonoursContextTimeout(t *testing.T) {
	bin := writeFakeClaude(t, `sleep 5; printf '%s' '{"result":"late"}'`)
	w := ClaudeWorkerRuntime{Bin: bin, Args: []string{}}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := w.Run(ctx, sampleInput()); err == nil {
		t.Error("expected error when ctx times out")
	}
	if time.Since(start) > 2*time.Second {
		t.Error("process was not killed on ctx timeout")
	}
}

// TestClaudeRuntimeThroughRunner is the integration slice: real fake-binary exec behind the Runner,
// producing metrics — the full Worker Runtime path, zero tokens.
func TestClaudeRuntimeThroughRunner(t *testing.T) {
	inner := `{\"ok\":true,\"summary\":\"via runner\"}`
	bin := writeFakeClaude(t, `cat >/dev/null; printf '%s' '{"result":"`+inner+`"}'`)
	var m Metrics
	r := &Runner{Runtime: ClaudeWorkerRuntime{Bin: bin, Args: []string{}}, OnMetrics: func(x Metrics) { m = x }}
	res, err := r.Run(context.Background(), sampleInput())
	if err != nil || !res.OK || res.Summary != "via runner" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if m.Runtime != "claude" || m.PromptBytes == 0 || m.TokenEstimate == 0 {
		t.Errorf("metrics = %+v", m)
	}
}
