package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/myotgo/ClaudWorkerV2/internal/assignment"
	"github.com/myotgo/ClaudWorkerV2/internal/controlplane"
	"github.com/myotgo/ClaudWorkerV2/internal/knowledge"
	"github.com/myotgo/ClaudWorkerV2/internal/lease"
	"github.com/myotgo/ClaudWorkerV2/internal/policy"
	"github.com/myotgo/ClaudWorkerV2/internal/resource"
	"github.com/myotgo/ClaudWorkerV2/internal/verify"
)

func steady() func() time.Time {
	t := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		cur := t
		t = t.Add(time.Second)
		return cur
	}
}

// --- deterministic fakes for the external edges ---

type fakeJira struct {
	issues     []Issue
	transition []string
	comments   int
	mu         sync.Mutex
}

func (f *fakeJira) Eligible(context.Context) ([]Issue, error) { return f.issues, nil }
func (f *fakeJira) Get(_ context.Context, key string) (Issue, error) {
	for _, i := range f.issues {
		if i.Key == key {
			return i, nil
		}
	}
	return Issue{Key: key, Summary: "recovered"}, nil
}
func (f *fakeJira) Transition(_ context.Context, key, to string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.transition = append(f.transition, key+"->"+to)
	return nil
}
func (f *fakeJira) Comment(context.Context, string, string) error {
	f.mu.Lock()
	f.comments++
	f.mu.Unlock()
	return nil
}

type fakeDeveloper struct {
	calls int
	mu    sync.Mutex
}

func (f *fakeDeveloper) Develop(context.Context, DevInput) (DevResult, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return DevResult{OK: true, Summary: "did work", ChangedFiles: []string{"main.go"}}, nil
}

type fakeVerifier struct {
	seq   [][]verify.Result
	calls int
	mu    sync.Mutex
}

func (f *fakeVerifier) Verify(context.Context, string) ([]verify.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.calls
	f.calls++
	if i >= len(f.seq) {
		i = len(f.seq) - 1
	}
	return f.seq[i], nil
}

type fakeMerger struct{ merged bool }

func (f *fakeMerger) Merge(context.Context, string) (bool, error) { return f.merged, nil }

func pass() []verify.Result { return []verify.Result{{Verifier: "v", Outcome: verify.Pass}} }
func fail() []verify.Result {
	return []verify.Result{{Verifier: "v", Outcome: verify.Fail, Detail: "boom"}}
}

// harness builds an Orchestrator with REAL subsystems and injected fakes.
type harness struct {
	o     *Orchestrator
	jira  *fakeJira
	dev   *fakeDeveloper
	res   *resource.Manager
	lm    *lease.Manager
	cp    *controlplane.Server
	store assignment.Store
}

func newHarness(t *testing.T, issues []Issue, verSeq [][]verify.Result, healthyAccount bool) *harness {
	t.Helper()
	clk := steady()
	res := resource.New(resource.WithClock(clk))
	h := resource.HealthHealthy
	if !healthyAccount {
		h = resource.HealthDown
	}
	res.Register(resource.Resource{ID: "acct-a", Kind: resource.KindClaudeAccount, Name: "acct-a", Health: h})

	pol := policy.New(policy.Config{})
	lm := lease.New(lease.NewMemoryStore(), lease.WithClock(clk))
	kb := knowledge.New(knowledge.NewMemoryStore(), knowledge.WithClock(clk))
	_, _ = kb.Create("git-branch", "rule", "Branch discipline", "branch off development", knowledge.SourceHuman, knowledge.StatusActive)
	cp := controlplane.NewServer(controlplane.NewBus(controlplane.WithClock(clk)))
	store := assignment.NewMemoryStore()

	jira := &fakeJira{issues: issues}
	dev := &fakeDeveloper{}
	o := New(&Orchestrator{
		Resources: res, Policy: pol, Leases: lm, Knowledge: kb, Verify: verify.New(),
		Store: store, CP: cp, Jira: jira, Developer: dev,
		Verifier: &fakeVerifier{seq: verSeq}, Merger: &fakeMerger{merged: true},
	}, WithClock(clk))
	o.RegisterControlPlane()
	return &harness{o: o, jira: jira, dev: dev, res: res, lm: lm, cp: cp, store: store}
}

func TestAutonomousClaimToCompletion(t *testing.T) {
	h := newHarness(t, []Issue{{Key: "SCRUM-1", Summary: "Add hello file", AcceptanceCriteria: "- hello.txt exists"}}, [][]verify.Result{pass()}, true)

	did, err := h.o.ProcessOnce(context.Background())
	if err != nil || !did {
		t.Fatalf("ProcessOnce did=%v err=%v", did, err)
	}
	// Assignment reached Done.
	a, ok, _ := h.store.Load("SCRUM-1")
	if !ok || a.State != assignment.StateDone {
		t.Fatalf("assignment = %+v, want Done", a)
	}
	// Worker ran; Jira moved to In Progress then Done.
	if h.dev.calls < 1 {
		t.Errorf("developer not called")
	}
	if len(h.jira.transition) < 2 {
		t.Errorf("jira transitions = %v", h.jira.transition)
	}
	// Resource released; no lingering leases.
	if av, _ := h.res.AvailabilityOf("acct-a"); av != resource.Available {
		t.Errorf("resource not released: %v", av)
	}
	act, _ := h.lm.Active()
	if len(act) != 0 {
		t.Errorf("leases not released: %+v", act)
	}
	// Events published (Timeline).
	types := eventTypes(h.cp)
	for _, want := range []string{controlplane.EventAssignmentCreated, controlplane.EventLeaseGranted, controlplane.EventRuntimeStarted, controlplane.EventVerificationFinished, controlplane.EventAssignmentCompleted} {
		if !types[want] {
			t.Errorf("missing event %q (have %v)", want, keys(types))
		}
	}
}

