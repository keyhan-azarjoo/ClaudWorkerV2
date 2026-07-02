package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/myotgo/ClaudWorkerV2/internal/assignment"
)

// ClaudeWorkerRuntime is the FIRST WorkerRuntime provider. It is intentionally tiny: build the prompt
// (deterministically, via BuildPrompt), deliver it on the process stdin, exec the binary, collect
// stdout, and parse the result. Everything else (retry/timeout/metrics/logging) is Runner's job, so a
// future provider (Codex/GPT/Gemini/local) only needs to re-implement this thin exec+parse.
//
// It is stateless: no field survives a Run, so every execution starts clean (disposable, Law 4).
type ClaudeWorkerRuntime struct {
	Bin  string   // provider binary; default "claude"
	Args []string // extra args; default ["-p","--output-format","json"] (prompt arrives on stdin)
}

// Name identifies the provider.
func (ClaudeWorkerRuntime) Name() string { return "claude" }

// Run delivers the prompt on stdin and collects the completion from stdout. It honours ctx: a
// deadline or cancellation kills the process (exec.CommandContext). A spawn error, non-zero exit, or
// undecodable transport is returned as an error (transient/infra — Runner may retry). A process that
// ran but produced output that does not satisfy the contract yields Result.OK=false with nil error (a
// semantic failure the Assignment Engine handles).
func (w ClaudeWorkerRuntime) Run(ctx context.Context, in assignment.WorkerInput) (Response, error) {
	bin := w.Bin
	if bin == "" {
		bin = "claude"
	}
	args := w.Args
	if args == nil {
		args = []string{"-p", "--output-format", "json"}
	}
	prompt := BuildPrompt(in)
	resp := Response{PromptBytes: len(prompt)}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = strings.NewReader(prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return resp, fmt.Errorf("claude runtime: exec %s: %w (stderr: %s)", bin, err, strings.TrimSpace(stderr.String()))
	}

	// claude --output-format json wraps the assistant text in an envelope {"result": "..."}.
	var env struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		return resp, fmt.Errorf("claude runtime: decode envelope: %w", err)
	}
	completion := strings.TrimSpace(env.Result)
	resp.CompletionBytes = len(completion)

	res, ok := parseResult(completion)
	if !ok {
		// The provider ran but did not return a contract-valid object — a semantic failure, not an
		// infra error. Surface it so the engine can retry development.
		resp.Result = assignment.WorkerResult{OK: false, Notes: "worker output did not match the WorkerResult contract"}
		return resp, nil
	}
	resp.Result = res
	return resp, nil
}

// parseResult extracts a WorkerResult from the completion text, tolerating a ```json fence. It returns
// ok=false if no valid WorkerResult object is present.
func parseResult(completion string) (assignment.WorkerResult, bool) {
	s := stripFence(completion)
	var res assignment.WorkerResult
	if err := json.Unmarshal([]byte(s), &res); err != nil {
		return assignment.WorkerResult{}, false
	}
	return res, true
}

// stripFence removes a leading/trailing markdown code fence (```json … ```), if present.
func stripFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:] // drop the ```lang line
	}
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	return strings.TrimSpace(s)
}
