// Package improvement is the Improvement Engine (docs/17, docs/21 S9 — renamed from Repair Loop).
//
// The platform IMPROVES software, not only repairs failures. Each iteration the engine runs the
// verify → improve → verify loop, but it delegates everything except orchestration:
//   - verification to a Verifier port (the S8 Verification Engine) — it does NOT verify;
//   - the actual improvement to an Improver port (which wraps the Worker Runtime, S5) — the ONLY
//     non-deterministic step;
//   - the STOP decision to a StopDecider port (the S6 Policy Engine) — it never decides when to stop.
//
// The improvement step receives ONLY: Assignment, Verification Results, Knowledge Context, Relevant
// Files. Nothing else (no execution state, no Git, no Jira, no policy internals).
//
// It does NOT verify, merge, update Jira, own Assignment state, or own policies. It is deterministic
// apart from the Improver (worker): it never hides retries (every iteration is recorded), every
// iteration reports measurable progress (verification delta), repeated identical failures are
// detected, and a hard iteration ceiling makes infinite loops impossible.
package improvement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"sort"
	"strings"
	"time"

	"claudworker/internal/verify"
)

// Category documents the KIND of improvement an iteration made (observability). The engine does not
// choose it — the Improver reports it.
type Category string

const (
	CatDefect          Category = "defect"
	CatReliability     Category = "reliability"
	CatPerformance     Category = "performance"
	CatUX              Category = "ux"
	CatMaintainability Category = "maintainability"
	CatAccessibility   Category = "accessibility"
	CatSecurity        Category = "security"
	CatDocumentation   Category = "documentation"
)

// File is a path + content pair (relevant-file context for the Improver).
type File struct {
	Path    string
	Content string
}

// ImprovementInput is EXACTLY what the improve step receives — the four permitted inputs, nothing
// more.
type ImprovementInput struct {
	Assignment          string          // owning Assignment identity (opaque issue key)
	VerificationResults []verify.Result // why the last verification did not pass
	KnowledgeContext    string          // from the Knowledge Brain (S4)
	RelevantFiles       []File          // files in scope
}

// Change is what one improvement produced (observable). The Improver applies the change; the engine
// only records what happened.
type Change struct {
	Category     Category
	Reason       string   // improvement reason (tracked)
	ChangedFiles []string // files changed this iteration
}

// Verifier delegates to the Verification Engine (S8). The improvement engine never verifies itself.
type Verifier interface {
	Verify(ctx context.Context) ([]verify.Result, error)
}

// Improver performs ONE improvement from the four permitted inputs and returns what it changed. It is
// the only non-deterministic dependency (it wraps the Worker Runtime).
type Improver interface {
	Improve(ctx context.Context, in ImprovementInput) (Change, error)
}

// Decision is the Policy Engine's verdict on whether to keep improving.
type Decision string

const (
	Continue Decision = "continue"
	Defer    Decision = "defer"
	Escalate Decision = "escalate"
	Fail     Decision = "fail"
)

// StopInput is the observable state the engine hands the policy each failed iteration.
type StopInput struct {
	Iteration int            // 1-based
	Failures  int            // non-passing verification results
	Delta     int            // failures reduced vs previous iteration (positive = progress)
	Stuck     bool           // identical failure repeated >= StuckThreshold times with no progress
	Outcome   verify.Outcome // aggregated verification outcome
}

// StopDecider is the Policy Engine port. The Improvement Engine asks it after every failed
// verification; the policy — never the engine — decides continue/defer/escalate/fail.
type StopDecider interface {
	Decide(in StopInput) Decision
}

// Status is the terminal state of a loop run.
type Status string

const (
	StatusPassed    Status = "passed"    // verification passed
	StatusDeferred  Status = "deferred"  // policy deferred
	StatusEscalated Status = "escalated" // policy escalated to a human
	StatusFailed    Status = "failed"    // policy gave up
	StatusExhausted Status = "exhausted" // hit the hard iteration ceiling (safety backstop)
)