func TestImprovementLoopRunsUntilPass(t *testing.T) {
	// verification fails once then passes → the improvement loop must run the developer again.
	h := newHarness(t, []Issue{{Key: "SCRUM-2", Summary: "fix bug"}}, [][]verify.Result{fail(), pass()}, true)
	did, err := h.o.ProcessOnce(context.Background())
	if err != nil || !did {
		t.Fatal(err)
	}
	a, _, _ := h.store.Load("SCRUM-2")
	if a.State != assignment.StateDone {
		t.Fatalf("state = %s, want Done", a.State)
	}
	if h.dev.calls < 2 {
		t.Errorf("developer calls = %d, want >=2 (initial + improvement)", h.dev.calls)
	}
}

func TestPolicyResourceLeaseGating(t *testing.T) {
	// No healthy runtime resource → the pipeline defers BEFORE running the worker (Resource gate),
	// and never acquires a resource lease (order preserved).
	h := newHarness(t, []Issue{{Key: "SCRUM-3", Summary: "x"}}, [][]verify.Result{pass()}, false)
	_, err := h.o.ProcessOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a, _, _ := h.store.Load("SCRUM-3")
	if a.State == assignment.StateDone {
		t.Fatal("must not complete without a runtime resource")
	}
	if h.dev.calls != 0 {
		t.Errorf("developer ran despite no resource: %d", h.dev.calls)
	}
	// only the issue lease may exist; no resource lease was taken.
	act, _ := h.lm.Active()
	for _, l := range act {
		if l.Kind == lease.KindResource {
			t.Errorf("resource lease taken without an available resource: %+v", l)
		}
	}
}

func TestRecoverySkipsTerminalResumesUnfinished(t *testing.T) {
	h := newHarness(t, []Issue{{Key: "SCRUM-9", Summary: "resume me"}}, [][]verify.Result{pass()}, true)
	// pre-seed: one completed (must NOT restart) + one unfinished (must resume).
	_ = h.store.Save(&assignment.Assignment{IssueKey: "DONE-1", State: assignment.StateDone})
	_ = h.store.Save(&assignment.Assignment{IssueKey: "SCRUM-9", State: assignment.StateDeveloping})

	if err := h.o.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	done, _, _ := h.store.Load("DONE-1")
	if done.State != assignment.StateDone {
		t.Errorf("completed assignment was disturbed: %s", done.State)
	}
	resumed, _, _ := h.store.Load("SCRUM-9")
	if resumed.State != assignment.StateDone {
		t.Errorf("unfinished assignment not resumed to Done: %s", resumed.State)
	}
	if h.dev.calls < 1 {
		t.Error("resume did not run the worker")
	}
}

func TestControlPlaneGoesLive(t *testing.T) {
	h := newHarness(t, []Issue{{Key: "SCRUM-1", Summary: "Add hello"}}, [][]verify.Result{pass()}, true)
	_, _ = h.o.ProcessOnce(context.Background())

	ts := httptest.NewServer(h.cp.Handler())
	defer ts.Close()

	// assignments.list returns the completed assignment through the API.
	var env struct {
		Data []assignment.Assignment `json:"data"`
	}
	getJSON(t, ts.URL+"/v1/query/assignments.list", &env)
	if len(env.Data) != 1 || env.Data[0].IssueKey != "SCRUM-1" {
		t.Errorf("assignments.list via API = %+v", env.Data)
	}
	// status reports the orchestrator.
	var senv struct {
		Data map[string]any `json:"data"`
	}
	getJSON(t, ts.URL+"/v1/status", &senv)
	if _, ok := senv.Data["orchestrator"]; !ok {
		t.Errorf("status missing orchestrator: %+v", senv.Data)
	}
	// metrics report counters.
	getJSON(t, ts.URL+"/v1/metrics", &senv)
	if _, ok := senv.Data["counters"]; !ok {
		t.Errorf("metrics missing counters: %+v", senv.Data)
	}
	// orchestrator.tick command is registered.
	resp, _ := http.Post(ts.URL+"/v1/command/orchestrator.tick", "application/json", nil)
	if resp.StatusCode != 200 {
		t.Errorf("tick command status = %d", resp.StatusCode)
	}
}

// helpers

func eventTypes(cp *controlplane.Server) map[string]bool {
	out := map[string]bool{}
	for _, ev := range cp.Bus().Recent(0) {
		out[ev.Type] = true
	}
	return out
}
func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
func getJSON(t *testing.T, url string, out any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
}
