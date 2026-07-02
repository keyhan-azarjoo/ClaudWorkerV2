package gitadapter

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/myotgo/ClaudWorkerV2/internal/adapters/sim"
	"github.com/myotgo/ClaudWorkerV2/internal/assignment"
	"github.com/myotgo/ClaudWorkerV2/internal/controlplane"
	git "github.com/myotgo/ClaudWorkerV2/internal/git"
	"github.com/myotgo/ClaudWorkerV2/internal/knowledge"
	"github.com/myotgo/ClaudWorkerV2/internal/lease"
	"github.com/myotgo/ClaudWorkerV2/internal/orchestrator"
	"github.com/myotgo/ClaudWorkerV2/internal/policy"
	"github.com/myotgo/ClaudWorkerV2/internal/resource"
	"github.com/myotgo/ClaudWorkerV2/internal/verify"
)

// setupRepo builds a real git repo: a bare origin + a working clone on `development` with an initial
// committed file, so tests exercise real Git end to end.
func setupRepo(t *testing.T) (*git.Git, string, string) {
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
	_ = os.WriteFile(filepath.Join(work, "base.txt"), []byte("base\n"), 0o644)
	if _, err := g.Commit(ctx, work, "init", true); err != nil {
		t.Fatal(err)
	}
	run(work, "remote", "add", "origin", origin)
	if err := g.Push(ctx, work, "origin", "development"); err != nil {
		t.Fatal(err)
	}
	return g, work, base
}

func newAdapter(t *testing.T) (*Adapter, *git.Git, string) {
	g, repo, base := setupRepo(t)
	return New(g, repo, "development", filepath.Join(base, "wt")), g, repo
}

func TestWorkspaceLifecycle(t *testing.T) {
	a, g, repo := newAdapter(t)
	ctx := context.Background()
	wt, err := a.EnsureWorkspace(ctx, "SCRUM-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("worktree missing: %v", err)
	}
	wts, _ := g.Worktrees(ctx, repo)
	if !hasWorktree(wts, "agent/SCRUM-1") {
		t.Fatalf("branch worktree not registered: %+v", wts)
	}
	// idempotent
	if _, err := a.EnsureWorkspace(ctx, "SCRUM-1"); err != nil {
		t.Fatalf("EnsureWorkspace not idempotent: %v", err)
	}
	// cleanup removes worktree + branch
	if err := a.Cleanup(ctx, "SCRUM-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree not removed")
	}
	wts, _ = g.Worktrees(ctx, repo)
	if hasWorktree(wts, "agent/SCRUM-1") {
		t.Errorf("worktree still registered after cleanup")
	}
}

func TestDeveloperCommitsRealChange(t *testing.T) {
	a, _, _ := newAdapter(t)
	dev := NewDeveloper(a, &sim.Developer{})
	res, err := dev.Develop(context.Background(), orchestrator.DevInput{Issue: "SCRUM-1", Summary: "add file"})
	if err != nil || !res.OK {
		t.Fatalf("develop = %+v err=%v", res, err)
	}
	// the change is physically present + committed in the worktree
	wt := a.WorktreeFor("SCRUM-1")
	if _, err := os.Stat(filepath.Join(wt, "sim_change.txt")); err != nil {
		t.Errorf("developer did not write the file: %v", err)
	}
	clean, _ := a.g.IsClean(context.Background(), wt)
	if !clean {
		t.Errorf("worktree not clean after commit (change not committed)")
	}
}

func TestMergeSuccess(t *testing.T) {
	a, g, repo := newAdapter(t)
	ctx := context.Background()
	dev := NewDeveloper(a, &sim.Developer{})
	if _, err := dev.Develop(ctx, orchestrator.DevInput{Issue: "SCRUM-1", Summary: "add file"}); err != nil {
		t.Fatal(err)
	}
	merged, err := NewMerger(a).Merge(ctx, "SCRUM-1")
	if err != nil || !merged {
		t.Fatalf("merge = %v err=%v", merged, err)
	}
	// the file is now on development in the integration clone
	if _, err := os.Stat(filepath.Join(repo, "sim_change.txt")); err != nil {
		t.Errorf("merged file missing on development: %v", err)
	}
	clean, _ := g.IsClean(ctx, repo)
	if !clean {
		t.Errorf("integration clone not clean after merge")
	}
}

func TestMergeConflictAbortsCleanly(t *testing.T) {
	a, g, repo := newAdapter(t)
	ctx := context.Background()
	// branch changes base.txt
	wt, _ := a.EnsureWorkspace(ctx, "SCRUM-1")
	_ = os.WriteFile(filepath.Join(wt, "base.txt"), []byte("branch change\n"), 0o644)
	if _, err := g.Commit(ctx, wt, "branch edit", true); err != nil {
		t.Fatal(err)
	}
	// development changes the SAME file differently → conflict
	_ = os.WriteFile(filepath.Join(repo, "base.txt"), []byte("dev change\n"), 0o644)
	if _, err := g.Commit(ctx, repo, "dev edit", true); err != nil {
		t.Fatal(err)
	}
	merged, err := NewMerger(a).Merge(ctx, "SCRUM-1")
	if err != nil {
		t.Fatal(err)
	}
	if merged {
		t.Fatal("conflicting merge reported success")
	}
	// the merge was aborted → integration clone stays clean and usable
	clean, _ := g.IsClean(ctx, repo)
	if !clean {
		t.Errorf("clone left dirty after conflict (not aborted)")
	}
}

