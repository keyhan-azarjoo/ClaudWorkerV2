// Package git is the deterministic Git toolbelt (docs/07_Git.md).
//
// Every future worker performs Git operations THROUGH this package — a worker never shells out to
// git itself (Law 5/6/18: deterministic work is Go; AI never executes deterministic workflows).
// Every method returns a typed, structured result and a machine-readable error; nothing here returns
// text intended for AI parsing. Operations are deterministic, idempotent where meaningful, and
// restart-safe (a re-run converges to the same state). All operations emit structured logs
// (op, duration, result, error, affected resources) and never log secrets.
package git

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// Identity is the commit author/committer. It is owner-mandated: always keyhanazarjoo (C-2).
type Identity struct {
	Name  string
	Email string
}

// Git is a deterministic git client. It has no hidden global state; construct one per use.
type Git struct {
	bin string
	id  Identity
	log *slog.Logger
}

// Option configures a Git.
type Option func(*Git)

// WithLogger sets the structured logger (default: discard).
func WithLogger(l *slog.Logger) Option { return func(g *Git) { g.log = l } }

// WithIdentity sets the commit identity (required for Commit/Merge).
func WithIdentity(id Identity) Option { return func(g *Git) { g.id = id } }

// New builds a Git client.
func New(opts ...Option) *Git {
	g := &Git{bin: "git", log: slog.New(slog.DiscardHandler)}
	for _, o := range opts {
		o(g)
	}
	return g
}

