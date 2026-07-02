// Package sim provides deterministic in-memory adapters for the Orchestrator's external ports. They
// are the "keep the fakes" building blocks of SIMULATION MODE (Phase 2): the platform runs the whole
// orchestration loop through them WITHOUT touching Claude, Jira, GitHub, devices, or hardware. This is
// the regression + demonstration environment; it is not test-only code.
package sim

import (
	"context"
	"sync"

	"github.com/myotgo/ClaudWorkerV2/internal/orchestrator"
	"github.com/myotgo/ClaudWorkerV2/internal/verify"
)

// Jira is a deterministic in-memory work source.
type Jira struct {
	mu          sync.Mutex
	issues      []orchestrator.Issue
	Transitions []string
	Comments    []string
}

// NewJira returns a simulated Jira seeded with the given issues (DemoIssues if none).
func NewJira(issues ...orchestrator.Issue) *Jira {
	if len(issues) == 0 {
		issues = DemoIssues()
	}
	return &Jira{issues: issues}
}

// DemoIssues is a small, stable backlog for demos/regression.
func DemoIssues() []orchestrator.Issue {
	return []orchestrator.Issue{
		{Key: "SIM-1", Summary: "Add a hello endpoint", AcceptanceCriteria: "- GET /hello returns 200"},
		{Key: "SIM-2", Summary: "Fix flaky retry", AcceptanceCriteria: "- retry stops after cap"},
		{Key: "SIM-3", Summary: "Document the config", AcceptanceCriteria: "- README covers every field"},
	}
}

func (j *Jira) Eligible(context.Context) ([]orchestrator.Issue, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]orchestrator.Issue, len(j.issues))
	copy(out, j.issues)
	return out, nil
}

func (j *Jira) Get(_ context.Context, key string) (orchestrator.Issue, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, i := range j.issues {
		if i.Key == key {
			return i, nil
		}
	}
	return orchestrator.Issue{Key: key, Summary: "simulated"}, nil
}

func (j *Jira) Transition(_ context.Context, key, to string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Transitions = append(j.Transitions, key+"->"+to)
	return nil
}

func (j *Jira) Comment(_ context.Context, key, text string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Comments = append(j.Comments, key+": "+text)
	return nil
}

// Developer is a deterministic worker: it always "produces" a small change. It represents the Worker
// Runtime without spending tokens or calling Claude.
type Developer struct {
	mu    sync.Mutex
	Calls int
}

func (d *Developer) Develop(_ context.Context, in orchestrator.DevInput) (orchestrator.DevResult, error) {
	d.mu.Lock()
	d.Calls++
	d.mu.Unlock()
	return orchestrator.DevResult{OK: true, Summary: "simulated change for " + in.Issue, ChangedFiles: []string{"sim_change.txt"}}, nil
}

// Verifier is a deterministic verifier. By default it PASSES so simulated issues complete; an optional
// scripted sequence lets a demo show the improvement loop (fail → improve → pass).
type Verifier struct {
	mu    sync.Mutex
	seq   [][]verify.Result
	calls int
}

// NewVerifier returns a verifier that plays the given result sequence (last repeats); empty = always
// pass.
func NewVerifier(seq ...[]verify.Result) *Verifier { return &Verifier{seq: seq} }

func (v *Verifier) Verify(context.Context, string) ([]verify.Result, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.seq) == 0 {
		return []verify.Result{{Verifier: "sim", Type: verify.TypeUnit, Outcome: verify.Pass, Summary: "simulated pass"}}, nil
	}
	i := v.calls
	v.calls++
	if i >= len(v.seq) {
		i = len(v.seq) - 1
	}
	return v.seq[i], nil
}

// Merger is a deterministic merge that always succeeds.
type Merger struct{}

func (Merger) Merge(context.Context, string) (bool, error) { return true, nil }