func TestRebaseConflictAbortsCleanly(t *testing.T) {
	a, g, repo := newAdapter(t)
	ctx := context.Background()
	wt, _ := a.EnsureWorkspace(ctx, "SCRUM-1")
	_ = os.WriteFile(filepath.Join(wt, "base.txt"), []byte("branch change\n"), 0o644)
	if _, err := g.Commit(ctx, wt, "branch edit", true); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(repo, "base.txt"), []byte("dev change\n"), 0o644)
	if _, err := g.Commit(ctx, repo, "dev edit", true); err != nil {
		t.Fatal(err)
	}
	rebased, conflicts, err := a.Rebase(ctx, "SCRUM-1")
	if err != nil {
		t.Fatal(err)
	}
	if rebased || len(conflicts) == 0 {
		t.Fatalf("expected rebase conflict, got rebased=%v conflicts=%v", rebased, conflicts)
	}
	clean, _ := g.IsClean(ctx, wt)
	if !clean {
		t.Errorf("worktree left dirty after rebase conflict (not aborted)")
	}
}

func TestPullFF(t *testing.T) {
	a, _, _ := newAdapter(t)
	// PullFF against the origin created in setup (no divergence) must succeed and be ff-only.
	if err := a.PullFF(context.Background()); err != nil {
		t.Fatalf("pull ff-only: %v", err)
	}
}

func TestCleanupAfterFailureAndRestart(t *testing.T) {
	a, g, repo := newAdapter(t)
	ctx := context.Background()
	if _, err := a.EnsureWorkspace(ctx, "SCRUM-1"); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash: a BRAND-NEW adapter over the same clone (restart) sees the orphaned worktree
	// and can clean it — restart-safe, no human step.
	a2 := New(g, repo, "development", a.worktreeDir)
	wts, _ := a2.Worktrees(ctx)
	if !hasWorktree(wts, "agent/SCRUM-1") {
		t.Fatalf("restart did not see the orphaned worktree: %+v", wts)
	}
	if err := a2.Cleanup(ctx, "SCRUM-1"); err != nil {
		t.Fatal(err)
	}
	wts, _ = a2.Worktrees(ctx)
	if hasWorktree(wts, "agent/SCRUM-1") {
		t.Errorf("orphan worktree not cleaned after restart")
	}
	// double cleanup is safe
	if err := a2.Cleanup(ctx, "SCRUM-1"); err != nil {
		t.Errorf("cleanup not idempotent: %v", err)
	}
}

func TestStatusReadModel(t *testing.T) {
	a, _, _ := newAdapter(t)
	ctx := context.Background()
	_, _ = a.EnsureWorkspace(ctx, "SCRUM-1")
	st, err := a.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Branch != "development" || st.Worktrees < 2 || len(st.Conflicts) != 0 {
		t.Errorf("status = %+v", st)
	}
}

// TestProductionFlowRealGit is the Phase-2.2 production validation: the whole loop with REAL Git —
// Jira → Assignment → real Git workspace + commit → (sim) worker → verify → improve → real --no-ff
// merge → Jira Done → worktree/branch cleanup.
func TestProductionFlowRealGit(t *testing.T) {
	g, repo, base := setupRepo(t)
	a := New(g, repo, "development", filepath.Join(base, "wt"))
	ctx := context.Background()

	res := resource.New()
	res.Register(resource.Resource{ID: "claude-1", Kind: resource.KindClaudeAccount, Health: resource.HealthHealthy})
	store := assignment.NewMemoryStore()
	jiraS := sim.NewJira(orchestrator.Issue{Key: "SCRUM-1", Summary: "add hello", AcceptanceCriteria: "- hello"})

	o := orchestrator.New(&orchestrator.Orchestrator{
		Resources: res,
		Policy:    policy.New(policy.Config{}),
		Leases:    lease.New(lease.NewMemoryStore()),
		Knowledge: knowledge.New(knowledge.NewMemoryStore()),
		Verify:    verify.New(),
		Store:     store,
		CP:        controlplane.NewServer(controlplane.NewBus()),
		Jira:      jiraS,
		Developer: NewDeveloper(a, &sim.Developer{}), // REAL Git workspace + commit
		Verifier:  sim.NewVerifier(),
		Merger:    NewMerger(a), // REAL --no-ff merge
		Cleaner:   a,            // REAL cleanup
		Cfg:       orchestrator.Config{DevBranch: "development"},
	})

	did, err := o.ProcessOnce(ctx)
	if err != nil || !did {
		t.Fatalf("ProcessOnce did=%v err=%v", did, err)
	}
	// Assignment Done
	got, _, _ := store.Load("SCRUM-1")
	if got.State != assignment.StateDone {
		t.Fatalf("assignment = %s, want done", got.State)
	}
	// Real merge: the worker's file is on development in the integration clone
	if _, err := os.Stat(filepath.Join(repo, "sim_change.txt")); err != nil {
		t.Errorf("real merge did not land the change on development: %v", err)
	}
	// Jira Done recorded
	foundDone := false
	for _, tr := range jiraS.Transitions {
		if tr == "SCRUM-1->Done" {
			foundDone = true
		}
	}
	if !foundDone {
		t.Errorf("Jira not transitioned to Done: %v", jiraS.Transitions)
	}
	// Cleanup: worktree + branch removed
	wts, _ := g.Worktrees(ctx, repo)
	if hasWorktree(wts, "agent/SCRUM-1") {
		t.Errorf("worktree not cleaned after completion: %+v", wts)
	}
}

func hasWorktree(wts []git.Worktree, branch string) bool {
	for _, w := range wts {
		if w.Branch == branch || w.Branch == "refs/heads/"+branch {
			return true
		}
	}
	return false
}
