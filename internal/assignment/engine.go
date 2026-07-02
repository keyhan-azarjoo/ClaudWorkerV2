package assignment

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/myotgo/ClaudWorkerV2/internal/git"
	"github.com/myotgo/ClaudWorkerV2/internal/jira"
)

// Engine drives one Assignment through its lifecycle. It is 100% deterministic Go (Law 18): it
// coordinates the git/jira toolbelt and spawns a disposable AI worker via the Worker port, but
// contains no AI reasoning itself. Dependencies are concrete (git, jira) — the only interface is
// Worker (two real implementations). No speculative abstraction.
type Engine struct {
	Owner       string   // run/engine id recorded as the claim owner
	RepoPath    string   // local clone; must be checked out on DevBranch
	DevBranch   string   // integration branch (e.g. "development")
	WorktreeDir string   // parent dir for per-Assignment worktrees
	MaxAttempts int      // retry cap (config defaults.retry_limits.max_attempts)
	InProgress  []string // Jira status names meaning "in progress"
	Done        []string // Jira status names meaning "done"

	Jira   *jira.Client
	Git    *git.Git
	Worker Worker
	Store  *Store
	Log    *slog.Logger
}

func (e *Engine) log() *slog.Logger {
	if e.Log != nil {
		return e.Log
	}
	return slog.New(discardHandler{})
}

// ClaimAndRun searches the work queue, claims the first eligible unclaimed issue, and drives it to a
// terminal state. Returns (nil, nil) when there is nothing eligible to do (an idle engine is a
// success, P7). It never claims an issue that already has an active Assignment (issue lock).
func (e *Engine) ClaimAndRun(ctx context.Context, workJQL string, maxResults int) (*Assignment, error) {
	res, err := e.Jira.Search(ctx, workJQL, nil, maxResults)
	if err != nil {
		return nil, fmt.Errorf("claim: search: %w", err)
	}
	for _, iss := range res.Issues {
		// Skip any issue that already has an Assignment — active (issue lock, I-1) or terminal
		// (never redo completed work, Law 19). One active Assignment per issue is the lock.
		if prev, exists, err := e.Store.Load(iss.Key); err != nil {
			return nil, err
		} else if exists {
			e.log().Info("assignment", "op", "skip", "issue", iss.Key, "reason", "already has assignment", "state", string(prev.State))
			continue
		}
		a, err := e.claim(ctx, iss)
		if err != nil {
			return nil, err
		}
		if err := e.drive(ctx, a); err != nil {
			return a, err
		}
		return a, nil
	}
	return nil, nil
}

// Resume re-drives every unfinished Assignment after a restart. Completed work is never redone
// (Law 19): terminal Assignments are ignored; each other resumes at its last stable state.
func (e *Engine) Resume(ctx context.Context) ([]*Assignment, error) {
	unfinished, err := e.Store.Unfinished()
	if err != nil {
		return nil, err
	}
	for _, a := range unfinished {
		e.log().Info("assignment", "op", "resume", "issue", a.IssueKey, "state", string(a.State), "attempt", a.Attempt)
		if err := e.drive(ctx, a); err != nil {
			return unfinished, err
		}
	}
	return unfinished, nil
}

// claim creates the Assignment (the issue lock), transitions Jira to in-progress, and prepares the
// branch + worktree. The Assignment is persisted before any external mutation so a crash mid-claim is
// recoverable.
func (e *Engine) claim(ctx context.Context, iss jira.Issue) (*Assignment, error) {
	a := &Assignment{
		ID:        iss.Key,
		IssueKey:  iss.Key,
		Owner:     e.Owner,
		State:     StateClaimed,
		Summary:   iss.Fields.Summary,
		Branch:    "agent/" + iss.Key + "-" + slug(iss.Fields.Summary),
		CreatedAt: nowUTC(),
	}
	a.Worktree = filepath.Join(e.WorktreeDir, iss.Key)
	if err := e.Store.Save(a); err != nil {
		return nil, err
	}
	// Jira: make ownership visible.
	if len(e.InProgress) > 0 {
		if _, err := e.Jira.TransitionTo(ctx, iss.Key, e.InProgress...); err != nil {
			e.log().Warn("assignment", "op", "claim.transition", "issue", iss.Key, "error", err.Error())
		}
	}
	_, _ = e.Jira.AddComment(ctx, iss.Key, "ClaudWorker claimed this issue on branch "+a.Branch)

	// Git: fresh branch + worktree off the newest DevBranch.
	if err := e.Git.Fetch(ctx, e.RepoPath); err != nil {
		return a, e.fail(a, "fetch: "+err.Error())
	}
	if err := e.Git.CreateBranch(ctx, e.RepoPath, a.Branch, e.DevBranch); err != nil {
		return a, e.fail(a, "branch: "+err.Error())
	}
	if _, err := e.Git.AddWorktree(ctx, e.RepoPath, a.Worktree, a.Branch, e.DevBranch); err != nil {
		return a, e.fail(a, "worktree: "+err.Error())
	}
	a.Progress = "claimed; worktree ready"
	return a, e.Store.Save(a)
}

