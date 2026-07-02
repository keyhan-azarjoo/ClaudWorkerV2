// Package assignment is the Assignment Engine (docs/16_WorkerStateMachine.md, docs/21 S2/S3).
//
// The Assignment is the deterministic execution unit that carries ONE Jira issue through its
// lifecycle. It owns lifecycle and retries; workers own nothing and are disposable (Law 4). The
// engine contains NO AI reasoning (Law 18); AI appears only behind the Worker port.
//
// S3 — State Store: the engine persists ONLY the minimum information required to recover execution
// after an interruption. Anything recomputable from Git, Jira, or the Knowledge Brain is NOT
// persisted (see docs/reports/PERSISTENCE_REVIEW_S3.md). The engine talks only to the Store
// interface and never knows the storage implementation.
package assignment

// State is a point in the Assignment lifecycle. The skeleton uses the subset needed to complete one
// issue end-to-end (Blocked/Cancelled arrive with the full Decision Engine, S6).
type State string

const (
	StateClaimed    State = "claimed"    // issue claimed
	StateDeveloping State = "developing" // spawn one disposable AI worker → apply result → commit
	StateQA         State = "qa"         // handed over to QA
	StateMerging    State = "merging"    // handed over to Merge (deterministic --no-ff)
	StateDone       State = "done"       // merged, Jira closed
	StateFailed     State = "failed"     // attempts exhausted / unrecoverable
)

// Terminal reports whether no further work will happen for this Assignment.
func (s State) Terminal() bool { return s == StateDone || s == StateFailed }

// Assignment is the PERSISTED state — the whole record. Every field here is information that would
// otherwise be LOST on interruption and cannot be regenerated:
//
//   - IssueKey: which Jira issue this in-flight execution belongs to. Jira knows the issue exists,
//     but not that THIS engine is mid-execution on it (a human could also move an issue to
//     In Progress). The identity of an in-flight execution is not recomputable.
//   - State:    the lifecycle checkpoint. Neither Git nor Jira records "we are at QA vs Merging";
//     without it, resume cannot know where to continue.
//   - Attempt:  the retry count. Not present in Git or Jira; required to enforce the bounded-retry
//     cap ACROSS a restart (otherwise a crash-loop could retry forever).
//
// Everything else the engine needs at runtime (branch, worktree, summary, acceptance criteria) is
// recomputed deterministically from IssueKey + config, or re-fetched from Git/Jira — so it is NOT
// stored. See PERSISTENCE_REVIEW_S3.md for the field-by-field justification.
type Assignment struct {
	IssueKey string `json:"issue_key"`
	State    State  `json:"state"`
	Attempt  int    `json:"attempt"`
}

// Store is the ONLY view the engine has of persistence. It is deliberately storage-agnostic: the
// engine never knows whether the backing is JSON files, SQLite, BoltDB, Postgres, or memory. The
// architecture explicitly requires this inversion (docs/21 S3), and there are two real
// implementations (FileStore, MemoryStore).
type Store interface {
	// Save persists the Assignment's recoverable state (idempotent, durable/atomic).
	Save(a *Assignment) error
	// Load returns the Assignment for issueKey, or ok=false if none is stored.
	Load(issueKey string) (*Assignment, bool, error)
	// List returns all stored Assignments (order unspecified).
	List() ([]*Assignment, error)
}

// unfinished filters a list to non-terminal Assignments (used by Resume). Kept as a free function so
// it is not part of the Store contract — it is trivially derivable from List (Law 17).
func unfinished(all []*Assignment) []*Assignment {
	var out []*Assignment
	for _, a := range all {
		if !a.State.Terminal() {
			out = append(out, a)
		}
	}
	return out
}
