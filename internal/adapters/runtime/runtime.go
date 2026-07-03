// Package runtimeadapter is the REAL Worker Runtime edge (Phase 2.3): it executes the local Claude
// Code CLI as the reasoning worker, inside the assignment worktree, under the account the Resource
// Manager selected. It stays provider-agnostic (Claude is the first provider, via internal/runtime's
// ClaudeWorkerRuntime) and owns ONLY: process lifecycle, stdin/stdout, timeout, cancellation,
// infrastructure-only retry, metrics, and logging.
//
// It NEVER decides which account to use (the Resource Manager selected it) and NEVER lets execution /
// Git / policy / lease state into the prompt — the prompt is built from the four permitted inputs by
// internal/runtime.BuildPrompt. Workers are disposable: a fresh process per call, no session, no
// resume, no hidden context.
package runtimeadapter

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"claudworker/internal/assignment"
	"claudworker/internal/orchestrator"
	"claudworker/internal/runtime"
)

// Account is how one Claude account is executed. Selected by the Resource Manager; used, never chosen,
// by the runtime.
type Account struct {
	ID        string
	ConfigDir string   // CLAUDE_CONFIG_DIR (claude) or CODEX_HOME (codex) for this account
	Model     string   // optional --model (claude)
	Engine    string   // "claude" (default) | "codex" — selects the CLI to run
	Env       []string // extra environment
}

// Class classifies an execution outcome (owner-mandated taxonomy). Only Infrastructure is retried by
// the runtime; everything else returns to the Policy Engine.
type Class string

const (
	ClassSuccess        Class = "success"
	ClassInfrastructure Class = "infrastructure"
	ClassAuthentication Class = "authentication"
	ClassRateLimit      Class = "rate_limit"
	ClassTimeout        Class = "timeout"
	ClassCancellation   Class = "cancellation"
	ClassRuntimeFailure Class = "runtime_failure"
	ClassSemantic       Class = "semantic" // ran, declined the task (OK=false)
)

// Metrics is one execution's observable data (for the Control Plane).
type Metrics struct {
	Issue           string `json:"issue"`
	Account         string `json:"account"`
	Runtime         string `json:"runtime"`
	Duration        string `json:"duration"`
	PromptBytes     int    `json:"prompt_bytes"`
	CompletionBytes int    `json:"completion_bytes"`
	TokenEstimate   int    `json:"token_estimate"`
	InputTokens     int    `json:"input_tokens"`  // ACCURATE tokens sent (0 if provider didn't report)
	OutputTokens    int    `json:"output_tokens"` // ACCURATE tokens received
	Retries         int    `json:"retries"`
	Class           Class  `json:"class"`
}

// Worker is the real Worker Runtime worktree worker (implements gitadapter.WorktreeWorker).
type Worker struct {
	Bin             string
	Accounts        map[string]Account
	MaxInfraRetries int
	Timeout         time.Duration
	CooldownFor     time.Duration                         // how long to cool a rate-limited/auth-failed account
	Cooldown        func(account string, d time.Duration) // health signal → Resource Manager (optional)
	OnMetrics       func(Metrics)                         // → Control Plane (optional)
	OnLog           func(issue, line string)              // live agent activity → Control Plane (optional)
	OnTokens        func(issue string, in, out int)       // live (rough) per-run token usage → Control Plane
	OnTokensDone    func(issue string, in, out int)       // accurate per-run totals at run end → banked to the task total
	now             func() time.Time

	mu       sync.Mutex
	active   int
	history  []Metrics
	failover int
	cooled   int
}

// New builds a Worker with defaults.
func New(bin string, accounts map[string]Account) *Worker {
	if bin == "" {
		bin = "claude"
	}
	if accounts == nil {
		accounts = map[string]Account{}
	}
	return &Worker{Bin: bin, Accounts: accounts, MaxInfraRetries: 2, Timeout: 10 * time.Minute, CooldownFor: 15 * time.Minute, now: time.Now}
}