// IterationRecord makes every iteration observable (nothing hidden).
type IterationRecord struct {
	N            int            `json:"n"`
	Outcome      verify.Outcome `json:"outcome"`
	Failures     int            `json:"failures"`
	Delta        int            `json:"delta"`
	ChangedFiles []string       `json:"changed_files,omitempty"`
	Category     Category       `json:"category,omitempty"`
	Reason       string         `json:"reason"`
	Elapsed      time.Duration  `json:"elapsed"`
	Signature    string         `json:"signature"`
}

// Progress is the observable summary of a run.
type Progress struct {
	Iterations   int               `json:"iterations"`
	ChangedFiles []string          `json:"changed_files"` // cumulative, unique, sorted
	FinalOutcome verify.Outcome    `json:"final_outcome"`
	Elapsed      time.Duration     `json:"elapsed"`
	Records      []IterationRecord `json:"records"`
}

// Result is the outcome of a whole loop run.
type Result struct {
	Status   Status
	Progress Progress
}

// Engine orchestrates the loop. All decisions are delegated to the ports; the only engine-owned
// numbers are safety invariants (MaxIterations, StuckThreshold) that guarantee termination.
type Engine struct {
	Verifier       Verifier
	Improver       Improver
	Policy         StopDecider
	MaxIterations  int // hard safety ceiling — makes infinite loops impossible (default 20)
	StuckThreshold int // identical-failure repeats → Stuck (default 2)
	Log            *slog.Logger
	now            func() time.Time
}

// Option configures the Engine.
type Option func(*Engine)

// WithClock overrides the time source (deterministic elapsed in tests).
func WithClock(now func() time.Time) Option { return func(e *Engine) { e.now = now } }

// New builds an Engine, applying safe defaults for the termination invariants.
func New(v Verifier, im Improver, p StopDecider, opts ...Option) *Engine {
	e := &Engine{Verifier: v, Improver: im, Policy: p, MaxIterations: 20, StuckThreshold: 2, now: time.Now}
	for _, o := range opts {
		o(e)
	}
	if e.MaxIterations <= 0 {
		e.MaxIterations = 20
	}
	if e.StuckThreshold <= 0 {
		e.StuckThreshold = 2
	}
	return e
}

func (e *Engine) log() *slog.Logger {
	if e.Log != nil {
		return e.Log
	}
	return slog.New(slog.DiscardHandler)
}

