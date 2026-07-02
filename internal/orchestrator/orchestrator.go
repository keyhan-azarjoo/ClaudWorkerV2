// Package orchestrator is the Orchestrator / Serve Loop (docs/21 S11) — the heart of ClaudWorker V2.
//
// It owns the EXECUTION LOOP and nothing else: startup, shutdown, scheduling, event flow,
// orchestration, and subsystem wiring. It connects the existing subsystems and NEVER duplicates their
// logic — every step calls the real subsystem. Resource usage always goes Policy → Resource → Lease,
// never bypassing them. Every significant transition publishes an event to the Control Plane so the
// Operations Console (its Timeline especially) tells the complete story.
package orchestrator

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/myotgo/ClaudWorkerV2/internal/assignment"
	"github.com/myotgo/ClaudWorkerV2/internal/controlplane"
	"github.com/myotgo/ClaudWorkerV2/internal/improvement"
	"github.com/myotgo/ClaudWorkerV2/internal/knowledge"
	"github.com/myotgo/ClaudWorkerV2/internal/lease"
	"github.com/myotgo/ClaudWorkerV2/internal/policy"
	"github.com/myotgo/ClaudWorkerV2/internal/resource"
	"github.com/myotgo/ClaudWorkerV2/internal/verify"
)

// --- Ports for the external / non-deterministic edges (real adapters or deterministic fakes) ---

// Issue is the minimal Jira issue the loop needs (task identity + what to build).
type Issue struct {
	Key                string
	Summary            string
	AcceptanceCriteria string
}

// Jira is the work source. Real impl wraps jira.Client; tests inject a deterministic fake.
type Jira interface {
	Eligible(ctx context.Context) ([]Issue, error)
	Get(ctx context.Context, key string) (Issue, error)
	Transition(ctx context.Context, key, to string) error
	Comment(ctx context.Context, key, text string) error
}

// DevInput is what a develop/improve step gives the runtime. The prompt is built from ONLY the four
// permitted fields (Issue+Summary = Assignment, AcceptanceCriteria, KnowledgeContext, and relevant
// files the runtime reads from the worktree). Runtime + Account are execution routing (which provider,
// which Resource-Manager-selected account) — they NEVER enter the prompt. Account was added in Phase
// 2.3 so the Worker Runtime executes under the account the Resource Manager selected (multi-account);
// the runtime still never CHOOSES the account.
type DevInput struct {
	Issue, Summary, AcceptanceCriteria, KnowledgeContext, Runtime, Account string
}

// DevResult is a worker outcome (files changed). Real impl wraps runtime.Runner; tests fake it.
type DevResult struct {
	OK           bool
	Summary      string
	ChangedFiles []string
}

// Developer runs the Worker Runtime for one attempt. It is the only non-deterministic edge.
type Developer interface {
	Develop(ctx context.Context, in DevInput) (DevResult, error)
}

// Verifier verifies the current work for an issue. Real impl wraps verify.Engine + a driver.
type Verifier interface {
	Verify(ctx context.Context, issue string) ([]verify.Result, error)
}

// Merger merges the assignment branch into the integration branch. Real impl wraps git.Git.
type Merger interface {
	Merge(ctx context.Context, issue string) (bool, error)
}

// Workspace optionally cleans up an assignment's Git workspace (worktree + branch) when the
// assignment reaches a terminal state. It is OPTIONAL and nil-safe: simulation (and every pre-Phase-2.2
// wiring) leaves it unset and is unaffected. It was introduced in Phase 2.2 because a real Git edge
// must clean worktrees/branches on completion AND failure (safety), which no existing port covered —
// the minimal change that keeps the loop safe without touching the Assignment or Policy engines.
type Workspace interface {
	Cleanup(ctx context.Context, issue string) error
}

// --- Orchestrator ---

// Config carries the loop's non-subsystem knobs.
type Config struct {
	DevBranch           string // integration branch (for the merge lease)
	MaxImprovementIters int    // safety ceiling for the improvement loop (0 = default)
	InProgressStatus    string // Jira status name for "in progress"
	DoneStatus          string // Jira status name for "done"
	StartActive         bool   // if false (default), the loop starts IDLE and claims no work until
	//                            orchestrator.start is issued (manual "Start Working"). Interrupted
	//                            assignments are also not auto-resumed while idle.
}

