package improvement

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/myotgo/ClaudWorkerV2/internal/policy"
	"github.com/myotgo/ClaudWorkerV2/internal/verify"
)

func steady() func() time.Time {
	t := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	return func() time.Time { cur := t; t = t.Add(time.Second); return cur }
}

// scriptedVerifier returns a pre-set sequence of outcomes (last repeats).
type scriptedVerifier struct {
	seq   [][]verify.Result
	calls int
}

func (s *scriptedVerifier) Verify(context.Context) ([]verify.Result, error) {
	i := s.calls
	s.calls++
	if i >= len(s.seq) {
		i = len(s.seq) - 1
	}
	return s.seq[i], nil
}

func fails(detail string) []verify.Result {
	return []verify.Result{{Verifier: "v", Type: verify.TypeUnit, Outcome: verify.Fail, Detail: detail}}
}
func passes() []verify.Result {
	return []verify.Result{{Verifier: "v", Type: verify.TypeUnit, Outcome: verify.Pass}}
}

// countingImprover records how many improvements it made and reports changed files.
type countingImprover struct {
	calls int
	err   error
}

func (c *countingImprover) Improve(context.Context, ImprovementInput) (Change, error) {
	c.calls++
	if c.err != nil {
		return Change{}, c.err
	}
	return Change{Category: CatDefect, Reason: "fix", ChangedFiles: []string{"f.go"}}, nil
}

// decider is a fixed StopDecider for tests.
type decider struct{ d Decision }

func (x decider) Decide(StopInput) Decision { return x.d }