// Run drives verify → improve → verify until the policy stops it, verification passes, or the safety
// ceiling is hit. It carries the four permitted inputs into each improve step and records every
// iteration. `base` supplies Assignment / KnowledgeContext / RelevantFiles (its VerificationResults
// are replaced each iteration by the fresh verification).
func (e *Engine) Run(ctx context.Context, base ImprovementInput) (Result, error) {
	start := e.now()
	prog := Progress{}
	changed := map[string]bool{}
	prevFailures := -1
	lastSig := ""
	sigRepeat := 0

	for {
		if prog.Iterations >= e.MaxIterations {
			// Safety backstop: never loop forever, even under a misconfigured policy. Reported loudly.
			e.log().Error("improvement", "op", "exhausted", "assignment", base.Assignment, "iterations", prog.Iterations)
			return e.finalize(StatusExhausted, verify.Inconclusive, &prog, start, changed), nil
		}
		iterN := prog.Iterations + 1

		results, err := e.Verifier.Verify(ctx)
		if err != nil {
			return Result{Progress: e.snapshot(&prog, verify.Inconclusive, start, changed)}, err
		}
		outcome := verify.Aggregate(results)
		failures := countFailures(results)
		sig := signature(results)
		delta := 0
		if prevFailures >= 0 {
			delta = prevFailures - failures
		}
		rec := IterationRecord{N: iterN, Outcome: outcome, Failures: failures, Delta: delta, Signature: sig, Elapsed: e.now().Sub(start)}

		if outcome == verify.Pass {
			rec.Reason = "verification passed"
			prog.Records = append(prog.Records, rec)
			prog.Iterations = iterN
			e.log().Info("improvement", "op", "passed", "assignment", base.Assignment, "iteration", iterN)
			return e.finalize(StatusPassed, outcome, &prog, start, changed), nil
		}

		// stuck detection: same failure signature and no reduction in failures
		if sig == lastSig && delta <= 0 {
			sigRepeat++
		} else {
			sigRepeat = 0
		}
		lastSig = sig
		prevFailures = failures
		stuck := sigRepeat >= e.StuckThreshold

		// The Policy Engine — never the improvement engine — decides whether to stop.
		decision := e.Policy.Decide(StopInput{Iteration: iterN, Failures: failures, Delta: delta, Stuck: stuck, Outcome: outcome})
		e.log().Info("improvement", "op", "decision", "assignment", base.Assignment, "iteration", iterN,
			"failures", failures, "delta", delta, "stuck", stuck, "decision", string(decision))
		switch decision {
		case Defer:
			rec.Reason = "policy: defer"
			prog.Records = append(prog.Records, rec)
			prog.Iterations = iterN
			return e.finalize(StatusDeferred, outcome, &prog, start, changed), nil
		case Escalate:
			rec.Reason = "policy: escalate"
			prog.Records = append(prog.Records, rec)
			prog.Iterations = iterN
			return e.finalize(StatusEscalated, outcome, &prog, start, changed), nil
		case Fail:
			rec.Reason = "policy: fail"
			prog.Records = append(prog.Records, rec)
			prog.Iterations = iterN
			return e.finalize(StatusFailed, outcome, &prog, start, changed), nil
		}

		// Continue: improve (worker runtime — the only non-determinism). Receives EXACTLY the four
		// permitted inputs.
		in := ImprovementInput{
			Assignment:          base.Assignment,
			VerificationResults: results,
			KnowledgeContext:    base.KnowledgeContext,
			RelevantFiles:       base.RelevantFiles,
		}
		change, err := e.Improver.Improve(ctx, in)
		if err != nil {
			// An improve failure is a real (recorded) iteration that made no progress; the next loop
			// lets the policy react (stuck). Never hidden.
			rec.Reason = "improve failed: " + err.Error()
			e.log().Warn("improvement", "op", "improve.error", "assignment", base.Assignment, "iteration", iterN, "error", err.Error())
		} else {
			rec.Category = change.Category
			rec.Reason = change.Reason
			rec.ChangedFiles = change.ChangedFiles
			for _, f := range change.ChangedFiles {
				changed[f] = true
			}
		}
		prog.Records = append(prog.Records, rec)
		prog.Iterations = iterN
	}
}

func (e *Engine) finalize(status Status, outcome verify.Outcome, prog *Progress, start time.Time, changed map[string]bool) Result {
	return Result{Status: status, Progress: e.snapshot(prog, outcome, start, changed)}
}

func (e *Engine) snapshot(prog *Progress, outcome verify.Outcome, start time.Time, changed map[string]bool) Progress {
	files := make([]string, 0, len(changed))
	for f := range changed {
		files = append(files, f)
	}
	sort.Strings(files)
	prog.ChangedFiles = files
	prog.FinalOutcome = outcome
	prog.Elapsed = e.now().Sub(start)
	return *prog
}

func countFailures(results []verify.Result) int {
	n := 0
	for _, r := range results {
		if r.Outcome != verify.Pass {
			n++
		}
	}
	return n
}

// signature is a deterministic fingerprint of the non-passing results (verifier + outcome + detail),
// used to detect repeated identical failures.
func signature(results []verify.Result) string {
	parts := make([]string, 0, len(results))
	for _, r := range results {
		if r.Outcome == verify.Pass {
			continue
		}
		parts = append(parts, string(r.Outcome)+"|"+r.Verifier+"|"+r.Detail)
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:8])
}