// Orchestrator wires the subsystems and runs the loop. It holds subsystem INSTANCES (deterministic,
// in-process) and PORTS for the external edges. It contains no business logic of its own beyond
// sequencing + event publishing.
type Orchestrator struct {
	Resources *resource.Manager
	Policy    *policy.Engine
	Leases    *lease.Manager
	Knowledge *knowledge.Brain
	Verify    *verify.Engine // present for completeness/registration; verification runs via the Verifier port
	Store     assignment.Store
	CP        *controlplane.Server
	Jira      Jira
	Developer Developer
	Verifier  Verifier
	Merger    Merger
	Cleaner   Workspace // optional (Phase 2.2); nil in simulation
	Cfg       Config
	Log       *slog.Logger

	now     func() time.Time
	trigger chan struct{}

	mu        sync.Mutex
	counters  map[string]int
	decisions []map[string]any // policy-decision ring for the Policies page
	lastIssue string
	running   bool // the serve loop goroutine is alive
	active    bool // the loop is claiming/processing work (vs. attached-but-idle)
}

// SetActive turns work processing on or off. Turning it on wakes the loop so it drains eligible work;
// turning it off leaves the loop alive but idle (it claims nothing new). Safe to call from a command.
func (o *Orchestrator) SetActive(a bool) {
	o.mu.Lock()
	o.active = a
	o.mu.Unlock()
	if a {
		o.Notify()
	}
}

// IsActive reports whether the loop is currently processing work.
func (o *Orchestrator) IsActive() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.active
}

// Option configures the Orchestrator.
type Option func(*Orchestrator)

// WithClock overrides the time source (deterministic tests).
func WithClock(now func() time.Time) Option { return func(o *Orchestrator) { o.now = now } }

