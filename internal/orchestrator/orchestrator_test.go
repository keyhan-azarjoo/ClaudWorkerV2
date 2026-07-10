package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"claudworker/internal/assignment"
	"claudworker/internal/controlplane"
	"claudworker/internal/knowledge"
	"claudworker/internal/lease"
	"claudworker/internal/policy"
	"claudworker/internal/resource"
	"claudworker/internal/verify"
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
	notOK bool // when true, the worker reports ok=false (a semantic "could not complete") with no error
	mu    sync.Mutex
}

func (f *fakeDeveloper) Develop(context.Context, DevInput) (DevResult, error) {
	f.mu.Lock()
	f.calls++
	notOK := f.notOK
	f.mu.Unlock()
	return DevResult{OK: !notOK, Summary: "did work", ChangedFiles: []string{"main.go"}}, nil
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

// TestSemanticFailureDoesNotMerge guards the SCRUM-1000 bug: when the worker reports ok=false ("could
// not complete") but the build still passes, the task must NOT be merged/marked done — it must fail.
func TestSemanticFailureDoesNotMerge(t *testing.T) {
	h := newHarness(t, []Issue{{Key: "SCRUM-X", Summary: "do a thing"}}, [][]verify.Result{pass()}, true)
	h.dev.notOK = true // worker declines (ok=false), but verification (build) passes
	if _, err := h.o.ProcessOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	a, _, _ := h.store.Load("SCRUM-X")
	if a.State == assignment.StateDone {
		t.Fatalf("ok=false task must NOT be Done; got %s", a.State)
	}
	if a.State != assignment.StateFailed {
		t.Errorf("state = %s, want failed", a.State)
	}
	// It must not have merged.
	types := eventTypes(h.cp)
	if types["MergeCompleted"] {
		t.Error("an ok=false task must not merge")
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

	h.o.SetActive(true) // resume only happens when the loop is active (idle-by-default)
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

// TestIdleByDefaultSkipsResume guards the idle-by-default contract: a freshly built loop is not
// active, and Recover must NOT resume interrupted work until it is started (manual "Start Working").
func TestIdleByDefaultSkipsResume(t *testing.T) {
	h := newHarness(t, []Issue{{Key: "SCRUM-9", Summary: "resume me"}}, [][]verify.Result{pass()}, true)
	_ = h.store.Save(&assignment.Assignment{IssueKey: "SCRUM-9", State: assignment.StateDeveloping})

	if h.o.IsActive() {
		t.Fatal("orchestrator must start idle (inactive) by default")
	}
	if err := h.o.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	resumed, _, _ := h.store.Load("SCRUM-9")
	if resumed.State == assignment.StateDone {
		t.Error("idle orchestrator must NOT resume work before being started")
	}
	if h.dev.calls != 0 {
		t.Errorf("worker ran while idle: %d calls", h.dev.calls)
	}
	// After starting, resume proceeds.
	h.o.SetActive(true)
	if !h.o.IsActive() {
		t.Fatal("SetActive(true) did not activate")
	}
	if err := h.o.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if done, _, _ := h.store.Load("SCRUM-9"); done.State != assignment.StateDone {
		t.Errorf("after start, unfinished work not resumed: %s", done.State)
	}
}

// TestTaskActivityPersistsAcrossRestart guards that the dashboard task boxes (stage timeline, account,
// token totals) survive a process restart: a run records activity to TaskLogDir, and a FRESH
// Orchestrator pointed at the same dir restores every box via Recover — plus a legacy assignment with
// no activity file still gets a box (state overlaid from the Store).
func TestTaskActivityPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, []Issue{{Key: "SCRUM-1", Summary: "Add hello"}}, [][]verify.Result{pass()}, true)
	h.o.TaskLogDir = dir
	if _, err := h.o.ProcessOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.o.SetTaskTokens("SCRUM-1", 900, 200)   // live current-run (rough)
	h.o.BankTaskTokens("SCRUM-1", 1200, 340) // accurate per-run total banked (also persists)
	h.o.recordAction("SCRUM-1", "finish", "done", "merged")

	// A fresh Orchestrator (simulating a restart) sharing the same Store + TaskLogDir.
	store := h.store
	_ = store.Save(&assignment.Assignment{IssueKey: "LEGACY-7", State: assignment.StateFailed})
	clk := steady()
	o2 := New(&Orchestrator{
		Resources: resource.New(resource.WithClock(clk)), Policy: policy.New(policy.Config{}),
		Leases:    lease.New(lease.NewMemoryStore(), lease.WithClock(clk)),
		Knowledge: knowledge.New(knowledge.NewMemoryStore(), knowledge.WithClock(clk)),
		Verify:    verify.New(), Store: store, Jira: &fakeJira{}, Developer: &fakeDeveloper{},
		Verifier: &fakeVerifier{}, Merger: &fakeMerger{merged: true},
	}, WithClock(clk))
	o2.TaskLogDir = dir
	if err := o2.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}

	in, out := o2.TaskTokens("SCRUM-1")
	if in != 1200 || out != 340 {
		t.Errorf("restored tokens = %d/%d, want 1200/340", in, out)
	}
	o2.mu.Lock()
	sc1 := o2.taskLog["SCRUM-1"]
	leg := o2.taskLog["LEGACY-7"]
	o2.mu.Unlock()
	if sc1 == nil || len(sc1.Actions) == 0 {
		t.Fatalf("SCRUM-1 box/timeline not restored: %+v", sc1)
	}
	if sc1.Account == "" {
		t.Errorf("restored box lost its account")
	}
	if leg == nil {
		t.Fatalf("legacy assignment (no activity file) got no box")
	}
	// A resumed run must ADD to the prior token total, not reset it.
	o2.SetTaskTokens("SCRUM-1", 500, 100)
	if in, out := o2.TaskTokens("SCRUM-1"); in != 1700 || out != 440 {
		t.Errorf("resumed tokens = %d/%d, want 1700/440 (banked + new)", in, out)
	}
}

