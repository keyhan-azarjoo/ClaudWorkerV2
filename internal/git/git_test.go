package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func testIdentity() Identity {
	return Identity{Name: "keyhanazarjoo", Email: "keyhanazarjoo@gmail.com"}
}

func mustExec(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setupRepo returns a git client, a work repo on branch "development" with one commit, and a bare
// origin the work repo can push to.
func setupRepo(t *testing.T) (*Git, string, string) {
	t.Helper()
	ctx := context.Background()
	base := t.TempDir()
	origin := filepath.Join(base, "origin.git")
	work := filepath.Join(base, "work")

	mustExec(t, base, "init", "--bare", "-b", "development", origin)
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	mustExec(t, work, "init", "-b", "development")

	g := New(WithIdentity(testIdentity()))
	writeFile(t, work, "README.md", "hello\n")
	if _, err := g.Commit(ctx, work, "init", true); err != nil {
		t.Fatalf("initial commit: %v", err)
	}
	mustExec(t, work, "remote", "add", "origin", origin)
	if err := g.Push(ctx, work, "origin", "development"); err != nil {
		t.Fatalf("push: %v", err)
	}
	return g, work, origin
}

func TestCloneIsIdempotent(t *testing.T) {
	ctx := context.Background()
	g, _, origin := setupRepo(t)
	dst := filepath.Join(t.TempDir(), "clone")

	if err := g.Clone(ctx, origin, dst); err != nil {
		t.Fatalf("clone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "README.md")); err != nil {
		t.Errorf("expected README in clone: %v", err)
	}
	// second clone into same dir is a no-op success (idempotent)
	if err := g.Clone(ctx, origin, dst); err != nil {
		t.Errorf("second clone should be idempotent: %v", err)
	}
}

func TestCleanAndRevAndTags(t *testing.T) {
	ctx := context.Background()
	g, work, _ := setupRepo(t)

	clean, err := g.IsClean(ctx, work)
	if err != nil || !clean {
		t.Fatalf("expected clean tree, got clean=%v err=%v", clean, err)
	}
	rev, err := g.CurrentRevision(ctx, work)
	if err != nil || len(rev) != 40 {
		t.Fatalf("CurrentRevision = %q err=%v", rev, err)
	}
	mustExec(t, work, "tag", "v1")
	tags, err := g.Tags(ctx, work)
	if err != nil || len(tags) != 1 || tags[0] != "v1" {
		t.Fatalf("Tags = %v err=%v", tags, err)
	}

	writeFile(t, work, "new.txt", "x")
	clean, _ = g.IsClean(ctx, work)
	if clean {
		t.Error("tree should be dirty after adding a file")
	}
	changed, _ := g.ChangedFiles(ctx, work)
	if len(changed) != 1 || changed[0].Path != "new.txt" {
		t.Errorf("ChangedFiles = %v", changed)
	}
}

func TestBranchCreateDeleteIdempotent(t *testing.T) {
	ctx := context.Background()
	g, work, _ := setupRepo(t)

	if err := g.CreateBranch(ctx, work, "agent/x", "development"); err != nil {
		t.Fatalf("create: %v", err)
	}
	// idempotent create
	if err := g.CreateBranch(ctx, work, "agent/x", "development"); err != nil {
		t.Errorf("second create should be idempotent: %v", err)
	}
	if err := g.DeleteBranch(ctx, work, "agent/x", true); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// idempotent delete (already gone)
	if err := g.DeleteBranch(ctx, work, "agent/x", true); err != nil {
		t.Errorf("delete of missing branch should be idempotent: %v", err)
	}
}

func TestWorktreeAddRemoveIdempotent(t *testing.T) {
	ctx := context.Background()
	g, work, _ := setupRepo(t)
	wtPath := filepath.Join(t.TempDir(), "wt")

	wt, err := g.AddWorktree(ctx, work, wtPath, "agent/feature", "development")
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if wt.Branch != "agent/feature" {
		t.Errorf("worktree branch = %q", wt.Branch)
	}
	if _, err := os.Stat(filepath.Join(wtPath, "README.md")); err != nil {
		t.Errorf("worktree missing files: %v", err)
	}
	// idempotent add
	if _, err := g.AddWorktree(ctx, work, wtPath, "agent/feature", "development"); err != nil {
		t.Errorf("second AddWorktree should be idempotent: %v", err)
	}
	if err := g.RemoveWorktree(ctx, work, wtPath); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	// idempotent remove
	if err := g.RemoveWorktree(ctx, work, wtPath); err != nil {
		t.Errorf("second RemoveWorktree should be idempotent: %v", err)
	}
}

func TestCommitNothingToCommit(t *testing.T) {
	ctx := context.Background()
	g, work, _ := setupRepo(t)
	res, err := g.Commit(ctx, work, "noop", true)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !res.Nothing {
		t.Errorf("expected Nothing=true on clean tree, got %+v", res)
	}
}

func TestMergeNoFFAndAheadBehind(t *testing.T) {
	ctx := context.Background()
	g, work, _ := setupRepo(t)

	if err := g.CreateBranch(ctx, work, "agent/f", "development"); err != nil {
		t.Fatal(err)
	}
	mustExec(t, work, "checkout", "agent/f")
	writeFile(t, work, "f.txt", "feature\n")
	if _, err := g.Commit(ctx, work, "feat", true); err != nil {
		t.Fatal(err)
	}
	bs, err := g.AheadBehind(ctx, work, "development", "agent/f")
	if err != nil {
		t.Fatal(err)
	}
	if bs.Ahead != 1 || bs.Behind != 0 {
		t.Errorf("AheadBehind = %+v, want ahead=1 behind=0", bs)
	}

	mustExec(t, work, "checkout", "development")
	res, err := g.Merge(ctx, work, "agent/f", "merge f")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if !res.Merged || len(res.Conflicts) != 0 {
		t.Errorf("Merge = %+v, want merged no conflicts", res)
	}
}

func TestMergeConflictAbortsCleanly(t *testing.T) {
	ctx := context.Background()
	g, work, _ := setupRepo(t)

	// two branches change the same line
	if err := g.CreateBranch(ctx, work, "agent/a", "development"); err != nil {
		t.Fatal(err)
	}
	mustExec(t, work, "checkout", "agent/a")
	writeFile(t, work, "README.md", "A\n")
	if _, err := g.Commit(ctx, work, "a", true); err != nil {
		t.Fatal(err)
	}

	mustExec(t, work, "checkout", "development")
	writeFile(t, work, "README.md", "B\n")
	if _, err := g.Commit(ctx, work, "b", true); err != nil {
		t.Fatal(err)
	}

	res, err := g.Merge(ctx, work, "agent/a", "merge a")
	if err != nil {
		t.Fatalf("Merge returned error instead of structured conflict: %v", err)
	}
	if res.Merged {
		t.Fatal("expected conflict, got merged")
	}
	if len(res.Conflicts) == 0 {
		t.Error("expected conflict paths reported")
	}
	// tree must be clean again (merge aborted -> restart-safe)
	clean, _ := g.IsClean(ctx, work)
	if !clean {
		t.Error("tree must be clean after aborted merge")
	}
}

func TestFetchAndPushRoundTrip(t *testing.T) {
	ctx := context.Background()
	g, work, origin := setupRepo(t)

	writeFile(t, work, "second.txt", "2\n")
	if _, err := g.Commit(ctx, work, "second", true); err != nil {
		t.Fatal(err)
	}
	if err := g.Push(ctx, work, "origin", "development"); err != nil {
		t.Fatalf("push: %v", err)
	}
	// fresh clone sees the pushed commit
	dst := filepath.Join(t.TempDir(), "verify")
	if err := g.Clone(ctx, origin, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "second.txt")); err != nil {
		t.Errorf("pushed file not present in fresh clone: %v", err)
	}
	if err := g.Fetch(ctx, work); err != nil {
		t.Errorf("fetch: %v", err)
	}
}

func TestErrorIsStructured(t *testing.T) {
	ctx := context.Background()
	g := New(WithIdentity(testIdentity()))
	_, err := g.CurrentRevision(ctx, filepath.Join(t.TempDir(), "not-a-repo"))
	if err == nil {
		t.Fatal("expected error on non-repo")
	}
	ge, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *git.Error, got %T", err)
	}
	if ge.Op == "" || ge.ExitCode == 0 {
		t.Errorf("error not populated: %+v", ge)
	}
}

func BenchmarkCurrentRevision(b *testing.B) {
	ctx := context.Background()
	base := b.TempDir()
	work := filepath.Join(base, "w")
	_ = os.MkdirAll(work, 0o755)
	exec.Command("git", "-C", work, "init", "-b", "development").Run()
	g := New(WithIdentity(testIdentity()))
	os.WriteFile(filepath.Join(work, "f"), []byte("x"), 0o644)
	_, _ = g.Commit(ctx, work, "c", true)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := g.CurrentRevision(ctx, work); err != nil {
			b.Fatal(err)
		}
	}
}
