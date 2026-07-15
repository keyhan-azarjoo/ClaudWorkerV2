package orchestrator

import (
	"testing"

	"claudworker/internal/assignment"
)

// TestWorktreeReapable — a leftover worktree is reapable only when its issue is terminal or unknown, and
// never when the work is still resumable (so the janitor can't destroy in-progress work).
func TestWorktreeReapable(t *testing.T) {
	store := assignment.NewMemoryStore()
	_ = store.Save(&assignment.Assignment{IssueKey: "DONE-1", State: assignment.StateDone})
	_ = store.Save(&assignment.Assignment{IssueKey: "FAIL-1", State: assignment.StateFailed})
	_ = store.Save(&assignment.Assignment{IssueKey: "DEV-1", State: assignment.StateDeveloping})
	o := New(&Orchestrator{Store: store}, WithClock(steady()))

	cases := map[string]bool{
		"DONE-1":  true,  // terminal → cleanup leftover
		"FAIL-1":  true,  // terminal → cleanup leftover
		"DEV-1":   false, // still resumable → keep
		"UNKNOWN": true,  // no assignment owns it → stray leftover
	}
	for issue, want := range cases {
		if got := o.WorktreeReapable(issue); got != want {
			t.Errorf("WorktreeReapable(%q)=%v, want %v", issue, got, want)
		}
	}
}
