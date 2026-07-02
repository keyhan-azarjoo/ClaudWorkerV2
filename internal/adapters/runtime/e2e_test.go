package runtimeadapter

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	gitadapter "github.com/myotgo/ClaudWorkerV2/internal/adapters/git"
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

// TestFailoverThroughResourceManager: a rate-limit cools the account VIA the Resource Manager; the
// next reservation fails over to a healthy account. The runtime signals health; the Resource Manager
// selects — the runtime never chooses the account.
func TestFailoverThroughResourceManager(t *testing.T) {
	res := resource.New()
	res.Register(resource.Resource{ID: "acct-a", Kind: resource.KindClaudeAccount, Health: resource.HealthHealthy})
	res.Register(resource.Resource{ID: "acct-b", Kind: resource.KindClaudeAccount, Health: resource.HealthHealthy})

	bin := writeFakeClaude(t, `cat >/dev/null; echo "rate limit 429" >&2; exit 1`)
	w := New(bin, map[string]Account{"acct-a": {ID: "acct-a"}, "acct-b": {ID: "acct-b"}})
	w.Cooldown = func(account string, d time.Duration) { res.Cooldown(account, time.Now().Add(d)) }

	// Resource Manager selects acct-a first.
	r1, ok := res.Reserve("owner", resource.Filter{Kind: resource.KindClaudeAccount})
	if !ok || r1.ID != "acct-a" {
		t.Fatalf("first reserve = %v", r1)
	}
	// Worker hits a rate limit under acct-a → cools it via the Resource Manager.
	if _, err := w.Develop(context.Background(), t.TempDir(), devInput("acct-a")); err == nil {
		t.Fatal("expected rate-limit error")
	}
	res.Release("acct-a")
	// Next reservation fails over to acct-b (acct-a is cooling).
	r2, ok := res.Reserve("owner", resource.Filter{Kind: resource.KindClaudeAccount})
	if !ok || r2.ID != "acct-b" {
		t.Fatalf("failover reserve = %v, want acct-b", r2)
	}
}

// TestAccountExhaustion: with every account cooled, the Resource Manager offers none — the loop must
// not run the worker (the orchestrator defers).
func TestAccountExhaustion(t *testing.T) {
	res := resource.New()
	res.Register(resource.Resource{ID: "acct-a", Kind: resource.KindClaudeAccount, Health: resource.HealthHealthy})
	res.Cooldown("acct-a", time.Now().Add(time.Hour))
	if _, ok := res.Reserve("owner", resource.Filter{Kind: resource.KindClaudeAccount}); ok {
		t.Fatal("exhausted accounts should offer no reservation")
	}
}

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

// TestProductionFlowRealClaude is the Phase-2.3 production validation: the whole loop with REAL Jira
// (sim), REAL Git, and the REAL Claude runtime (fake CLI binary — zero tokens) editing the worktree,
// through verification/improvement, a real merge, and Jira Done.
func TestProductionFlowRealClaude(t *testing.T) {
	g, repo, base := setupRepo(t)
	gitA := gitadapter.New(g, repo, "development", filepath.Join(base, "wt"))
	ctx := context.Background()

	// Real runtime backed by a fake CLI that edits a file in the worktree.
	inner := `{\"ok\":true,\"summary\":\"implemented\"}`
	bin := writeFakeClaude(t, `cat >/dev/null; echo "hello" > feature.txt; printf '%s' '{"result":"`+inner+`"}'`)
	worker := New(bin, map[string]Account{"claude-1": {ID: "claude-1"}})

	res := resource.New()
	res.Register(resource.Resource{ID: "claude-1", Kind: resource.KindClaudeAccount, Health: resource.HealthHealthy})
	store := assignment.NewMemoryStore()
	jiraS := sim.NewJira(orchestrator.Issue{Key: "SCRUM-1", Summary: "add feature", AcceptanceCriteria: "- feature.txt"})

	o := orchestrator.New(&orchestrator.Orchestrator{
		Resources: res,
		Policy:    policy.New(policy.Config{}),
		Leases:    lease.New(lease.NewMemoryStore()),
		Knowledge: knowledge.New(knowledge.NewMemoryStore()),
		Verify:    verify.New(),
		Store:     store,
		CP:        controlplane.NewServer(controlplane.NewBus()),
		Jira:      jiraS,
		Developer: gitadapter.NewDeveloper(gitA, worker), // REAL Git + REAL Claude runtime
		Verifier:  sim.NewVerifier(),
		Merger:    gitadapter.NewMerger(gitA),
		Cleaner:   gitA,
		Cfg:       orchestrator.Config{DevBranch: "development"},
	})

	did, err := o.ProcessOnce(ctx)
	if err != nil || !did {
		t.Fatalf("ProcessOnce did=%v err=%v", did, err)
	}
	got, _, _ := store.Load("SCRUM-1")
	if got.State != assignment.StateDone {
		t.Fatalf("assignment = %s, want done", got.State)
	}
	// The Claude-edited file was committed and really merged onto development.
	if _, err := os.Stat(filepath.Join(repo, "feature.txt")); err != nil {
		t.Errorf("real Claude change did not reach development: %v", err)
	}
	// Worker metrics captured.
	if s := worker.Snapshot(); len(s.Recent) == 0 || s.Recent[len(s.Recent)-1].Class != ClassSuccess {
		t.Errorf("worker metrics = %+v", s)
	}
}