// New builds an Orchestrator from a pre-populated struct (collaborators set on its fields) and wires
// defaults. Pass a pointer so the embedded mutex is never copied.
func New(p *Orchestrator, opts ...Option) *Orchestrator {
	if p.now == nil {
		p.now = time.Now
	}
	if p.Cfg.DevBranch == "" {
		p.Cfg.DevBranch = "development"
	}
	if p.Cfg.MaxImprovementIters <= 0 {
		p.Cfg.MaxImprovementIters = 10
	}
	if p.Cfg.InProgressStatus == "" {
		p.Cfg.InProgressStatus = "In Progress"
	}
	if p.Cfg.DoneStatus == "" {
		p.Cfg.DoneStatus = "Done"
	}
	p.trigger = make(chan struct{}, 1)
	p.counters = map[string]int{}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func (o *Orchestrator) log() *slog.Logger {
	if o.Log != nil {
		return o.Log
	}
	return slog.New(slog.DiscardHandler)
}

// emit publishes an event to the Control Plane (if wired) and records a counter. Every significant
// transition calls this — the Timeline is the sum of these events.
func (o *Orchestrator) emit(evType, subsystem string, data any) {
	o.mu.Lock()
	o.counters[evType]++
	if evType == controlplane.EventPolicyDecision {
		if m, ok := data.(map[string]any); ok {
			o.decisions = append(o.decisions, m)
			if len(o.decisions) > 200 {
				o.decisions = o.decisions[len(o.decisions)-200:]
			}
		}
	}
	o.mu.Unlock()
	if o.CP != nil {
		o.CP.Bus().Publish(evType, subsystem, data)
	}
	o.log().Info("event", "type", evType, "subsystem", subsystem)
}

func (o *Orchestrator) count(name string) {
	o.mu.Lock()
	o.counters[name]++
	o.mu.Unlock()
}

// Notify wakes the loop (e.g. when new Jira work arrives). Non-blocking → no busy waiting.
func (o *Orchestrator) Notify() {
	select {
	case o.trigger <- struct{}{}:
	default:
	}
}

// Run is the serve loop: drain all eligible work, then BLOCK until notified or the context is
// cancelled (shutdown). No polling, no busy waiting.
func (o *Orchestrator) Run(ctx context.Context) error {
	o.mu.Lock()
	o.running = true
	o.active = o.Cfg.StartActive // idle by default unless configured to start active
	o.mu.Unlock()
	defer func() { o.mu.Lock(); o.running = false; o.mu.Unlock() }()

	if err := o.Recover(ctx); err != nil {
		o.log().Error("orchestrator", "op", "recover", "error", err.Error())
	}
	for {
		for o.IsActive() { // drain only while active; idle otherwise (no work claimed)
			did, err := o.ProcessOnce(ctx)
			if err != nil {
				o.log().Error("orchestrator", "op", "process", "error", err.Error())
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !did {
				break
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-o.trigger:
		}
	}
}

// Recover runs on startup: reap expired leases, then resume unfinished assignments WITHOUT restarting
// completed ones (terminal states are skipped). Leases are durable and re-acquired idempotently.
func (o *Orchestrator) Recover(ctx context.Context) error {
	if n, err := o.Leases.Reap(); err == nil && n > 0 {
		o.emit(controlplane.EventLeaseExpired, "lease", map[string]any{"reaped": n})
	}
	all, err := o.Store.List()
	if err != nil {
		return err
	}
	for _, a := range all {
		if a.State.Terminal() {
			continue // never restart completed work (Law 19)
		}
		if !o.IsActive() {
			continue // idle: do not auto-resume interrupted work until manually started
		}
		iss, err := o.Jira.Get(ctx, a.IssueKey)
		if err != nil {
			continue
		}
		o.emit("AssignmentResumed", "orchestrator", map[string]any{"issue": a.IssueKey, "state": string(a.State)})
		_ = o.runAssignment(ctx, a, iss)
	}
	return nil
}

// ProcessOnce claims and runs the next eligible issue. Returns did=false when there is nothing to do
// (an idle engine is a success). It always goes Policy (budget) → then claim → Resource → Lease.
func (o *Orchestrator) ProcessOnce(ctx context.Context) (bool, error) {
	// Refresh policies (budget gate) — do not even claim work while paused.
	bd := o.Policy.Budget.Decide(policy.BudgetInput{UsagePct: 0, UsageKnown: true})
	o.emit(controlplane.EventPolicyDecision, "policy", map[string]any{"policy": "budget", "decision": pick(bd.Allow, "continue", "defer"), "reason": bd.Reason})
	if !bd.Allow {
		return false, nil
	}

	a, iss, ok, err := o.claimNext(ctx)
	if err != nil || !ok {
		return false, err
	}
	return true, o.runAssignment(ctx, a, iss)
}

// claimNext finds the first eligible, unclaimed issue and claims it: persist the Assignment, acquire
// the ISSUE lease, move Jira to in-progress, and publish. It never re-claims an issue that already has
// an Assignment (issue lock + no redo).
func (o *Orchestrator) claimNext(ctx context.Context) (*assignment.Assignment, Issue, bool, error) {
	issues, err := o.Jira.Eligible(ctx)
	if err != nil {
		return nil, Issue{}, false, err
	}
	for _, iss := range issues {
		if _, exists, err := o.Store.Load(iss.Key); err != nil {
			return nil, Issue{}, false, err
		} else if exists {
			continue
		}
		a := &assignment.Assignment{IssueKey: iss.Key, State: assignment.StateClaimed}
		if err := o.Store.Save(a); err != nil {
			return nil, Issue{}, false, err
		}
		// Lease: issue ownership (Policy→Resource→Lease; the issue lease needs no resource reservation).
		if l, granted, _ := o.Leases.Acquire(lease.Request{Kind: lease.KindIssue, Resource: iss.Key, Owner: iss.Key, Reason: "claim", Renewable: true}); granted {
			o.emit(controlplane.EventLeaseGranted, "lease", l)
		}
		if o.Cfg.InProgressStatus != "" {
			_ = o.Jira.Transition(ctx, iss.Key, o.Cfg.InProgressStatus)
		}
		_ = o.Jira.Comment(ctx, iss.Key, "ClaudWorker claimed this issue")
		o.mu.Lock()
		o.lastIssue = iss.Key
		o.mu.Unlock()
		o.emit(controlplane.EventAssignmentCreated, "assignment", map[string]any{"issue": iss.Key, "state": string(a.State)})
		return a, iss, true, nil
	}
	return nil, Issue{}, false, nil
}

// keywords derives deterministic relevance terms from the task for the Knowledge Brain.
func keywords(iss Issue) []string {
	fields := strings.Fields(strings.ToLower(iss.Summary + " " + iss.AcceptanceCriteria))
	seen := map[string]bool{}
	var out []string
	for _, w := range fields {
		w = strings.Trim(w, ".,:;()[]{}\"'`")
		if len(w) < 3 || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

func pick(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

// buildImprovement wires S8 (verify) + S9 (improve) + S6 (policy) into the improvement loop for one
// issue, reusing each subsystem — the Orchestrator adds no loop logic of its own.
func (o *Orchestrator) buildImprovement(iss Issue, kctx, runtimeName, account string) *improvement.Engine {
	verifier := &impVerifier{o: o, issue: iss.Key}
	improver := &impImprover{o: o, iss: iss, kctx: kctx, runtime: runtimeName, account: account}
	dec := improvement.NewPolicyDecider(o.Policy)
	eng := improvement.New(verifier, improver, dec, improvement.WithClock(o.now))
	eng.MaxIterations = o.Cfg.MaxImprovementIters
	eng.Log = o.Log
	return eng
}