// TestTaskReportPersistsLineByLine guards the drawer report: each streamed agent line is appended to
// the per-task log file and a FRESH orchestrator (restart) reads the full line-by-line transcript back
// via TaskStream — so DONE tasks keep their report.
func TestTaskReportPersistsLineByLine(t *testing.T) {
	dir := t.TempDir()
	o := New(&Orchestrator{Store: assignment.NewMemoryStore()}, WithClock(steady()))
	o.TaskLogDir = dir
	lines := []string{"▶ agent started", "🔧 Bash — git status", "🤖 sub-agent — fix the bug", "done: merged"}
	for _, l := range lines {
		o.AppendTaskLog("SCRUM-42", l)
	}
	// A fresh orchestrator (simulated restart) reads the persisted transcript.
	o2 := New(&Orchestrator{Store: assignment.NewMemoryStore()}, WithClock(steady()))
	o2.TaskLogDir = dir
	got := o2.TaskStream("SCRUM-42")
	if len(got) != len(lines) {
		t.Fatalf("restored %d lines, want %d (%v)", len(got), len(lines), got)
	}
	for i := range lines {
		if got[i] != lines[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], lines[i])
		}
	}
	// An unknown task returns an empty slice (never nil → the console gets a JSON array).
	if s := o2.TaskStream("NOPE-1"); s == nil || len(s) != 0 {
		t.Errorf("empty stream = %#v, want non-nil empty slice", s)
	}
}

// TestTokenBankAcrossRuns guards accurate token accounting: each Develop run banks its ACCURATE result
// total, and the task total is the SUM across the initial develop + every improvement iteration, while
// live (rough) mid-run updates never corrupt the banked sum.
func TestTokenBankAcrossRuns(t *testing.T) {
	o := New(&Orchestrator{Store: assignment.NewMemoryStore()}, WithClock(steady()))
	// Run 1: live rough updates climb, then the accurate total is banked.
	o.SetTaskTokens("SCRUM-7", 5000, 10)
	o.SetTaskTokens("SCRUM-7", 9000, 40)
	o.BankTaskTokens("SCRUM-7", 8200, 55) // accurate run-1 total (from result event)
	if in, out := o.TaskTokens("SCRUM-7"); in != 8200 || out != 55 {
		t.Fatalf("after run 1 = %d/%d, want 8200/55", in, out)
	}
	// Run 2 (an improvement iteration): live climbs from ~0 again, then banks.
	o.SetTaskTokens("SCRUM-7", 3000, 12)
	if in, _ := o.TaskTokens("SCRUM-7"); in != 8200+3000 {
		t.Errorf("mid run-2 in = %d, want %d (banked + live)", in, 11200)
	}
	o.BankTaskTokens("SCRUM-7", 4100, 30)
	if in, out := o.TaskTokens("SCRUM-7"); in != 12300 || out != 85 {
		t.Errorf("after run 2 = %d/%d, want 12300/85 (sum of both runs)", in, out)
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
