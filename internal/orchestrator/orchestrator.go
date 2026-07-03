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
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
	OperatorNote                                                           string   // human guidance from a manual Continue
	Rules                                                                  []string // standing rules every agent must follow
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

// TaskAction is one entry in a task's activity timeline (a stage transition for the dashboard boxes).
type TaskAction struct {
	Stage  string    `json:"stage"`
	Status string    `json:"status"`
	Detail string    `json:"detail,omitempty"`
	At     time.Time `json:"at"`
}

// TaskActivity is the ordered activity timeline for one issue (newest task first in the query result).
type TaskActivity struct {
	Issue     string       `json:"issue"`
	State     string       `json:"state"`
	Account   string       `json:"account,omitempty"`
	StartedAt time.Time    `json:"started_at"`
	Actions   []TaskAction `json:"actions"`
	TokensIn  int          `json:"tokens_in"`  // ACCURATE tokens sent for this task (live, cumulative)
	TokensOut int          `json:"tokens_out"` // ACCURATE tokens received for this task (live, cumulative)
	Agents    int          `json:"agents"`     // how many agents worked on this task (main + sub-agents spawned)

	// per-run token bookkeeping (unexported; not serialized). Each Develop run reports a cumulative
	// count that starts near 0; when it drops we've begun a new run, so we bank the finished run.
	curIn, curOut, bankIn, bankOut int
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

	// WorkAllowed optionally gates ALL work (auto-claim + manual run). It returns false + a reason when
	// the project is deactivated (e.g. every repo turned off on the Git page), so agents do nothing.
	// nil = always allowed.
	WorkAllowed func() (bool, string)

	// Rules optionally supplies the standing rules every agent must obey (injected into the prompt so the
	// main agent reads them before any change). nil = no extra rules.
	Rules func() []string

	now     func() time.Time
	trigger chan struct{}

	TaskLogDir string // if set, per-task agent transcripts are persisted here (survive restarts)

	mu         sync.Mutex
	counters   map[string]int
	taskLog    map[string]*TaskActivity
	taskStream map[string][]string // per-issue live agent activity lines (bounded; also persisted)
	inflight   map[string]bool     // issues currently executing — guards against double-launch
	decisions  []map[string]any    // policy-decision ring for the Policies page
	lastIssue  string
	running    bool // the serve loop goroutine is alive
	active     bool // the loop is claiming/processing work (vs. attached-but-idle)
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

// activeRules returns the standing rules to inject into the worker prompt (nil-safe).
func (o *Orchestrator) activeRules() []string {
	if o.Rules != nil {
		return o.Rules()
	}
	return nil
}

// IsActive reports whether the loop is currently processing work.
func (o *Orchestrator) IsActive() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.active
}

// AppendTaskLog records one live activity line for a task (what the agent is thinking/doing). It keeps
// the recent lines in memory AND appends to a per-task file (TaskLogDir) so the full agent transcript
// survives restarts and can be reviewed for DONE tasks.
func (o *Orchestrator) AppendTaskLog(issue, line string) {
	o.mu.Lock()
	if o.taskStream == nil {
		o.taskStream = map[string][]string{}
	}
	s := append(o.taskStream[issue], line)
	if len(s) > 4000 {
		s = s[len(s)-4000:]
	}
	o.taskStream[issue] = s
	// Count how many agents worked on this task: the main agent (1) plus every sub-agent it spawned
	// (each "🤖 sub-agent" line = one Task-tool fan-out). Persisted with the activity, so DONE tasks keep it.
	t := o.ensureTask(issue)
	if t.Agents == 0 {
		t.Agents = 1 // the orchestrating agent
	}
	if strings.HasPrefix(line, "🤖 sub-agent") {
		t.Agents++
	}
	dir := o.TaskLogDir
	o.mu.Unlock()

	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err == nil {
			if f, err := os.OpenFile(filepath.Join(dir, logSlug(issue)+".log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
				_, _ = f.WriteString(line + "\n")
				_ = f.Close()
			}
		}
	}
}