func TestPassFirstNoImprovement(t *testing.T) {
	v := &scriptedVerifier{seq: [][]verify.Result{passes()}}
	im := &countingImprover{}
	e := New(v, im, decider{Continue}, WithClock(steady()))
	res, err := e.Run(context.Background(), ImprovementInput{Assignment: "SCRUM-1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusPassed || im.calls != 0 || res.Progress.Iterations != 1 {
		t.Errorf("pass-first = %+v (improves=%d)", res, im.calls)
	}
}

func TestImproveThenPass(t *testing.T) {
	v := &scriptedVerifier{seq: [][]verify.Result{fails("boom"), passes()}}
	im := &countingImprover{}
	e := New(v, im, decider{Continue}, WithClock(steady()))
	res, err := e.Run(context.Background(), ImprovementInput{Assignment: "SCRUM-1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusPassed || im.calls != 1 {
		t.Fatalf("improve-then-pass = %+v (improves=%d)", res, im.calls)
	}
	if len(res.Progress.ChangedFiles) != 1 || res.Progress.ChangedFiles[0] != "f.go" {
		t.Errorf("changed files = %v", res.Progress.ChangedFiles)
	}
	if len(res.Progress.Records) != 2 { // fail iteration + pass iteration
		t.Errorf("records = %d, want 2", len(res.Progress.Records))
	}
}

func TestPolicyDeferStops(t *testing.T) {
	v := &scriptedVerifier{seq: [][]verify.Result{fails("x")}}
	im := &countingImprover{}
	e := New(v, im, decider{Defer}, WithClock(steady()))
	res, _ := e.Run(context.Background(), ImprovementInput{})
	if res.Status != StatusDeferred || im.calls != 0 {
		t.Errorf("defer = %+v improves=%d (must not improve after defer)", res, im.calls)
	}
}

func TestPolicyEscalateStops(t *testing.T) {
	v := &scriptedVerifier{seq: [][]verify.Result{fails("x")}}
	e := New(v, &countingImprover{}, decider{Escalate}, WithClock(steady()))
	res, _ := e.Run(context.Background(), ImprovementInput{})
	if res.Status != StatusEscalated {
		t.Errorf("escalate = %+v", res.Status)
	}
}

func TestPolicyFailStops(t *testing.T) {
	v := &scriptedVerifier{seq: [][]verify.Result{fails("x")}}
	e := New(v, &countingImprover{}, decider{Fail}, WithClock(steady()))
	res, _ := e.Run(context.Background(), ImprovementInput{})
	if res.Status != StatusFailed {
		t.Errorf("fail = %+v", res.Status)
	}
}

// TestSafetyCeilingGuaranteesTermination: even a policy that ALWAYS says Continue against a verifier
// that never passes must terminate (no infinite loop).
func TestSafetyCeilingGuaranteesTermination(t *testing.T) {
	v := &scriptedVerifier{seq: [][]verify.Result{fails("always")}}
	im := &countingImprover{}
	e := New(v, im, decider{Continue}, WithClock(steady()))
	e.MaxIterations = 5
	res, _ := e.Run(context.Background(), ImprovementInput{})
	if res.Status != StatusExhausted {
		t.Fatalf("status = %s, want exhausted", res.Status)
	}
	if res.Progress.Iterations != 5 {
		t.Errorf("iterations = %d, want 5 (ceiling)", res.Progress.Iterations)
	}
}

// TestStuckDetection: identical failures with no progress raise Stuck, which a real policy uses to
// stop. Here we assert the Stuck flag reaches the decider.
func TestStuckDetection(t *testing.T) {
	v := &scriptedVerifier{seq: [][]verify.Result{fails("same"), fails("same"), fails("same"), fails("same")}}
	var seen []StopInput
	spy := stopSpy{d: Continue, seen: &seen}
	e := New(v, &countingImprover{}, spy, WithClock(steady()))
	e.MaxIterations = 4
	e.StuckThreshold = 2
	_, _ = e.Run(context.Background(), ImprovementInput{})
	// by the 3rd failed decision the identical signature has repeated >= 2 times → Stuck
	sawStuck := false
	for _, s := range seen {
		if s.Stuck {
			sawStuck = true
		}
	}
	if !sawStuck {
		t.Errorf("Stuck was never signalled to the policy: %+v", seen)
	}
}

// TestMeasurableProgressDelta: fewer failures than last time yields a positive delta.
func TestMeasurableProgressDelta(t *testing.T) {
	twoFail := []verify.Result{
		{Verifier: "a", Outcome: verify.Fail, Detail: "1"},
		{Verifier: "b", Outcome: verify.Fail, Detail: "2"},
	}
	oneFail := []verify.Result{
		{Verifier: "a", Outcome: verify.Pass},
		{Verifier: "b", Outcome: verify.Fail, Detail: "2"},
	}
	v := &scriptedVerifier{seq: [][]verify.Result{twoFail, oneFail, passes()}}
	e := New(v, &countingImprover{}, decider{Continue}, WithClock(steady()))
	res, _ := e.Run(context.Background(), ImprovementInput{})
	if res.Status != StatusPassed {
		t.Fatalf("status = %s", res.Status)
	}
	// iteration 2 reduced failures 2→1 → delta 1
	if res.Progress.Records[1].Delta != 1 {
		t.Errorf("delta = %d, want 1", res.Progress.Records[1].Delta)
	}
}

func TestImproveErrorRecordedNotHidden(t *testing.T) {
	v := &scriptedVerifier{seq: [][]verify.Result{fails("x"), passes()}}
	im := &countingImprover{err: errors.New("worker boom")}
	e := New(v, im, decider{Continue}, WithClock(steady()))
	res, _ := e.Run(context.Background(), ImprovementInput{})
	// first iteration failed to improve (recorded), second verify passes
	if res.Status != StatusPassed {
		t.Fatalf("status = %s", res.Status)
	}
	if res.Progress.Records[0].Reason == "" || res.Progress.Records[0].Reason[:14] != "improve failed" {
		t.Errorf("improve error not recorded: %q", res.Progress.Records[0].Reason)
	}
}

// TestReceivesOnlyFourInputs asserts the Improver is handed exactly the four permitted fields.
func TestReceivesOnlyFourInputs(t *testing.T) {
	v := &scriptedVerifier{seq: [][]verify.Result{fails("x"), passes()}}
	var got ImprovementInput
	im := improverFunc(func(_ context.Context, in ImprovementInput) (Change, error) {
		got = in
		return Change{Reason: "r"}, nil
	})
	e := New(v, im, decider{Continue}, WithClock(steady()))
	base := ImprovementInput{Assignment: "SCRUM-1", KnowledgeContext: "K", RelevantFiles: []File{{Path: "f", Content: "c"}}}
	_, _ = e.Run(context.Background(), base)
	if got.Assignment != "SCRUM-1" || got.KnowledgeContext != "K" || len(got.RelevantFiles) != 1 {
		t.Errorf("improver input missing permitted fields: %+v", got)
	}
	if len(got.VerificationResults) == 0 {
		t.Error("improver must receive the verification results")
	}
}

// TestPolicyAdapterOwnsStopDecision wires the REAL Policy Engine: with retry cap 2, a never-passing
// verifier must stop via the policy (not the engine), reaching a terminal state within the cap.
func TestPolicyAdapterOwnsStopDecision(t *testing.T) {
	pe := policy.New(policy.Config{Retry: policy.RetryConfig{MaxAttempts: 2}, Escalation: policy.EscalationConfig{MaxAttempts: 2}})
	v := &scriptedVerifier{seq: [][]verify.Result{fails("x")}}
	e := New(v, &countingImprover{}, NewPolicyDecider(pe), WithClock(steady()))
	e.MaxIterations = 50 // ceiling is NOT what stops us — the policy is
	res, _ := e.Run(context.Background(), ImprovementInput{})
	if res.Status == StatusExhausted {
		t.Fatal("policy should have stopped the loop before the safety ceiling")
	}
	if res.Status != StatusEscalated && res.Status != StatusFailed {
		t.Errorf("policy terminal = %s, want escalated/failed", res.Status)
	}
	if res.Progress.Iterations > 3 {
		t.Errorf("iterations = %d, expected the policy to stop near the retry cap", res.Progress.Iterations)
	}
}

type improverFunc func(context.Context, ImprovementInput) (Change, error)

func (f improverFunc) Improve(ctx context.Context, in ImprovementInput) (Change, error) {
	return f(ctx, in)
}

type stopSpy struct {
	d    Decision
	seen *[]StopInput
}

func (s stopSpy) Decide(in StopInput) Decision {
	*s.seen = append(*s.seen, in)
	return s.d
}
