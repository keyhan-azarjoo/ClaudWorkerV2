package assignment

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/myotgo/ClaudWorkerV2/internal/git"
	"github.com/myotgo/ClaudWorkerV2/internal/jira"
)

// fakeWorker returns a scripted sequence of results (one per call); the last repeats after exhaust.
type fakeWorker struct {
	mu      sync.Mutex
	results []WorkerResult
	calls   int
}

func (f *fakeWorker) Run(ctx context.Context, in WorkerInput) (WorkerResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	i := f.calls - 1
	if i >= len(f.results) {
		i = len(f.results) - 1
	}
	return f.results[i], nil
}

func mockJira(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/3/search", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jira.SearchResult{
			Total:  1,
			Issues: []jira.Issue{{Key: "SCRUM-1", Fields: jira.IssueFields{Summary: "Add hello file"}}},
		})
	})
	mux.HandleFunc("/rest/api/3/issue/SCRUM-1/transitions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(204)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"transitions": []map[string]any{
			{"id": "11", "name": "Start", "to": map[string]string{"name": "In Progress"}},
			{"id": "31", "name": "Finish", "to": map[string]string{"name": "Done"}},
		}})
	})
	mux.HandleFunc("/rest/api/3/issue/SCRUM-1/comment", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":"c1"}`)
	})
	mux.HandleFunc("/rest/api/3/issue/SCRUM-1", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"key": "SCRUM-1",
			"fields": map[string]any{
				"summary":     "Add hello file",
				"description": "Acceptance Criteria\n- hello.txt exists",
			},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func setupGit(t *testing.T) (*git.Git, string) {
	t.Helper()
	ctx := context.Background()
	base := t.TempDir()
	origin := filepath.Join(base, "origin.git")
	work := filepath.Join(base, "work")
	run := func(dir string, args ...string) {
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(base, "init", "--bare", "-b", "development", origin)
	_ = os.MkdirAll(work, 0o755)
	run(work, "init", "-b", "development")
	g := git.New(git.WithIdentity(git.Identity{Name: "keyhanazarjoo", Email: "keyhanazarjoo@gmail.com"}))
	_ = os.WriteFile(filepath.Join(work, "README.md"), []byte("hi\n"), 0o644)
	if _, err := g.Commit(ctx, work, "init", true); err != nil {
		t.Fatal(err)
	}
	run(work, "remote", "add", "origin", origin)
	if err := g.Push(ctx, work, "origin", "development"); err != nil {
		t.Fatal(err)
	}
	return g, work
}

func newEngine(t *testing.T, g *git.Git, repo string, w Worker, store Store, maxAttempts int) *Engine {
	t.Helper()
	srv := mockJira(t)
	return &Engine{
		RepoPath:    repo,
		DevBranch:   "development",
		WorktreeDir: filepath.Join(t.TempDir(), "wt"),
		MaxAttempts: maxAttempts,
		InProgress:  []string{"In Progress"},
		Done:        []string{"Done"},
		Jira:        jira.New(srv.URL, "me@x.com", "tok"),
		Git:         g,
		Worker:      w,
		Store:       store,
	}
}

func TestFullLifecycleToDone(t *testing.T) {
	ctx := context.Background()
	g, repo := setupGit(t)
	w := &fakeWorker{results: []WorkerResult{{OK: true, Summary: "add hello", Files: []File{{Path: "hello.txt", Content: "hello\n"}}}}}
	store := NewMemoryStore()
	e := newEngine(t, g, repo, w, store, 3)

	a, err := e.ClaimAndRun(ctx, "project = SCRUM", 10)
	if err != nil {
		t.Fatalf("ClaimAndRun: %v", err)
	}
	if a == nil || a.State != StateDone {
		t.Fatalf("assignment = %+v, want Done", a)
	}
	if w.calls != 1 {
		t.Errorf("worker called %d times, want 1", w.calls)
	}
	// developed file merged into development
	if _, err := os.Stat(filepath.Join(repo, "hello.txt")); err != nil {
		t.Errorf("merged file missing: %v", err)
	}
	// branch + worktree cleaned up (recomputed paths)
	if _, err := os.Stat(e.worktreeFor("SCRUM-1")); !os.IsNotExist(err) {
		t.Errorf("worktree not removed")
	}
	// persisted state is minimal + Done
	got, ok, _ := store.Load("SCRUM-1")
	if !ok || got.State != StateDone {
		t.Errorf("persisted = %+v", got)
	}
}

func TestIssueLockPreventsReclaim(t *testing.T) {
	ctx := context.Background()
	g, repo := setupGit(t)
	w := &fakeWorker{results: []WorkerResult{{OK: true, Summary: "x", Files: []File{{Path: "x.txt", Content: "x"}}}}}
	e := newEngine(t, g, repo, w, NewMemoryStore(), 3)

	if _, err := e.ClaimAndRun(ctx, "jql", 10); err != nil {
		t.Fatal(err)
	}
	a, err := e.ClaimAndRun(ctx, "jql", 10) // SCRUM-1 now terminal → skipped
	if err != nil {
		t.Fatal(err)
	}
	if a != nil {
		t.Errorf("expected no new assignment, got %+v", a)
	}
	if w.calls != 1 {
		t.Errorf("worker called %d times, want 1 (no redo)", w.calls)
	}
}

func TestRetryThenSucceed(t *testing.T) {
	ctx := context.Background()
	g, repo := setupGit(t)
	w := &fakeWorker{results: []WorkerResult{
		{OK: false, Notes: "first fails"},
		{OK: true, Summary: "ok", Files: []File{{Path: "ok.txt", Content: "ok"}}},
	}}
	e := newEngine(t, g, repo, w, NewMemoryStore(), 3)
	a, err := e.ClaimAndRun(ctx, "jql", 10)
	if err != nil {
		t.Fatal(err)
	}
	if a.State != StateDone || a.Attempt != 1 {
		t.Errorf("state=%s attempt=%d, want Done/1", a.State, a.Attempt)
	}
}

func TestFailAfterMaxAttempts(t *testing.T) {
	ctx := context.Background()
	g, repo := setupGit(t)
	w := &fakeWorker{results: []WorkerResult{{OK: false, Notes: "always fails"}}}
	e := newEngine(t, g, repo, w, NewMemoryStore(), 2)
	a, err := e.ClaimAndRun(ctx, "jql", 10)
	if err != nil {
		t.Fatal(err)
	}
	if a.State != StateFailed || a.Attempt != 2 {
		t.Errorf("state=%s attempt=%d, want Failed/2", a.State, a.Attempt)
	}
}

// TestResumeRejectsNewerFormat proves recovery does not silently ignore a version mismatch: a record
// written by a newer state format aborts Resume with an error.
func TestResumeRejectsNewerFormat(t *testing.T) {
	ctx := context.Background()
	g, repo := setupGit(t)
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SCRUM-9.json"),
		[]byte(`{"issue_key":"SCRUM-9","state":"developing","attempt":0,"spec_version":999}`), 0o644); err != nil {
		t.Fatal(err)
	}
	store, _ := NewFileStore(dir)
	e := newEngine(t, g, repo, &fakeWorker{results: []WorkerResult{{OK: true}}}, store, 3)
	if _, err := e.Resume(ctx); err == nil {
		t.Error("Resume must fail on a newer state format, not silently ignore it")
	}
}

// TestRestartResumeFromDisk proves Law 19 against real durable storage: a brand-new FileStore over
// the same directory (a genuine reload from disk) resumes an unfinished Assignment to Done without
// re-running completed work — using ONLY the persisted {issue,state,attempt} plus recomputed paths.
func TestRestartResumeFromDisk(t *testing.T) {
	ctx := context.Background()
	g, repo := setupGit(t)
	w := &fakeWorker{results: []WorkerResult{{OK: true, Summary: "dev", Files: []File{{Path: "r.txt", Content: "r"}}}}}

	dir := filepath.Join(t.TempDir(), "state")
	store1, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	e1 := newEngine(t, g, repo, w, store1, 3)

	// Drive to the QA checkpoint, then "crash".
	res, _ := e1.Jira.Search(ctx, "jql", nil, 10)
	a, err := e1.claim(ctx, res.Issues[0])
	if err != nil {
		t.Fatal(err)
	}
	a.State = StateDeveloping
	if err := e1.develop(ctx, a); err != nil {
		t.Fatal(err)
	}
	if a.State != StateQA {
		t.Fatalf("pre-restart state = %s, want QA", a.State)
	}
	developCalls := w.calls

	// Restart: NEW FileStore over the SAME dir → reload purely from disk.
	store2, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	e2 := newEngine(t, g, repo, w, store2, 3)
	e2.WorktreeDir = e1.WorktreeDir // same worktree location (recomputed, not stored)

	resumed, err := e2.Resume(ctx)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if len(resumed) != 1 {
		t.Fatalf("resumed %d, want 1", len(resumed))
	}
	final, _, _ := store2.Load("SCRUM-1")
	if final.State != StateDone {
		t.Errorf("post-resume state = %s, want Done", final.State)
	}
	if w.calls != developCalls {
		t.Errorf("worker re-invoked on resume (%d→%d): completed work redone", developCalls, w.calls)
	}
	again, _ := e2.Resume(ctx)
	if len(again) != 0 {
		t.Errorf("second Resume found %d unfinished, want 0", len(again))
	}
}