// ensureTask returns the TaskActivity for an issue, creating it if needed. Caller holds o.mu.
func (o *Orchestrator) ensureTask(issue string) *TaskActivity {
	if o.taskLog == nil {
		o.taskLog = map[string]*TaskActivity{}
	}
	t := o.taskLog[issue]
	if t == nil {
		t = &TaskActivity{Issue: issue, StartedAt: o.clock()()}
		o.taskLog[issue] = t
	}
	return t
}

// SetTaskTokens records the LIVE (rough) token usage for the CURRENT run so the dashboard count moves
// during a long run. It is a progress indicator only — the ACCURATE per-run totals are banked by
// BankTaskTokens at each run boundary (from the provider's terminal result event). Task total shown =
// banked (completed runs) + current-run live.
func (o *Orchestrator) SetTaskTokens(issue string, in, out int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	t := o.ensureTask(issue)
	t.curIn, t.curOut = in, out
	t.TokensIn = t.bankIn + t.curIn
	t.TokensOut = t.bankOut + t.curOut
}

// BankTaskTokens adds one finished run's ACCURATE token totals (from the provider result event) to the
// task total and clears the live current-run counters. Called once per Develop run (initial + each
// improvement iteration), so the task total is the accurate sum across the whole task. Persists the new
// totals so they survive a restart.
func (o *Orchestrator) BankTaskTokens(issue string, in, out int) {
	o.mu.Lock()
	t := o.ensureTask(issue)
	t.bankIn += in
	t.bankOut += out
	t.curIn, t.curOut = 0, 0
	t.TokensIn = t.bankIn
	t.TokensOut = t.bankOut
	snap := snapshotActivityLocked(t)
	dir := o.TaskLogDir
	o.mu.Unlock()
	o.writeActivity(dir, snap)
}

// TaskTokens returns the accurate cumulative (sent, received) tokens for a task.
func (o *Orchestrator) TaskTokens(issue string) (in, out int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if t := o.taskLog[issue]; t != nil {
		return t.TokensIn, t.TokensOut
	}
	return 0, 0
}

// TaskStream returns the recorded agent transcript for a task (oldest first). It reads the persisted
// file when available (so DONE tasks / post-restart still show the full report), falling back to the
// in-memory buffer.
func (o *Orchestrator) TaskStream(issue string) []string {
	o.mu.Lock()
	mem := append([]string(nil), o.taskStream[issue]...)
	dir := o.TaskLogDir
	o.mu.Unlock()

	if dir != "" {
		if b, err := os.ReadFile(filepath.Join(dir, logSlug(issue)+".log")); err == nil {
			lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
			if len(lines) > 4000 {
				lines = lines[len(lines)-4000:]
			}
			if len(lines) >= len(mem) { // the file is the durable superset
				return lines
			}
		}
	}
	if mem == nil {
		return []string{} // never marshal null — the console expects an array
	}
	return mem
}