func (w *Worker) clock() func() time.Time {
	if w.now != nil {
		return w.now
	}
	return time.Now
}

// Develop runs the CLI in the worktree under the selected account and returns the result. Only
// infrastructure failures are retried here; other classes return to the caller (→ Policy Engine).
func (w *Worker) Develop(ctx context.Context, worktree string, in orchestrator.DevInput) (orchestrator.DevResult, error) {
	acct := w.Accounts[in.Account] // zero Account (default env) if unknown/empty

	// Live activity + accurate token usage → Control Plane (per task).
	onLine := func(s string) {
		if w.OnLog != nil {
			w.OnLog(in.Issue, s)
		}
	}
	onUsage := func(inTok, outTok int) {
		if w.OnTokens != nil {
			w.OnTokens(in.Issue, inTok, outTok)
		}
	}
	// Engine routing: codex accounts run the Codex CLI; everything else runs Claude Code.
	var rt runtime.WorkerRuntime
	if acct.Engine == "codex" {
		rt = runtime.CodexWorkerRuntime{Bin: "codex", Dir: worktree, Env: codexEnv(acct), OnLine: onLine}
	} else {
		crt := runtime.ClaudeWorkerRuntime{Bin: w.Bin, Dir: worktree, Env: accountEnv(acct), OnLine: onLine, OnUsage: onUsage}
		if acct.Model != "" {
			// Mirror defaultClaudeArgs (stream-json for live output + acceptEdits for autonomous edits)
			// and pin the account's model.
			crt.Args = []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "acceptEdits", "--model", acct.Model}
		}
		rt = crt
	}
	wi := assignment.WorkerInput{
		IssueKey:           in.Issue,
		Summary:            in.Summary,
		AcceptanceCriteria: in.AcceptanceCriteria,
		KnowledgeContext:   in.KnowledgeContext,
		OperatorNote:       in.OperatorNote,
		Rules:              in.Rules,
		// RelevantFiles intentionally empty: the CLI reads the worktree directly. Execution/Git/Policy/
		// Lease state never enter the prompt.
	}

	start := w.clock()()
	w.enter()
	defer w.leave()

	var (
		resp    runtime.Response
		runErr  error
		class   Class
		retries int
	)
	for {
		attemptCtx := ctx
		cancel := context.CancelFunc(func() {})
		if w.Timeout > 0 {
			attemptCtx, cancel = context.WithTimeout(ctx, w.Timeout)
		}
		resp, runErr = rt.Run(attemptCtx, wi)
		cancel()
		class = classify(ctx, attemptCtx, runErr, resp)
		if class == ClassInfrastructure && retries < w.MaxInfraRetries && ctx.Err() == nil {
			retries++
			continue
		}
		break
	}

	m := Metrics{
		Issue: in.Issue, Account: in.Account, Runtime: "claude",
		Duration:        w.clock()().Sub(start).String(),
		PromptBytes:     resp.PromptBytes,
		CompletionBytes: resp.CompletionBytes,
		TokenEstimate:   runtime.EstimateTokens(resp.PromptBytes + resp.CompletionBytes),
		InputTokens:     resp.InputTokens,
		OutputTokens:    resp.OutputTokens,
		Retries:         retries,
		Class:           class,
	}
	w.record(m)
	// Bank this run's ACCURATE token totals (from the result event) into the task total. Each Develop
	// call is one run boundary (initial develop, then each improvement iteration), so the task total is
	// the sum of every run's accurate usage.
	if w.OnTokensDone != nil {
		w.OnTokensDone(in.Issue, resp.InputTokens, resp.OutputTokens)
	}

	switch class {
	case ClassSuccess:
		return orchestrator.DevResult{OK: true, Summary: resp.Result.Summary, ChangedFiles: paths(resp.Result.Files)}, nil
	case ClassSemantic:
		return orchestrator.DevResult{OK: false, Summary: resp.Result.Notes}, nil
	case ClassRateLimit, ClassAuthentication:
		// Health signal to the Resource Manager so the account is skipped and work fails over. The
		// runtime does NOT choose the next account — the Resource Manager does.
		w.cool(in.Account)
		return orchestrator.DevResult{OK: false, Summary: string(class)}, errors.New("worker: " + string(class))
	default: // infrastructure / timeout / cancellation / runtime_failure → return to the Policy Engine
		return orchestrator.DevResult{OK: false, Summary: string(class)}, errof(class, runErr)
	}
}

