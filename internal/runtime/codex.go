package runtime

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"claudworker/internal/assignment"
)

// CodexWorkerRuntime executes the OpenAI Codex CLI (`codex exec`) non-interactively as a worker engine,
// in parity with the Claude worker. Codex edits the worktree directly with full autonomy; the Git side
// commits the physical changes, so a clean (exit 0) run is success. Stateless like the Claude runtime.
type CodexWorkerRuntime struct {
	Bin    string       // provider binary; default "codex"
	Dir    string       // working directory (the assignment worktree)
	Env    []string     // extra environment (e.g. CODEX_HOME for the selected account); appended to os.Environ
	OnLine func(string) // optional: called with each output line as codex streams
}

// Name identifies the provider.
func (CodexWorkerRuntime) Name() string { return "codex" }

// Run builds the prompt, runs `codex exec` with headless full-autonomy flags (matching V1's codex
// worker), and reports success on a clean exit. Codex writes changes into the worktree directly, so
// Result.Files stays empty — the Git adapter commits whatever changed.
func (w CodexWorkerRuntime) Run(ctx context.Context, in assignment.WorkerInput) (Response, error) {
	bin := w.Bin
	if bin == "" {
		bin = "codex"
	}
	prompt := BuildPrompt(in)
	resp := Response{PromptBytes: len(prompt)}

	args := []string{"exec", "--skip-git-repo-check", "--dangerously-bypass-approvals-and-sandbox", "--color", "never", prompt}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = w.Dir
	cmd.Env = append(os.Environ(), w.Env...)

	stdoutPipe, perr := cmd.StdoutPipe()
	if perr != nil {
		return resp, fmt.Errorf("codex runtime: stdout pipe: %w", perr)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return resp, fmt.Errorf("codex runtime: start %s: %w", bin, err)
	}
	summary := "codex completed"
	total := 0
	var nonEmpty []string
	sc := bufio.NewScanner(stdoutPipe)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		total += len(line) + 1
		if s := strings.TrimSpace(line); s != "" {
			summary = s // keep the last non-empty line as a short summary
			nonEmpty = append(nonEmpty, s)
			if w.OnLine != nil {
				w.OnLine(s)
			}
		}
	}
	resp.CompletionBytes = total
	if err := cmd.Wait(); err != nil {
		return resp, fmt.Errorf("codex runtime: exec %s: %w (stderr: %s)", bin, err, strings.TrimSpace(stderr.String()))
	}
	// Honor the agent's OWN result: if it printed the {"ok":...} WorkerResult contract, use that OK/summary
	// (so an ok:false "I couldn't complete this" is NOT reported as success). A clean run with no such line
	// defaults to success (codex edits the worktree directly; the Git side commits the changes).
	resp.Result = assignment.WorkerResult{OK: true, Summary: clip(summary, 400)}
	for i := len(nonEmpty) - 1; i >= 0; i-- {
		if strings.Contains(nonEmpty[i], "\"ok\"") {
			if res, ok := parseResult(nonEmpty[i]); ok {
				resp.Result = res
				break
			}
		}
	}
	return resp, nil
}