// Error is a machine-readable git failure.
type Error struct {
	Op       string   `json:"op"`
	Args     []string `json:"args"`
	ExitCode int      `json:"exit_code"`
	Stderr   string   `json:"stderr"`
	Err      string   `json:"err"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("git %s: exit %d: %s", e.Op, e.ExitCode, firstLine(e.Stderr))
}

// run executes git in dir with identity injected, returning trimmed stdout. It logs one structured
// line (never secrets — git args here never contain tokens; remotes use the credential helper).
func (g *Git) run(ctx context.Context, op, dir string, args ...string) (string, error) {
	full := make([]string, 0, len(args)+6)
	if dir != "" {
		full = append(full, "-C", dir)
	}
	// Force identity so every commit/merge is authored by keyhanazarjoo (C-2), independent of
	// any repo/global git config.
	if g.id.Name != "" {
		full = append(full, "-c", "user.name="+g.id.Name, "-c", "user.email="+g.id.Email)
	}
	full = append(full, args...)

	start := time.Now()
	cmd := exec.CommandContext(ctx, g.bin, full...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	dur := time.Since(start)

	out := strings.TrimRight(stdout.String(), "\n")
	if runErr != nil {
		exit := -1
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			exit = ee.ExitCode()
		}
		gerr := &Error{Op: op, Args: args, ExitCode: exit, Stderr: strings.TrimSpace(stderr.String()), Err: runErr.Error()}
		g.log.Error("git", "op", op, "dir", dir, "duration_ms", dur.Milliseconds(),
			"result", "error", "exit", exit, "error", firstLine(gerr.Stderr))
		return out, gerr
	}
	g.log.Info("git", "op", op, "dir", dir, "duration_ms", dur.Milliseconds(), "result", "ok")
	return out, nil
}

// ---- Repository-level operations ----

// Clone clones url into dir. Idempotent: if dir already contains a git repo, it is a no-op success.
func (g *Git) Clone(ctx context.Context, url, dir string) error {
	if g.isRepo(ctx, dir) {
		g.log.Info("git", "op", "clone", "result", "ok", "note", "already cloned", "dir", dir)
		return nil
	}
	_, err := g.run(ctx, "clone", "", "clone", url, dir)
	return err
}

// Fetch updates remote refs (with prune). Restart-safe and idempotent.
func (g *Git) Fetch(ctx context.Context, repo string) error {
	_, err := g.run(ctx, "fetch", repo, "fetch", "--prune", "--tags")
	return err
}

// ---- Inspection ----

// CurrentRevision returns the full SHA of HEAD in repo.
func (g *Git) CurrentRevision(ctx context.Context, repo string) (string, error) {
	return g.run(ctx, "rev-parse", repo, "rev-parse", "HEAD")
}

// IsClean reports whether the working tree has no uncommitted changes.
func (g *Git) IsClean(ctx context.Context, repo string) (bool, error) {
	out, err := g.run(ctx, "status", repo, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "", nil
}

// FileChange is one entry from `git status --porcelain`.
type FileChange struct {
	Status string `json:"status"` // two-char porcelain code, e.g. "M ", "??", "A "
	Path   string `json:"path"`
}

// ChangedFiles returns uncommitted changes in repo's working tree.
func (g *Git) ChangedFiles(ctx context.Context, repo string) ([]FileChange, error) {
	out, err := g.run(ctx, "status", repo, "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	return parsePorcelain(out), nil
}

// Diff returns the name-status changes between two refs (structured, not raw patch text).
func (g *Git) Diff(ctx context.Context, repo, from, to string) ([]FileChange, error) {
	out, err := g.run(ctx, "diff", repo, "diff", "--name-status", from, to)
	if err != nil {
		return nil, err
	}
	var changes []FileChange
	for _, line := range splitLines(out) {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 {
			changes = append(changes, FileChange{Status: parts[0], Path: parts[1]})
		}
	}
	return changes, nil
}

// BranchStatus reports how far branch is ahead/behind base.
type BranchStatus struct {
	Branch string `json:"branch"`
	Base   string `json:"base"`
	Ahead  int    `json:"ahead"`
	Behind int    `json:"behind"`
}

// AheadBehind computes ahead/behind counts of branch relative to base.
func (g *Git) AheadBehind(ctx context.Context, repo, base, branch string) (BranchStatus, error) {
	out, err := g.run(ctx, "rev-list", repo, "rev-list", "--left-right", "--count", base+"..."+branch)
	if err != nil {
		return BranchStatus{}, err
	}
	fields := strings.Fields(out)
	bs := BranchStatus{Branch: branch, Base: base}
	if len(fields) == 2 {
		bs.Behind = atoi(fields[0]) // left side = base-only commits
		bs.Ahead = atoi(fields[1])  // right side = branch-only commits
	}
	return bs, nil
}

// Conflicts returns the set of unmerged (conflicted) paths in repo, if any.
func (g *Git) Conflicts(ctx context.Context, repo string) ([]string, error) {
	out, err := g.run(ctx, "diff", repo, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

// Tags lists tags in repo.
func (g *Git) Tags(ctx context.Context, repo string) ([]string, error) {
	out, err := g.run(ctx, "tag", repo, "tag", "--list")
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

// ---- Branches ----

// CreateBranch creates name from base. Idempotent: if it already points at base, no error.
func (g *Git) CreateBranch(ctx context.Context, repo, name, base string) error {
	if g.branchExists(ctx, repo, name) {
		g.log.Info("git", "op", "branch", "result", "ok", "note", "already exists", "branch", name)
		return nil
	}
	_, err := g.run(ctx, "branch", repo, "branch", name, base)
	return err
}

// DeleteBranch deletes name. Idempotent: deleting a missing branch is a success (restart-safe).
func (g *Git) DeleteBranch(ctx context.Context, repo, name string, force bool) error {
	if !g.branchExists(ctx, repo, name) {
		return nil
	}
	flag := "-d"
	if force {
		flag = "-D"
	}
	_, err := g.run(ctx, "branch", repo, "branch", flag, name)
	return err
}

// ---- Worktrees (the anti-conflict foundation, docs/07_Git.md) ----

// Worktree describes one worktree.
type Worktree struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Head   string `json:"head"`
}

// AddWorktree creates a worktree at path on branch, creating branch from base if it does not exist.
// Idempotent: if path already exists as a worktree, the existing one is returned (restart-safe).
func (g *Git) AddWorktree(ctx context.Context, repo, path, branch, base string) (Worktree, error) {
	existing, _ := g.Worktrees(ctx, repo)
	for _, w := range existing {
		if sameWorktreePath(w.Path, path) {
			g.log.Info("git", "op", "worktree-add", "result", "ok", "note", "already exists", "path", path)
			return w, nil
		}
	}
	var err error
	if g.branchExists(ctx, repo, branch) {
		_, err = g.run(ctx, "worktree-add", repo, "worktree", "add", path, branch)
	} else {
		_, err = g.run(ctx, "worktree-add", repo, "worktree", "add", "-b", branch, path, base)
	}
	if err != nil {
		return Worktree{}, err
	}
	head, _ := g.CurrentRevision(ctx, path)
	return Worktree{Path: path, Branch: branch, Head: head}, nil
}

// RemoveWorktree removes the worktree at path. Idempotent: removing a missing worktree succeeds.
func (g *Git) RemoveWorktree(ctx context.Context, repo, path string) error {
	existing, _ := g.Worktrees(ctx, repo)
	found := false
	for _, w := range existing {
		if sameWorktreePath(w.Path, path) {
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	_, err := g.run(ctx, "worktree-remove", repo, "worktree", "remove", "--force", path)
	return err
}

// Worktrees lists all worktrees of repo.
func (g *Git) Worktrees(ctx context.Context, repo string) ([]Worktree, error) {
	out, err := g.run(ctx, "worktree-list", repo, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return parseWorktrees(out), nil
}

// ---- Mutations ----

// CommitResult is the outcome of a commit.
type CommitResult struct {
	SHA     string `json:"sha"`
	Branch  string `json:"branch"`
	Nothing bool   `json:"nothing_to_commit"`
}

// Commit stages all changes (if addAll) and commits with msg, authored by the configured identity.
// If there is nothing to commit it returns Nothing=true rather than an error (idempotent/restart-safe).
func (g *Git) Commit(ctx context.Context, dir, msg string, addAll bool) (CommitResult, error) {
	if g.id.Name == "" || g.id.Email == "" {
		return CommitResult{}, fmt.Errorf("git commit: identity required (C-2)")
	}
	if addAll {
		if _, err := g.run(ctx, "add", dir, "add", "-A"); err != nil {
			return CommitResult{}, err
		}
	}
	clean, err := g.IsClean(ctx, dir)
	if err != nil {
		return CommitResult{}, err
	}
	if clean {
		return CommitResult{Nothing: true}, nil
	}
	if _, err := g.run(ctx, "commit", dir, "commit", "-m", msg); err != nil {
		return CommitResult{}, err
	}
	sha, err := g.CurrentRevision(ctx, dir)
	if err != nil {
		return CommitResult{}, err
	}
	branch, _ := g.run(ctx, "branch-show", dir, "rev-parse", "--abbrev-ref", "HEAD")
	return CommitResult{SHA: sha, Branch: branch}, nil
}

// MergeResult reports a merge outcome. On conflict the merge is aborted so the tree stays clean
// (restart-safe); the caller/Decision Engine decides what to do (docs/20_DecisionEngine.md).
type MergeResult struct {
	Merged    bool     `json:"merged"`
	SHA       string   `json:"sha"`
	Conflicts []string `json:"conflicts"`
}

// Merge merges branch into repo's current HEAD using --no-ff (docs/07_Git.md G-2). On conflict it
// aborts and returns the conflicted paths.
func (g *Git) Merge(ctx context.Context, repo, branch, msg string) (MergeResult, error) {
	if g.id.Name == "" || g.id.Email == "" {
		return MergeResult{}, fmt.Errorf("git merge: identity required (C-2)")
	}
	_, err := g.run(ctx, "merge", repo, "merge", "--no-ff", "-m", msg, branch)
	if err != nil {
		conflicts, _ := g.Conflicts(ctx, repo)
		if len(conflicts) > 0 {
			_, _ = g.run(ctx, "merge-abort", repo, "merge", "--abort")
			return MergeResult{Merged: false, Conflicts: conflicts}, nil
		}
		return MergeResult{}, err
	}
	sha, _ := g.CurrentRevision(ctx, repo)
	return MergeResult{Merged: true, SHA: sha}, nil
}

// RebaseResult reports a rebase outcome; conflicts abort the rebase (restart-safe).
type RebaseResult struct {
	Rebased   bool     `json:"rebased"`
	Conflicts []string `json:"conflicts"`
}

// Rebase rebases the current branch of dir onto onto. On conflict it aborts and reports paths.
func (g *Git) Rebase(ctx context.Context, dir, onto string) (RebaseResult, error) {
	_, err := g.run(ctx, "rebase", dir, "rebase", onto)
	if err != nil {
		conflicts, _ := g.Conflicts(ctx, dir)
		_, _ = g.run(ctx, "rebase-abort", dir, "rebase", "--abort")
		return RebaseResult{Rebased: false, Conflicts: conflicts}, nil
	}
	return RebaseResult{Rebased: true}, nil
}

// Push pushes branch to remote. Never force (docs/07_Git.md G-5). Auth is via the git credential
// helper / gh — this method never embeds a token, so logs stay secret-free.
func (g *Git) Push(ctx context.Context, repo, remote, branch string) error {
	_, err := g.run(ctx, "push", repo, "push", remote, branch)
	return err
}

// ---- helpers ----

func (g *Git) isRepo(ctx context.Context, dir string) bool {
	_, err := g.run(ctx, "rev-parse", dir, "rev-parse", "--is-inside-work-tree")
	return err == nil
}

func (g *Git) branchExists(ctx context.Context, repo, name string) bool {
	_, err := g.run(ctx, "show-ref", repo, "show-ref", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil
}
