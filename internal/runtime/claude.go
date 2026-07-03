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
	Bin     string            // provider binary; default "claude"
	Args    []string          // extra args; default defaultClaudeArgs (prompt arrives on stdin)
	Dir     string            // working directory for the process (the assignment worktree); "" = inherit
	Env     []string          // extra environment (e.g. CLAUDE_CONFIG_DIR for the selected account)
	OnLine  func(string)      // optional: called with each human-readable line as the worker streams
	OnUsage func(in, out int) // optional: called with cumulative (sent, received) token counts, live
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
	var liveIn, liveOut int // rough running counts from assistant events (for a live-ish indicator)
	var accurateIn, accurateOut int
	var haveAccurate bool
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
		switch ev["type"] {
		case "assistant":
			// LIVE (rough) usage: assistant-turn usage is streamed and its output_tokens are partial, so
			// this is only a progress indicator. The AUTHORITATIVE totals come from the "result" event
			// below. We still emit it live so the dashboard token count moves during a long run.
			if msg, ok := ev["message"].(map[string]any); ok {
				if u, ok := msg["usage"].(map[string]any); ok {
					// Live INPUT estimate = the largest single-turn context seen (input + cache), which
					// tracks the same magnitude as the authoritative result-event total. Summing every
					// turn would over-count (each turn re-sends the context) and then snap down at bank —
					// this keeps the live number close so the final reconciliation is small. Monotonic.
					turnIn := intOf(u["input_tokens"]) + intOf(u["cache_read_input_tokens"]) + intOf(u["cache_creation_input_tokens"])
					if turnIn > liveIn {
						liveIn = turnIn
					}
					liveOut += intOf(u["output_tokens"]) // rough; corrected by the result event at run end
					if w.OnUsage != nil {
						w.OnUsage(liveIn, liveOut)
					}
				}
			}
		case "result":
			// The terminal "result" event carries the ACCURATE final usage for the whole run (input +
			// all cache reads/creations = tokens sent; output_tokens = tokens received).
			if u, ok := ev["usage"].(map[string]any); ok {
				accurateIn = intOf(u["input_tokens"]) + intOf(u["cache_read_input_tokens"]) + intOf(u["cache_creation_input_tokens"])
				accurateOut = intOf(u["output_tokens"])
				haveAccurate = true
			}
		}
		// The completion / WorkerResult envelope: the real stream-json carries it on the "result" event;
		// the legacy fake emits a bare {"result":"..."} line with no type. Catch both.
		if r, ok := ev["result"].(string); ok {
			resultStr = r
		}
	}
	// Prefer the accurate result-event totals; fall back to the rough assistant sum only if absent.
	if haveAccurate {
		resp.InputTokens, resp.OutputTokens = accurateIn, accurateOut
		if w.OnUsage != nil {
			w.OnUsage(accurateIn, accurateOut) // final authoritative correction
		}
	} else {
		resp.InputTokens, resp.OutputTokens = liveIn, liveOut
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

// logLinesFromEvent renders a stream-json event as human-readable activity ("thinking / doing /
// responses") lines — the transcript shown in the console drawer. It captures the agent's text,
// its reasoning (thinking), and each tool call with a short summary of what it's doing.
func logLinesFromEvent(ev map[string]any) []string {
	var out []string
	switch ev["type"] {
	case "assistant":
		msg, _ := ev["message"].(map[string]any)
		content, _ := msg["content"].([]any)
		// BRIEF, important-only transcript: the agent's decisions (text) + what it's DOING (tool calls
		// + sub-agents). Verbose chain-of-thought is intentionally omitted to keep the report concise.
		for _, c := range content {
			m, _ := c.(map[string]any)
			switch m["type"] {
			case "text":
				// Keep the agent's narrative essentially whole (only guard against a pathologically huge
				// block) so report lines aren't cut in half.
				if s := strings.TrimSpace(fmt.Sprint(m["text"])); s != "" && s != "<nil>" {
					out = append(out, clip(s, 4000))
				}
			case "tool_use":
				// BRIEF narrative report: keep only sub-agent spawns (a real milestone — the multi-agent
				// fan-out). Skip raw tool-command lines (Bash/Grep/Read/Edit …) so the report reads like a
				// progress narrative in the agent's own words, not a command log.
				if name, _ := m["name"].(string); name == "Task" {
					out = append(out, "🤖 sub-agent"+toolSummary(name, m["input"]))
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

// toolSummary extracts a short " — <what>" summary from a tool_use input (e.g. the Bash command or the
// edited file), so the transcript reads like "🔧 Bash — git status" rather than just "🔧 Bash".
func toolSummary(name string, input any) string {
	m, ok := input.(map[string]any)
	if !ok {
		return ""
	}
	for _, k := range []string{"command", "file_path", "path", "pattern", "description", "prompt", "query", "url"} {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return " — " + clip(strings.TrimSpace(v), 160)
		}
	}
	return ""
}

// intOf coerces a JSON number (float64) to int.
func intOf(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

// clip truncates s to n runes with an ellipsis.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
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
