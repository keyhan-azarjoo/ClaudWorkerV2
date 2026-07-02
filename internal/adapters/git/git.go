// Package gitadapter is the REAL Git adapter (Phase 2.2). It replaces the simulated Git edge with the
// deterministic internal/git toolbelt, giving the orchestration loop real worktrees, branches,
// commits, merges and cleanup. It changes no other subsystem: it implements the frozen
// orchestrator.Developer, orchestrator.Merger, and (new, optional) orchestrator.Workspace ports.
//
// Safety: all per-assignment development happens in DISPOSABLE worktrees named deterministically from
// the issue key (agent/<issue> ⇢ <worktrees>/<issue>). The human/main working tree is never touched;
// the integration merge runs on the engine's own dedicated clone's development branch, and conflicts
// are auto-aborted by the git toolbelt so the tree stays clean and restart-safe.
package gitadapter

import (
	"context"
	"os"
	"path/filepath"

	git "github.com/myotgo/ClaudWorkerV2/internal/git"
	"github.com/myotgo/ClaudWorkerV2/internal/orchestrator"
)

// Adapter wraps a *git.Git bound to one engine clone + integration branch + worktree parent dir.
type Adapter struct {
	g           *git.Git
	repo        string // the engine's local clone (checked out on devBranch)
	devBranch   string
	worktreeDir string
	remote      string
}

// New builds the adapter. remote defaults to "origin".
func New(g *git.Git, repo, devBranch, worktreeDir string) *Adapter {
	return &Adapter{g: g, repo: repo, devBranch: devBranch, worktreeDir: worktreeDir, remote: "origin"}
}

// BranchFor / WorktreeFor are the deterministic, recomputable workspace locations (never persisted).
func (a *Adapter) BranchFor(issue string) string   { return "agent/" + issue }
func (a *Adapter) WorktreeFor(issue string) string { return filepath.Join(a.worktreeDir, issue) }

// EnsureWorkspace prepares an isolated worktree on the assignment branch (creating the branch off
// devBranch if needed). Idempotent — reused across improvement iterations and after a restart.
func (a *Adapter) EnsureWorkspace(ctx context.Context, issue string) (string, error) {
	_ = a.g.Fetch(ctx, a.repo) // best-effort; offline is not fatal
	wt := a.WorktreeFor(issue)
	if _, err := a.g.AddWorktree(ctx, a.repo, wt, a.BranchFor(issue), a.devBranch); err != nil {
		return "", err
	}
	return wt, nil
}

// Cleanup removes the worktree and deletes the branch. Idempotent and safe if either is already gone
// — the failure/completion cleanup path (implements orchestrator.Workspace).
func (a *Adapter) Cleanup(ctx context.Context, issue string) error {
	_ = a.g.RemoveWorktree(ctx, a.repo, a.WorktreeFor(issue))
	_ = a.g.DeleteBranch(ctx, a.repo, a.BranchFor(issue), true)
	return nil
}

// PullFF fast-forwards the integration branch from the remote (ff-only; never a stray merge commit).
func (a *Adapter) PullFF(ctx context.Context) error {
	return a.g.Pull(ctx, a.repo, a.remote, a.devBranch)
}

// Rebase rebases an assignment's branch (in its worktree) onto the integration branch. On conflict it
// is auto-aborted by the toolbelt (clean, restart-safe) and rebased=false with the conflicting paths.
func (a *Adapter) Rebase(ctx context.Context, issue string) (rebased bool, conflicts []string, err error) {
	rr, err := a.g.Rebase(ctx, a.WorktreeFor(issue), a.devBranch)
	if err != nil {
		return false, nil, err
	}
	return rr.Rebased, rr.Conflicts, nil
}

// --- Control Plane state readers (real Git state for the console) ---

// Worktrees lists the live worktrees.
func (a *Adapter) Worktrees(ctx context.Context) ([]git.Worktree, error) {
	return a.g.Worktrees(ctx, a.repo)
}

// Status is the integration clone's Git status for the console (clean? conflicts?).
type Status struct {
	Branch    string   `json:"branch"`
	Clean     bool     `json:"clean"`
	Conflicts []string `json:"conflicts"`
	Worktrees int      `json:"worktrees"`
}

// Status reports the integration clone's cleanliness, conflicts, and worktree count.
func (a *Adapter) Status(ctx context.Context) (Status, error) {
	clean, err := a.g.IsClean(ctx, a.repo)
	if err != nil {
		return Status{}, err
	}
	conflicts, _ := a.g.Conflicts(ctx, a.repo)
	wts, _ := a.g.Worktrees(ctx, a.repo)
	return Status{Branch: a.devBranch, Clean: clean, Conflicts: conflicts, Worktrees: len(wts)}, nil
}

