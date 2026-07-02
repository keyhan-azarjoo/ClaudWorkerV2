// Package runtime is the Worker Runtime (docs/05, docs/21 S5).
//
// The Worker Runtime executes a reasoning engine for ONE Assignment attempt. It is NOT
// Claude-specific: `WorkerRuntime` is the provider port, and `ClaudeWorkerRuntime` is simply the
// first implementation. Codex/GPT/Gemini/a local runtime can be added later WITHOUT touching the
// Assignment Engine — the engine depends only on the `assignment.Worker` port, which the deterministic
// `Runner` satisfies.
//
// Ownership (owner-mandated S5 scope). The Worker Runtime owns: worker lifecycle, process execution,
// stdin/stdout, cancellation, timeout, retry, metrics, logging, prompt delivery, response collection.
// It owns NOTHING else — no Git, Jira, Locks, Decisions, QA, Knowledge, or Assignment state.
//
// Disposable (Law 4). Every execution starts clean: no session memory, no resume, no hidden state.
// Knowledge enters only via the KnowledgeContext field (produced by the Knowledge Brain, S4).
//
// Deterministic-first split (Law 18 + S5 runtime review). Everything provider-agnostic —
// prompt assembly, retry, timeout, metrics, logging — is plain deterministic Go here (BuildPrompt +
// Runner). The only provider-specific code is `ClaudeWorkerRuntime`, reduced to: deliver the prompt on
// stdin, exec the binary, collect stdout, parse the result. This keeps provider code as small as
// possible behind the interface.
package runtime

import (
	"context"
	"strings"

	"github.com/myotgo/ClaudWorkerV2/internal/assignment"
)

// bytesPerToken is the deterministic divisor for the token ESTIMATE (~4 chars/token, English-ish).
// It is a documented approximation for budgeting/metrics only — the runtime spends real tokens only
// when a provider process actually runs, and never counts them itself.
const bytesPerToken = 4

// WorkerRuntime executes one reasoning-engine invocation. Implementations are provider-specific and
// MUST be stateless across calls (disposable) and honour ctx (timeout + cancellation). They own only
// the provider mechanics; retry/metrics/logging are added deterministically by Runner.
type WorkerRuntime interface {
	// Name identifies the provider for logs/metrics (e.g. "claude").
	Name() string
	// Run delivers the prompt to the provider and collects its response. It returns a Response
	// (result + measured prompt/completion sizes). A non-nil error is a RUNTIME/infra failure
	// (spawn, timeout, non-zero exit, undecodable transport) that Runner may retry; a provider that
	// ran but declined the task returns Response with Result.OK=false and nil error (a semantic
	// failure the Assignment Engine handles, not the runtime).
	Run(ctx context.Context, in assignment.WorkerInput) (Response, error)
}

// Response is a provider's raw outcome plus the two sizes only the provider can measure. Runner folds
// these into Metrics.
type Response struct {
	Result          assignment.WorkerResult
	PromptBytes     int // bytes delivered to the provider
	CompletionBytes int // bytes of the provider's completion (its result text)
}

// Metrics is the deterministic measurement of one Runner.Run (owner-mandated set). No tokens are
// consumed to produce it; TokenEstimate is an approximation (see bytesPerToken).
type Metrics struct {
	Runtime         string `json:"runtime"`
	StartupTime     string `json:"startup_time"`   // time to spawn the provider process
	ExecutionTime   string `json:"execution_time"` // total wallclock of the attempt(s)
	PromptBytes     int    `json:"prompt_bytes"`
	CompletionBytes int    `json:"completion_bytes"`
	TokenEstimate   int    `json:"token_estimate"`
	Retries         int    `json:"retries"`
	Failed          bool   `json:"failed"`
	Cancelled       bool   `json:"cancelled"`
	TimedOut        bool   `json:"timed_out"`
}

// EstimateTokens returns the deterministic token estimate for a byte count.
func EstimateTokens(bytes int) int {
	if bytes <= 0 {
		return 0
	}
	return (bytes + bytesPerToken - 1) / bytesPerToken
}

// BuildPrompt renders the prompt from EXACTLY the four permitted inputs — the Assignment identity
// (issue + summary), AcceptanceCriteria, KnowledgeContext, and RelevantFiles — and nothing more.
// Execution state (attempt, lifecycle state, retries, locks, metrics) NEVER enters the prompt. The
// output is deterministic: identical WorkerInput → byte-identical prompt.
//
// This is provider-agnostic and lives in deterministic Go so no provider re-implements it (S5 review).
func BuildPrompt(in assignment.WorkerInput) string {
	var b strings.Builder
	b.WriteString("# Task\n")
	b.WriteString(in.IssueKey)
	if in.Summary != "" {
		b.WriteString(": ")
		b.WriteString(in.Summary)
	}
	b.WriteString("\n\n# Acceptance Criteria\n")
	b.WriteString(strings.TrimRight(in.AcceptanceCriteria, "\n"))
	b.WriteString("\n")

	if strings.TrimSpace(in.KnowledgeContext) != "" {
		b.WriteString("\n")
		b.WriteString(strings.TrimRight(in.KnowledgeContext, "\n"))
		b.WriteString("\n")
	}

	if len(in.RelevantFiles) > 0 {
		b.WriteString("\n# Relevant Files\n")
		for _, f := range in.RelevantFiles {
			b.WriteString("\n## ")
			b.WriteString(f.Path)
			b.WriteString("\n")
			b.WriteString(strings.TrimRight(f.Content, "\n"))
			b.WriteString("\n")
		}
	}

	// Working style: this runs as Claude Code inside the task's git worktree, so it can make real edits
	// directly AND spawn its own parallel subagents — the "several agents on one task, same branch" model.
	b.WriteString("\n# Working style\n")
	b.WriteString("You are working INSIDE this task's git worktree (your current directory). Make the real code changes directly here.\n")
	b.WriteString("When the task is large or divisible, spawn MULTIPLE PARALLEL SUBAGENTS (the Task tool) — up to 10 — to work together on this ONE task on the SAME branch, to finish faster and more accurately. Give each subagent a DIFFERENT part / different files so they never edit the same file at once. For a small task, do it directly with one. Make sure the combined result is coherent and complete before finishing.\n")

	// Output contract: reply with ONE JSON object matching WorkerResult so response collection is
	// deterministic. Edits are made directly in the worktree, so "files" may be empty even on success
	// (the Git side commits the physical changes).
	b.WriteString("\n# Response format\n")
	b.WriteString(`When finished, reply with a single JSON object: {"ok":bool,"summary":string,"notes":string,"files":[{"path":string,"content":string}]}. Set ok=true when the changes are complete; leave "files" empty if you edited the worktree directly.` + "\n")
	return b.String()
}
