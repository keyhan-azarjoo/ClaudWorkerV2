package assignment

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Worker is the ONE justified interface in S2: it has two realistic implementations — the real
// `claude -p` runner (production) and an in-memory fake (tests). AI lives only behind this port; the
// engine stays deterministic (Law 18).
type Worker interface {
	Run(ctx context.Context, in WorkerInput) (WorkerResult, error)
}

// WorkerInput is EXACTLY what the AI receives (docs/05, docs/16): the Assignment identity, acceptance
// criteria, relevant files, and Knowledge-Brain context. It deliberately carries NO execution state,
// NO Git logic, NO Jira logic, and NO lock logic.
type WorkerInput struct {
	IssueKey           string
	Summary            string
	AcceptanceCriteria string
	RelevantFiles      []File
	KnowledgeContext   string // empty until the Knowledge Brain (S4) exists
}

// File is a path + content pair used both for relevant-file context and for proposed writes.
type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// WorkerResult is the structured result the engine consumes. No free-form text is parsed elsewhere.
type WorkerResult struct {
	OK      bool   `json:"ok"`
	Summary string `json:"summary"`
	Files   []File `json:"files"` // files to write into the worktree
	Notes   string `json:"notes"`
}

// ClaudeWorker runs one disposable `claude -p` process. It is intentionally thin for S2 — prompt
// assembly (Knowledge Brain, S4) and strict output-schema validation (Worker Runner, S7) harden it
// later. In CI the engine is exercised with a fake Worker, so this spends no tokens during tests.
type ClaudeWorker struct {
	Bin string // default "claude"
}

// Run builds a minimal prompt from the safe input fields, execs claude with JSON output, and maps the
// envelope's result text into a WorkerResult.
func (w ClaudeWorker) Run(ctx context.Context, in WorkerInput) (WorkerResult, error) {
	bin := w.Bin
	if bin == "" {
		bin = "claude"
	}
	prompt := buildPrompt(in)
	cmd := exec.CommandContext(ctx, bin, "-p", prompt, "--output-format", "json")
	out, err := cmd.Output()
	if err != nil {
		return WorkerResult{}, fmt.Errorf("worker: claude run: %w", err)
	}
	// claude --output-format json returns an envelope with a "result" string.
	var env struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		return WorkerResult{}, fmt.Errorf("worker: decode envelope: %w", err)
	}
	return WorkerResult{OK: true, Summary: strings.TrimSpace(env.Result)}, nil
}

// buildPrompt renders only the safe fields. It never includes execution/git/jira/lock details.
func buildPrompt(in WorkerInput) string {
	var b strings.Builder
	b.WriteString("# Task\n")
	b.WriteString(in.IssueKey + ": " + in.Summary + "\n\n")
	b.WriteString("# Acceptance Criteria\n")
	b.WriteString(in.AcceptanceCriteria + "\n\n")
	if in.KnowledgeContext != "" {
		b.WriteString("# Architecture Summary\n" + in.KnowledgeContext + "\n\n")
	}
	if len(in.RelevantFiles) > 0 {
		b.WriteString("# Relevant Files\n")
		for _, f := range in.RelevantFiles {
			b.WriteString("## " + f.Path + "\n" + f.Content + "\n")
		}
	}
	return b.String()
}