// --- Developer: real Git workspace + commit around an inner (reasoning) worker ---

// WorktreeWorker runs the reasoning worker INSIDE a prepared worktree. It is the seam between the Git
// workspace (this package) and the Worker Runtime (Phase 2.3): the real runtime edits files in the
// given worktree; the simulated worker ignores it.
type WorktreeWorker interface {
	Develop(ctx context.Context, worktree string, in orchestrator.DevInput) (orchestrator.DevResult, error)
}

// FromDeveloper adapts a plain orchestrator.Developer (e.g. the sim worker) into a WorktreeWorker that
// ignores the worktree — used for simulation-of-Git and tests.
func FromDeveloper(d orchestrator.Developer) WorktreeWorker { return worktreeFromDeveloper{d} }

type worktreeFromDeveloper struct{ d orchestrator.Developer }

func (w worktreeFromDeveloper) Develop(ctx context.Context, _ string, in orchestrator.DevInput) (orchestrator.DevResult, error) {
	return w.d.Develop(ctx, in)
}

// Developer implements orchestrator.Developer. It prepares the worktree, runs the worker IN that
// worktree, materialises any declared changed files that the worker did not physically write, and
// commits. The Git side is fully real; the reasoning is the Worker Runtime (real Claude in live mode,
// sim otherwise).
type Developer struct {
	a      *Adapter
	worker WorktreeWorker
}

// NewDeveloper composes the Git workspace with a worktree-aware worker.
func NewDeveloper(a *Adapter, worker WorktreeWorker) *Developer {
	return &Developer{a: a, worker: worker}
}

// Develop prepares the worktree, runs the worker in it, and commits its output into the assignment
// branch.
func (d *Developer) Develop(ctx context.Context, in orchestrator.DevInput) (orchestrator.DevResult, error) {
	wt, err := d.a.EnsureWorkspace(ctx, in.Issue)
	if err != nil {
		return orchestrator.DevResult{}, err
	}
	res, err := d.worker.Develop(ctx, wt, in)
	if err != nil {
		return res, err
	}
	// Materialise declared changes that the (simulated) worker did not physically write, so the commit
	// is real. A real worker (Phase 2.3) writes into the worktree directly; existing files are kept.
	for _, f := range res.ChangedFiles {
		p := filepath.Join(wt, filepath.Clean(f))
		if _, statErr := os.Stat(p); os.IsNotExist(statErr) {
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			_ = os.WriteFile(p, []byte(res.Summary+"\n"), 0o644)
		}
	}
	if _, err := d.a.g.Commit(ctx, wt, in.Issue+": "+res.Summary, true); err != nil {
		return res, err
	}
	return res, nil
}

// --- Merger: real --no-ff merge of the assignment branch into the integration branch ---

// Merger implements orchestrator.Merger with a real Git merge. On conflict the merge is auto-aborted
// (clean tree, restart-safe) and Merge reports not-merged; the orchestrator then fails the assignment
// and the Cleaner removes the workspace.
type Merger struct{ a *Adapter }

// NewMerger builds the Git merger.
func NewMerger(a *Adapter) *Merger { return &Merger{a: a} }

// Merge fetches, merges the assignment branch --no-ff into the integration branch, and best-effort
// pushes. Returns merged=false on conflict (already aborted).
func (m *Merger) Merge(ctx context.Context, issue string) (bool, error) {
	// Fast-forward the integration branch to origin FIRST so our merge sits on top of the latest and
	// the push is a fast-forward (otherwise the remote moves ahead and the push is REJECTED, so the
	// merge never reaches GitHub). Best-effort: if it can't ff (diverged), we still merge locally.
	if err := m.a.g.Pull(ctx, m.a.repo, m.a.remote, m.a.devBranch); err != nil {
		_ = m.a.g.Fetch(ctx, m.a.repo)
	}
	branch := m.a.BranchFor(issue)
	mr, err := m.a.g.Merge(ctx, m.a.repo, branch, "Merge "+branch+" ("+issue+")")
	if err != nil {
		return false, err
	}
	if !mr.Merged {
		return false, nil // conflict — aborted, tree clean
	}
	_ = m.a.g.Push(ctx, m.a.repo, m.a.remote, m.a.devBranch) // best-effort; local merge already durable
	return true, nil
}

// Compile-time port conformance.
var (
	_ orchestrator.Developer = (*Developer)(nil)
	_ orchestrator.Merger    = (*Merger)(nil)
	_ orchestrator.Workspace = (*Adapter)(nil)
)