// drive is the deterministic state machine. Each step advances one state and persists, so the last
// stable state is always on disk for resume. On a recoverable failure it retries from Developing up
// to MaxAttempts; then it fails.
func (e *Engine) drive(ctx context.Context, a *Assignment) error {
	for !a.State.Terminal() {
		var err error
		switch a.State {
		case StateClaimed:
			a.State = StateDeveloping
			err = e.Store.Save(a)
		case StateDeveloping:
			err = e.develop(ctx, a)
		case StateQA:
			err = e.handOffToQA(ctx, a)
		case StateMerging:
			err = e.handOffToMerge(ctx, a)
		default:
			return fmt.Errorf("drive: unknown state %q", a.State)
		}
		if err != nil {
			return err
		}
	}
	e.log().Info("assignment", "op", "done", "issue", a.IssueKey, "state", string(a.State), "attempt", a.Attempt)
	return nil
}

// develop spawns the one disposable AI worker, applies its proposed files, and commits. Re-running is
// safe (the worker is disposable; an identical commit is a no-op).
func (e *Engine) develop(ctx context.Context, a *Assignment) error {
	ac, err := e.Jira.AcceptanceCriteria(ctx, a.IssueKey)
	if err != nil {
		return e.retryOrFail(a, "acceptance criteria: "+err.Error())
	}
	in := WorkerInput{
		IssueKey:           a.IssueKey,
		Summary:            a.Summary,
		AcceptanceCriteria: ac,
		// RelevantFiles + KnowledgeContext arrive with the Knowledge Brain (S4); empty for now.
	}
	res, err := e.Worker.Run(ctx, in)
	if err != nil {
		return e.retryOrFail(a, "worker: "+err.Error())
	}
	if !res.OK {
		return e.retryOrFail(a, "worker reported failure: "+res.Notes)
	}
	for _, f := range res.Files {
		dst := filepath.Join(a.Worktree, f.Path)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return e.retryOrFail(a, "write dir: "+err.Error())
		}
		if err := os.WriteFile(dst, []byte(f.Content), 0o644); err != nil {
			return e.retryOrFail(a, "write file: "+err.Error())
		}
	}
	if _, err := e.Git.Commit(ctx, a.Worktree, commitMessage(a, res), true); err != nil {
		return e.retryOrFail(a, "commit: "+err.Error())
	}
	a.Progress = "developed: " + res.Summary
	a.State = StateQA
	return e.Store.Save(a)
}

// handOffToQA hands the branch to QA. The QA subsystem is S8; the S2 skeleton implements the handover
// boundary as a deterministic PASS so the vertical slice completes end-to-end.
func (e *Engine) handOffToQA(ctx context.Context, a *Assignment) error {
	a.Progress = "QA handoff: PASS (skeleton — QA subsystem is S8)"
	a.State = StateMerging
	return e.Store.Save(a)
}

// handOffToMerge performs the deterministic Merge handover: refresh, --no-ff merge into DevBranch,
// then delete the branch + worktree. A conflict is reported and retried (bounded), never forced.
func (e *Engine) handOffToMerge(ctx context.Context, a *Assignment) error {
	if err := e.Git.Fetch(ctx, e.RepoPath); err != nil {
		return e.retryOrFail(a, "merge fetch: "+err.Error())
	}
	mr, err := e.Git.Merge(ctx, e.RepoPath, a.Branch, "Merge "+a.Branch+" ("+a.IssueKey+")")
	if err != nil {
		return e.retryOrFail(a, "merge: "+err.Error())
	}
	if !mr.Merged {
		return e.retryOrFail(a, fmt.Sprintf("merge conflict: %v", mr.Conflicts))
	}
	a.MergeSHA = mr.SHA
	_ = e.Git.RemoveWorktree(ctx, e.RepoPath, a.Worktree)
	_ = e.Git.DeleteBranch(ctx, e.RepoPath, a.Branch, true)

	if len(e.Done) > 0 {
		if _, err := e.Jira.TransitionTo(ctx, a.IssueKey, e.Done...); err != nil {
			e.log().Warn("assignment", "op", "close.transition", "issue", a.IssueKey, "error", err.Error())
		}
	}
	_, _ = e.Jira.AddComment(ctx, a.IssueKey, "Merged to "+e.DevBranch+" at "+a.MergeSHA)
	a.Progress = "merged " + a.MergeSHA
	a.State = StateDone
	return e.Store.Save(a)
}

// retryOrFail increments the attempt and either retries from Developing or fails the Assignment.
func (e *Engine) retryOrFail(a *Assignment, reason string) error {
	a.Attempt++
	if a.Attempt < e.MaxAttempts {
		e.log().Warn("assignment", "op", "retry", "issue", a.IssueKey, "attempt", a.Attempt, "reason", reason)
		a.Progress = "retry " + fmt.Sprintf("%d", a.Attempt) + ": " + reason
		a.State = StateDeveloping
		return e.Store.Save(a)
	}
	return e.fail(a, reason)
}

// fail marks the Assignment failed (attempts exhausted / unrecoverable). NEEDS_HUMAN labeling and
// notification are wired with the full workflow later; here we record the terminal failure.
func (e *Engine) fail(a *Assignment, reason string) error {
	e.log().Error("assignment", "op", "fail", "issue", a.IssueKey, "attempt", a.Attempt, "reason", reason)
	a.State = StateFailed
	a.Progress = "failed: " + reason
	return e.Store.Save(a)
}

func commitMessage(a *Assignment, res WorkerResult) string {
	m := res.Summary
	if m == "" {
		m = "work for " + a.IssueKey
	}
	return a.IssueKey + ": " + m
}

// slug makes a short, filesystem/branch-safe suffix from an issue summary.
func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n >= 24 {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			n++
		case r == ' ' || r == '-' || r == '_':
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
				b.WriteByte('-')
				n++
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
