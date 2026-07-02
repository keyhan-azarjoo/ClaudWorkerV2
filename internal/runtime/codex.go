package runtime

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/myotgo/ClaudWorkerV2/internal/assignment"
)

// CodexWorkerRuntime executes the OpenAI Codex CLI (`codex exec`) non-interactively as a worker engine,
// in parity with the Claude worker. Codex edits the worktree directly with full autonomy; the Git side
// commits the physical changes, so a clean (exit 0) run is success. Stateless like the Claude runtime.
type CodexWorkerRuntime struct {
	Bin string   // provider binary; default "codex"
	Dir string   // working directory (the assignment worktree)
	Env []string // extra environment (e.g. CODEX_HOME for the selected account); appended to os.Environ
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

	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	resp.CompletionBytes = out.Len()
	if err != nil {
		return resp, fmt.Errorf("codex runtime: exec %s: %w (stderr: %s)", bin, err, strings.TrimSpace(stderr.String()))
	}

	summary := "codex completed"
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			summary = s // keep the last non-empty line as a short summary
		}
	}
	resp.Result = assignment.WorkerResult{OK: true, Summary: summary}
	return resp, nil
}