// logSlug makes a filesystem-safe file name from an issue key.
func logSlug(issue string) string {
	var b strings.Builder
	for _, r := range issue {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	s := b.String()
	if s == "" {
		return "task"
	}
	return s
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
	p.taskLog = map[string]*TaskActivity{}
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

// clock returns the loop's time source (deterministic in tests; time.Now otherwise).
func (o *Orchestrator) clock() func() time.Time {
	if o.now != nil {
		return o.now
	}
	return time.Now
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

// recordAction appends one action to a task's activity timeline (for the dashboard task boxes) and
// persists the snapshot so the box + timeline survive a restart (write happens outside the lock).
func (o *Orchestrator) recordAction(issue, stage, status, detail string) {
	o.mu.Lock()
	if o.taskLog == nil {
		o.taskLog = map[string]*TaskActivity{}
	}
	t := o.taskLog[issue]
	if t == nil {
		// bound growth: if at capacity, drop the oldest task first.
		if len(o.taskLog) >= 40 {
			var oldestKey string
			var oldest time.Time
			for k, v := range o.taskLog {
				if oldestKey == "" || v.StartedAt.Before(oldest) {
					oldestKey, oldest = k, v.StartedAt
				}
			}
			delete(o.taskLog, oldestKey)
		}
		t = &TaskActivity{Issue: issue, StartedAt: o.clock()()}
		o.taskLog[issue] = t
	}
	t.Actions = append(t.Actions, TaskAction{Stage: stage, Status: status, Detail: detail, At: o.clock()()})
	if stage == "runtime" && detail != "" {
		t.Account = detail
	}
	snap := snapshotActivityLocked(t)
	dir := o.TaskLogDir
	o.mu.Unlock()
	o.writeActivity(dir, snap)
}

// snapshotActivityLocked deep-copies a TaskActivity (caller holds o.mu) so it can be written to disk
// outside the lock without racing further mutations.
func snapshotActivityLocked(t *TaskActivity) *TaskActivity {
	cp := *t
	cp.Actions = append([]TaskAction(nil), t.Actions...)
	return &cp
}

// writeActivity persists one task's activity snapshot (boxes + stage timeline + account + token totals)
// to <TaskLogDir>/<slug>.activity.json so it survives a restart. Called after each stage transition
// (infrequent). No-op when TaskLogDir is unset. Never holds o.mu.
func (o *Orchestrator) writeActivity(dir string, t *TaskActivity) {
	if dir == "" || t == nil {
		return
	}
	b, err := json.Marshal(t)
	if err != nil {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, logSlug(t.Issue)+".activity.json"), b, 0o644)
}

// loadActivity restores persisted task activity on startup so DONE/failed task boxes (with their stage
// timeline and accurate token totals) remain visible after a restart. Prior token totals are banked so a
// resumed run ADDS to them instead of resetting to zero.
func (o *Orchestrator) loadActivity() {
	dir := o.TaskLogDir
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.taskLog == nil {
		o.taskLog = map[string]*TaskActivity{}
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".activity.json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var t TaskActivity
		if json.Unmarshal(b, &t) != nil || t.Issue == "" {
			continue
		}
		t.bankIn, t.bankOut = t.TokensIn, t.TokensOut // resumed runs add to the prior total
		t.curIn, t.curOut = 0, 0
		// Backfill the agent count for tasks that ran before it was tracked: count sub-agent spawns in
		// the persisted transcript (+1 for the main agent).
		if t.Agents == 0 {
			if b, err := os.ReadFile(filepath.Join(dir, logSlug(t.Issue)+".log")); err == nil && len(b) > 0 {
				t.Agents = strings.Count(string(b), "🤖 sub-agent") + 1
			}
		}
		o.taskLog[t.Issue] = &t
	}
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
	// Restore the dashboard task boxes (timeline + tokens) persisted before the restart, then backfill a
	// minimal box for any known assignment that predates activity persistence — so every task the engine
	// has ever touched stays visible after a restart (the query overlays the live State from the Store).
	o.loadActivity()
	if all, err := o.Store.List(); err == nil {
		o.mu.Lock()
		for _, a := range all {
			if _, ok := o.taskLog[a.IssueKey]; !ok {
				o.taskLog[a.IssueKey] = &TaskActivity{Issue: a.IssueKey, State: string(a.State), StartedAt: o.clock()()}
			}
		}
		o.mu.Unlock()
	}

	if n, err := o.Leases.Reap(); err == nil && n > 0 {
		o.emit(controlplane.EventLeaseExpired, "lease", map[string]any{"reaped": n})
	}
	// Release STALE per-run leases. Resource + merge leases are only valid for a live execution; on
	// restart every prior execution is gone, so a killed run must not leave its account leased forever
	// (that blocks all new runs at acquireRuntime). Also free issue leases whose assignment is terminal
	// or no longer exists.
	if act, lerr := o.Leases.Active(); lerr == nil {
		for _, l := range act {
			switch l.Kind {
			case lease.KindResource, lease.KindMerge:
				_, _ = o.Leases.Release(l.Kind, l.Resource, l.Owner)
			case lease.KindIssue:
				if a, ok, _ := o.Store.Load(l.Owner); !ok || a.State.Terminal() {
					_, _ = o.Leases.Release(l.Kind, l.Resource, l.Owner)
				}
			}
		}
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
		_ = o.runAssignment(ctx, a, iss, "", "")
	}
	return nil
}

// ProcessOnce claims and runs the next eligible issue. Returns did=false when there is nothing to do
// (an idle engine is a success). It always goes Policy (budget) → then claim → Resource → Lease.
func (o *Orchestrator) ProcessOnce(ctx context.Context) (bool, error) {
	// Project gate: if deactivated (no active repo), claim nothing.
	if o.WorkAllowed != nil {
		if ok, _ := o.WorkAllowed(); !ok {
			return false, nil
		}
	}
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
	return true, o.runAssignment(ctx, a, iss, "", "")
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
		o.recordAction(iss.Key, "claimed", "done", "")
		return a, iss, true, nil
	}
	return nil, Issue{}, false, nil
}

// RunIssue claims and runs ONE specific issue on demand (operator "run this ticket"), bypassing the
// ready-label queue and the idle gate. It still respects the budget gate and the issue lock (never
// redoes terminal work). Intended to be called in a goroutine — it runs the full pipeline.
func (o *Orchestrator) RunIssue(ctx context.Context, key, account, operatorNote string) (bool, error) {
	if key == "" {
		return false, fmt.Errorf("issue key required")
	}
	// Project gate: a deactivated project (no active repo) can't be worked, even on a manual Run.
	if o.WorkAllowed != nil {
		if ok, reason := o.WorkAllowed(); !ok {
			return false, fmt.Errorf("project is deactivated: %s", reason)
		}
	}
	bd := o.Policy.Budget.Decide(policy.BudgetInput{UsagePct: 0, UsageKnown: true})
	if !bd.Allow {
		return false, fmt.Errorf("budget gate: %s", bd.Reason)
	}
	// Already tracked? Resume if unfinished; refuse to redo terminal work (Law 19).
	if a, exists, err := o.Store.Load(key); err != nil {
		return false, err
	} else if exists {
		if a.State.Terminal() {
			return false, fmt.Errorf("issue %s is already %s", key, a.State)
		}
		iss, err := o.Jira.Get(ctx, key)
		if err != nil {
			return false, err
		}
		return true, o.runAssignment(ctx, a, iss, account, operatorNote)
	}
	// Fresh claim for this specific issue.
	iss, err := o.Jira.Get(ctx, key)
	if err != nil {
		return false, err
	}
	a := &assignment.Assignment{IssueKey: key, State: assignment.StateClaimed}
	if err := o.Store.Save(a); err != nil {
		return false, err
	}
	if l, granted, _ := o.Leases.Acquire(lease.Request{Kind: lease.KindIssue, Resource: key, Owner: key, Reason: "manual-run", Renewable: true}); granted {
		o.emit(controlplane.EventLeaseGranted, "lease", l)
	}
	if o.Cfg.InProgressStatus != "" {
		_ = o.Jira.Transition(ctx, key, o.Cfg.InProgressStatus)
	}
	_ = o.Jira.Comment(ctx, key, "ClaudWorker claimed this issue (manual run)")
	o.mu.Lock()
	o.lastIssue = key
	o.mu.Unlock()
	o.emit(controlplane.EventAssignmentCreated, "assignment", map[string]any{"issue": key, "state": string(a.State)})
	o.recordAction(key, "claimed", "done", "")
	return true, o.runAssignment(ctx, a, iss, account, operatorNote)
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
func (o *Orchestrator) buildImprovement(iss Issue, kctx, runtimeName, account, operatorNote string) *improvement.Engine {
	verifier := &impVerifier{o: o, issue: iss.Key}
	improver := &impImprover{o: o, iss: iss, kctx: kctx, runtime: runtimeName, account: account, operatorNote: operatorNote}
	dec := improvement.NewPolicyDecider(o.Policy)
	eng := improvement.New(verifier, improver, dec, improvement.WithClock(o.now))
	eng.MaxIterations = o.Cfg.MaxImprovementIters
	eng.Log = o.Log
	return eng
}
