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

// ---- fakes ----

// fakeWorker returns a scripted sequence of results (one per call); the last is reused after exhaust.
type fakeWorker struct {
	mu      sync.Mutex
	results []WorkerResult
	err     error
	calls   int
}

func (f *fakeWorker) Run(ctx context.Context, in WorkerInput) (WorkerResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return WorkerResult{}, f.err
	}
	i := f.calls - 1
	if i >= len(f.results) {
		i = len(f.results) - 1
	}
	return f.results[i], nil
}

// mockJira serves the endpoints the engine uses: search, transitions (GET+POST), comment (POST),
// and GET issue (for acceptance criteria).
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

// setupGit builds a work repo on "development" with one commit and a bare origin to push/merge into.
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

func newEngine(t *testing.T, g *git.Git, repo string, w Worker, maxAttempts int) (*Engine, *Store) {
	t.Helper()
	srv := mockJira(t)
	jc := jira.New(srv.URL, "me@x.com", "tok")
	store, err := NewStore(filepath.Join(t.TempDir(), "assignments"))
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{
		Owner:       "engine-test",
		RepoPath:    repo,
		DevBranch:   "development",
		WorktreeDir: filepath.Join(t.TempDir(), "wt"),
		MaxAttempts: maxAttempts,
		InProgress:  []string{"In Progress"},
		Done:        []string{"Done"},
		Jira:        jc,
		Git:         g,
		Worker:      w,
		Store:       store,
	}
	return e, store
}

func TestFullLifecycleToDone(t *testing.T) {
	ctx := context.Background()
	g, repo := setupGit(t)
	w := &fakeWorker{results: []WorkerResult{{OK: true, Summary: "add hello", Files: []File{{Path: "hello.txt", Content: "hello\n"}}}}}
	e, store := newEngine(t, g, repo, w, 3)

	a, err := e.ClaimAndRun(ctx, "project = SCRUM", 10)
	if err != nil {
		t.Fatalf("ClaimAndRun: %v", err)
	}
	if a == nil || a.State != StateDone {
		t.Fatalf("assignment = %+v, want Done", a)
	}
	if a.MergeSHA == "" {
		t.Error("expected MergeSHA set")
	}
	if w.calls != 1 {
		t.Errorf("worker called %d times, want 1", w.calls)
	}
	// the developed file must be merged into development in the main repo
	if _, err := os.Stat(filepath.Join(repo, "hello.txt")); err != nil {
		t.Errorf("merged file missing: %v", err)
	}
	// branch + worktree cleaned up
	if _, err := os.Stat(a.Worktree); !os.IsNotExist(err) {
		t.Errorf("worktree not removed: %v", err)
	}
	// persisted as done
	got, ok, _ := store.Load("SCRUM-1")
	if !ok || got.State != StateDone {
		t.Errorf("persisted state = %+v", got)
	}
}

func TestIssueLockPreventsReclaim(t *testing.T) {
	ctx := context.Background()
	g, repo := setupGit(t)
	w := &fakeWorker{results: []WorkerResult{{OK: true, Summary: "x", Files: []File{{Path: "x.txt", Content: "x"}}}}}
	e, _ := newEngine(t, g, repo, w, 3)

	if _, err := e.ClaimAndRun(ctx, "jql", 10); err != nil {
		t.Fatal(err)
	}
	// second run: SCRUM-1 already has a (Done) assignment → must be skipped, returns nil
	a, err := e.ClaimAndRun(ctx, "jql", 10)
	if err != nil {
		t.Fatal(err)
	}
	if a != nil {
		t.Errorf("expected no new assignment (issue already processed), got %+v", a)
	}
	if w.calls != 1 {
		t.Errorf("worker called %d times, want 1 (no redo)", w.calls)
	}
}

func TestRetryThenSucceed(t *testing.T) {
	ctx := context.Background()
	g, repo := setupGit(t)
	w := &fakeWorker{results: []WorkerResult{
		{OK: false, Notes: "first attempt fails"},
		{OK: true, Summary: "ok now", Files: []File{{Path: "ok.txt", Content: "ok"}}},
	}}
	e, _ := newEngine(t, g, repo, w, 3)

	a, err := e.ClaimAndRun(ctx, "jql", 10)
	if err != nil {
		t.Fatal(err)
	}
	if a.State != StateDone {
		t.Fatalf("state = %s, want Done", a.State)
	}
	if a.Attempt != 1 {
		t.Errorf("attempt = %d, want 1", a.Attempt)
	}
}

func TestFailAfterMaxAttempts(t *testing.T) {
	ctx := context.Background()
	g, repo := setupGit(t)
	w := &fakeWorker{results: []WorkerResult{{OK: false, Notes: "always fails"}}}
	e, _ := newEngine(t, g, repo, w, 2)

	a, err := e.ClaimAndRun(ctx, "jql", 10)
	if err != nil {
		t.Fatal(err)
	}
	if a.State != StateFailed {
		t.Fatalf("state = %s, want Failed", a.State)
	}
	if a.Attempt != 2 {
		t.Errorf("attempt = %d, want 2", a.Attempt)
	}
}

// TestRestartResume proves Law 19: a fresh Engine sharing the same Store resumes an unfinished
// Assignment from its last stable state and completes it, without redoing completed work.
func TestRestartResume(t *testing.T) {
	ctx := context.Background()
	g, repo := setupGit(t)
	w := &fakeWorker{results: []WorkerResult{{OK: true, Summary: "dev", Files: []File{{Path: "r.txt", Content: "r"}}}}}
	e1, store := newEngine(t, g, repo, w, 3)

	// Drive up to the QA checkpoint, then "crash" (stop using e1).
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

	// Restart: brand-new Engine, same Store + repo + worker.
	e2 := &Engine{
		Owner: "engine-restarted", RepoPath: repo, DevBranch: "development",
		WorktreeDir: e1.WorktreeDir, MaxAttempts: 3,
		InProgress: []string{"In Progress"}, Done: []string{"Done"},
		Jira: e1.Jira, Git: g, Worker: w, Store: store,
	}
	resumed, err := e2.Resume(ctx)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if len(resumed) != 1 {
		t.Fatalf("resumed %d, want 1", len(resumed))
	}
	final, _, _ := store.Load("SCRUM-1")
	if final.State != StateDone {
		t.Errorf("post-resume state = %s, want Done", final.State)
	}
	// resume must NOT re-run the worker (develop already completed before the crash)
	if w.calls != developCalls {
		t.Errorf("worker re-invoked on resume (%d→%d): completed work redone", developCalls, w.calls)
	}
	// second Resume is a no-op (terminal not redone)
	again, _ := e2.Resume(ctx)
	if len(again) != 0 {
		t.Errorf("second Resume found %d unfinished, want 0", len(again))
	}
}