func (w *Worker) enter() { w.mu.Lock(); w.active++; w.mu.Unlock() }
func (w *Worker) leave() { w.mu.Lock(); w.active--; w.mu.Unlock() }

func (w *Worker) record(m Metrics) {
	w.mu.Lock()
	w.history = append(w.history, m)
	if len(w.history) > 200 {
		w.history = w.history[len(w.history)-200:]
	}
	w.mu.Unlock()
	if w.OnMetrics != nil {
		w.OnMetrics(m)
	}
}

func (w *Worker) cool(account string) {
	if account == "" {
		return
	}
	w.mu.Lock()
	w.cooled++
	w.failover++
	w.mu.Unlock()
	if w.Cooldown != nil {
		w.Cooldown(account, w.CooldownFor)
	}
}

// Snapshot is the Control Plane view of runtime state.
type Snapshot struct {
	Active         int       `json:"active_executions"`
	Cooldowns      int       `json:"cooldowns"`
	FailoverEvents int       `json:"failover_events"`
	Recent         []Metrics `json:"recent"`
}

// Snapshot returns runtime state for the console.
func (w *Worker) Snapshot() Snapshot {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(w.history)
	if n > 20 {
		n = 20
	}
	recent := make([]Metrics, n)
	copy(recent, w.history[len(w.history)-n:])
	return Snapshot{Active: w.active, Cooldowns: w.cooled, FailoverEvents: w.failover, Recent: recent}
}

// --- helpers ---

func accountEnv(a Account) []string {
	var env []string
	if a.ConfigDir != "" {
		env = append(env, "CLAUDE_CONFIG_DIR="+a.ConfigDir)
	}
	env = append(env, a.Env...)
	return env
}

// codexEnv pins the Codex account profile via CODEX_HOME (account isolation).
func codexEnv(a Account) []string {
	var env []string
	if a.ConfigDir != "" {
		env = append(env, "CODEX_HOME="+a.ConfigDir)
	}
	env = append(env, a.Env...)
	return env
}

func paths(files []assignment.File) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Path
	}
	return out
}

func errof(class Class, err error) error {
	if err != nil {
		return err
	}
	return errors.New("worker: " + string(class))
}

// classify maps an execution to a Class. Order matters: cancellation/timeout are detected from the
// contexts first, then provider-error markers, then success/semantic.
func classify(parent, attempt context.Context, err error, resp runtime.Response) Class {
	if err == nil {
		if resp.Result.OK {
			return ClassSuccess
		}
		return ClassSemantic
	}
	// Parent cancelled (shutdown) beats a derived timeout.
	if errors.Is(parent.Err(), context.Canceled) {
		return ClassCancellation
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(attempt.Err(), context.DeadlineExceeded) {
		return ClassTimeout
	}
	s := strings.ToLower(err.Error())
	switch {
	case containsAny(s, "rate limit", "429", "quota", "usage limit", "overloaded"):
		return ClassRateLimit
	case containsAny(s, "unauthorized", "401", "authentication", "not logged in", "invalid api key", "login"):
		return ClassAuthentication
	case containsAny(s, "executable file not found", "no such file", "connection refused", "network", "temporary failure", "timeout"):
		return ClassInfrastructure
	default:
		return ClassRuntimeFailure
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
