package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/myotgo/ClaudWorkerV2/internal/assignment"
)

// ClaudeWorkerRuntime is the FIRST WorkerRuntime provider. It builds the prompt (deterministically, via
// BuildPrompt), delivers it on stdin, runs the CLI in the worktree, and parses the result. It streams
// the CLI's output (stream-json) so the console can show what the agent is doing live (OnLine).
//
// It is stateless: no field survives a Run, so every execution starts clean (disposable, Law 4).
type ClaudeWorkerRuntime struct {
	Bin    string       // provider binary; default "claude"
	Args   []string     // extra args; default defaultClaudeArgs (prompt arrives on stdin)
	Dir    string       // working directory for the process (the assignment worktree); "" = inherit
	Env    []string     // extra environment (e.g. CLAUDE_CONFIG_DIR for the selected account)
	OnLine func(string) // optional: called with each human-readable line as the worker streams
}

// Name identifies the provider.
func (ClaudeWorkerRuntime) Name() string { return "claude" }

// defaultClaudeArgs is the headless invocation. stream-json (with --verbose) streams the agent's
// activity live; --permission-mode acceptEdits is REQUIRED for autonomous file edits in -p mode.
func defaultClaudeArgs() []string {
	return []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "acceptEdits"}
}

// Run streams the CLI. Each output line is a JSON event; assistant text + tool-use become live log
// lines (OnLine), and the terminal "result" event carries the WorkerResult envelope (same contract as
// before). ctx cancellation kills the process; a spawn error / non-zero exit is a (retryable) error.
func (w ClaudeWorkerRuntime) Run(ctx context.Context, in assignment.WorkerInput) (Response, error) {
	bin := w.Bin
	if bin == "" {
		bin = "claude"
	}
	args := w.Args
	if args == nil {
		args = defaultClaudeArgs()
	}
	prompt := BuildPrompt(in)
	resp := Response{PromptBytes: len(prompt)}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = strings.NewReader(prompt)
	if w.Dir != "" {
		cmd.Dir = w.Dir // run inside the assignment worktree (the CLI edits files in place)
	}
	if len(w.Env) > 0 {
		cmd.Env = append(os.Environ(), w.Env...) // account-specific env (e.g. CLAUDE_CONFIG_DIR)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return resp, fmt.Errorf("claude runtime: stdout pipe: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return resp, fmt.Errorf("claude runtime: start %s: %w", bin, err)
	}

	var resultStr string
	sc := bufio.NewScanner(stdoutPipe)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024) // events (esp. final result) can be large
	for sc.Scan() {
		var ev map[string]any
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue // ignore non-JSON lines
		}
		if w.OnLine != nil {
			for _, s := range logLinesFromEvent(ev) {
				w.OnLine(s)
			}
		}
		// The final "result" event (and the legacy {"result":...} envelope) carry the completion.
		if r, ok := ev["result"].(string); ok {
			resultStr = r
		}
	}
	if err := cmd.Wait(); err != nil {
		return resp, fmt.Errorf("claude runtime: exec %s: %w (stderr: %s)", bin, err, strings.TrimSpace(stderr.String()))
	}

	completion := strings.TrimSpace(resultStr)
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

// logLinesFromEvent renders a stream-json event as human-readable activity ("thinking/doing") lines.
func logLinesFromEvent(ev map[string]any) []string {
	var out []string
	switch ev["type"] {
	case "assistant":
		msg, _ := ev["message"].(map[string]any)
		content, _ := msg["content"].([]any)
		for _, c := range content {
			m, _ := c.(map[string]any)
			switch m["type"] {
			case "text":
				if s, _ := m["text"].(string); strings.TrimSpace(s) != "" {
					out = append(out, strings.TrimSpace(s))
				}
			case "tool_use":
				name, _ := m["name"].(string)
				if name == "Task" {
					out = append(out, "🤖 spawned a sub-agent")
				} else if name != "" {
					out = append(out, "🔧 "+name)
				}
			}
		}
	case "system":
		if ev["subtype"] == "init" {
			out = append(out, "▶ agent started")
		}
	}
	return out
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
