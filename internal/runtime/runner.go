package runtime

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"claudworker/internal/assignment"
)

// Runner is the deterministic, provider-agnostic wrapper around a WorkerRuntime. It owns the
// cross-provider concerns the runtime is responsible for — per-attempt timeout, cancellation
// detection, transient retry, metrics, and logging — so provider code stays minimal.
//
// It implements assignment.Worker, so the Assignment Engine consumes it directly with NO change to
// the engine: swapping Claude for another provider is just a different WorkerRuntime here.
//
// Retry boundary: Runner retries only RUNTIME/infra errors (spawn/timeout/transport). A semantic
// failure (Result.OK=false, nil error) is passed straight through — the Assignment Engine owns
// bounded development retries across restarts (S3), and double-counting must be avoided.
type Runner struct {
	Runtime    WorkerRuntime
	Timeout    time.Duration    // per-attempt timeout; 0 = inherit ctx only
	MaxRetries int              // transient-error retries; 0 = none
	Log        *slog.Logger     // optional; DiscardHandler if nil
	OnMetrics  func(Metrics)    // optional sink for per-run metrics
	now        func() time.Time // injectable clock (tests); defaults to time.Now
}

func (r *Runner) log() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.New(slog.DiscardHandler)
}

func (r *Runner) clock() func() time.Time {
	if r.now != nil {
		return r.now
	}
	return time.Now
}

// Run executes one Assignment attempt with retry/timeout/metrics, satisfying assignment.Worker.
func (r *Runner) Run(ctx context.Context, in assignment.WorkerInput) (assignment.WorkerResult, error) {
	clock := r.clock()
	start := clock()
	m := Metrics{Runtime: r.Runtime.Name()}

	var (
		resp    Response
		runErr  error
		attempt int
	)
	for attempt = 0; attempt <= r.MaxRetries; attempt++ {
		if attempt > 0 {
			m.Retries++
			r.log().Warn("worker", "op", "retry", "issue", in.IssueKey, "runtime", r.Runtime.Name(), "attempt", attempt)
		}
		attemptCtx := ctx
		cancel := context.CancelFunc(func() {})
		if r.Timeout > 0 {
			attemptCtx, cancel = context.WithTimeout(ctx, r.Timeout)
		}
		spawn := clock()
		resp, runErr = r.Runtime.Run(attemptCtx, in)
		m.StartupTime = clock().Sub(spawn).String() // spawn→return of the last attempt (upper bound on startup)
		cancel()

		if runErr == nil {
			break // success (semantic OK/!OK both count as "ran")
		}
		// Parent cancelled → stop immediately; retrying a cancelled context is pointless.
		if errors.Is(ctx.Err(), context.Canceled) {
			break
		}
	}

	m.ExecutionTime = clock().Sub(start).String()
	m.PromptBytes = resp.PromptBytes
	m.CompletionBytes = resp.CompletionBytes
	m.TokenEstimate = EstimateTokens(resp.PromptBytes + resp.CompletionBytes)

	if runErr != nil {
		m.Failed = true
		m.Cancelled = errors.Is(ctx.Err(), context.Canceled)
		m.TimedOut = errors.Is(runErr, context.DeadlineExceeded) || (r.Timeout > 0 && errors.Is(ctx.Err(), context.DeadlineExceeded))
		r.emit(m)
		r.log().Error("worker", "op", "fail", "issue", in.IssueKey, "runtime", r.Runtime.Name(),
			"retries", m.Retries, "timed_out", m.TimedOut, "cancelled", m.Cancelled, "error", runErr.Error())
		return assignment.WorkerResult{}, runErr
	}

	r.emit(m)
	r.log().Info("worker", "op", "done", "issue", in.IssueKey, "runtime", r.Runtime.Name(),
		"ok", resp.Result.OK, "prompt_bytes", m.PromptBytes, "completion_bytes", m.CompletionBytes,
		"token_estimate", m.TokenEstimate, "retries", m.Retries)
	return resp.Result, nil
}

func (r *Runner) emit(m Metrics) {
	if r.OnMetrics != nil {
		r.OnMetrics(m)
	}
}
