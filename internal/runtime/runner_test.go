package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"claudworker/internal/assignment"
)

// fakeRuntime returns a scripted (Response,error) per call; the last repeats after exhaust. It also
// records how it was invoked and can block to exercise timeout/cancellation.
type fakeRuntime struct {
	mu       sync.Mutex
	resps    []Response
	errs     []error
	calls    int
	blockFor time.Duration
}

func (f *fakeRuntime) Name() string { return "fake" }

func (f *fakeRuntime) Run(ctx context.Context, in assignment.WorkerInput) (Response, error) {
	f.mu.Lock()
	i := f.calls
	f.calls++
	f.mu.Unlock()
	if f.blockFor > 0 {
		select {
		case <-time.After(f.blockFor):
		case <-ctx.Done():
			return Response{PromptBytes: 10}, ctx.Err()
		}
	}
	idx := i
	if idx >= len(f.resps) {
		idx = len(f.resps) - 1
	}
	var err error
	if i < len(f.errs) {
		err = f.errs[i]
	}
	return f.resps[idx], err
}

// steadyClock advances by a fixed step each call so ExecutionTime is deterministic in tests.
func steadyClock() func() time.Time {
	t := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	return func() time.Time {
		cur := t
		t = t.Add(time.Millisecond)
		return cur
	}
}

func okResp() Response {
	return Response{Result: assignment.WorkerResult{OK: true, Summary: "did it"}, PromptBytes: 400, CompletionBytes: 40}
}

func TestRunnerSuccessMetrics(t *testing.T) {
	var got Metrics
	r := &Runner{
		Runtime:   &fakeRuntime{resps: []Response{okResp()}},
		OnMetrics: func(m Metrics) { got = m },
		now:       steadyClock(),
	}
	res, err := r.Run(context.Background(), sampleInput())
	if err != nil || !res.OK {
		t.Fatalf("Run = %+v err=%v", res, err)
	}
	if got.Runtime != "fake" || got.Retries != 0 || got.Failed {
		t.Errorf("metrics = %+v", got)
	}
	if got.PromptBytes != 400 || got.CompletionBytes != 40 {
		t.Errorf("byte metrics = %+v", got)
	}
	if got.TokenEstimate != EstimateTokens(440) {
		t.Errorf("token estimate = %d, want %d", got.TokenEstimate, EstimateTokens(440))
	}
}

func TestRunnerRetriesTransientThenSucceeds(t *testing.T) {
	var got Metrics
	fr := &fakeRuntime{
		resps: []Response{{PromptBytes: 400}, okResp()},
		errs:  []error{errors.New("spawn failed"), nil},
	}
	r := &Runner{Runtime: fr, MaxRetries: 2, OnMetrics: func(m Metrics) { got = m }, now: steadyClock()}
	res, err := r.Run(context.Background(), sampleInput())
	if err != nil || !res.OK {
		t.Fatalf("expected success after retry, got %+v err=%v", res, err)
	}
	if got.Retries != 1 {
		t.Errorf("retries = %d, want 1", got.Retries)
	}
	if fr.calls != 2 {
		t.Errorf("runtime called %d times, want 2", fr.calls)
	}
}

func TestRunnerFailsAfterMaxRetries(t *testing.T) {
	var got Metrics
	fr := &fakeRuntime{resps: []Response{{PromptBytes: 5}}, errs: []error{errors.New("boom"), errors.New("boom"), errors.New("boom")}}
	r := &Runner{Runtime: fr, MaxRetries: 2, OnMetrics: func(m Metrics) { got = m }, now: steadyClock()}
	_, err := r.Run(context.Background(), sampleInput())
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if !got.Failed || got.Retries != 2 {
		t.Errorf("metrics = %+v, want Failed retries=2", got)
	}
	if fr.calls != 3 {
		t.Errorf("runtime called %d times, want 3 (1 + 2 retries)", fr.calls)
	}
}

func TestRunnerSemanticFailureNotRetried(t *testing.T) {
	// Result.OK=false with nil error is a semantic failure — Runner must NOT retry it (the engine owns
	// development retries).
	fr := &fakeRuntime{resps: []Response{{Result: assignment.WorkerResult{OK: false, Notes: "declined"}, PromptBytes: 400}}}
	r := &Runner{Runtime: fr, MaxRetries: 3, now: steadyClock()}
	res, err := r.Run(context.Background(), sampleInput())
	if err != nil {
		t.Fatalf("semantic failure must not be an error: %v", err)
	}
	if res.OK {
		t.Error("expected OK=false passthrough")
	}
	if fr.calls != 1 {
		t.Errorf("runtime called %d times, want 1 (no retry on semantic failure)", fr.calls)
	}
}

func TestRunnerTimeout(t *testing.T) {
	var got Metrics
	fr := &fakeRuntime{resps: []Response{{PromptBytes: 5}}, blockFor: time.Hour} // never returns before timeout
	r := &Runner{Runtime: fr, Timeout: 20 * time.Millisecond, OnMetrics: func(m Metrics) { got = m }}
	_, err := r.Run(context.Background(), sampleInput())
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !got.TimedOut || !got.Failed {
		t.Errorf("metrics = %+v, want TimedOut+Failed", got)
	}
}

func TestRunnerCancellation(t *testing.T) {
	var got Metrics
	fr := &fakeRuntime{resps: []Response{{PromptBytes: 5}}, blockFor: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(15 * time.Millisecond); cancel() }()
	r := &Runner{Runtime: fr, MaxRetries: 5, OnMetrics: func(m Metrics) { got = m }}
	_, err := r.Run(ctx, sampleInput())
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if !got.Cancelled {
		t.Errorf("metrics = %+v, want Cancelled", got)
	}
	// a cancelled parent context must stop retrying immediately
	if got.Retries != 0 {
		t.Errorf("retries = %d, want 0 (no retry after cancel)", got.Retries)
	}
}

// Runner must satisfy assignment.Worker so the engine consumes it unchanged.
var _ assignment.Worker = (*Runner)(nil)
