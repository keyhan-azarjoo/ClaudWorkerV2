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

import "fmt"

// StateVersion is the current persisted-record FORMAT version. It is metadata describing the layout
// of the stored record — NOT execution state. Bump it only when the on-disk layout changes, and add
// a branch to migrate() so old records upgrade deterministically. It exists purely for long-term
// compatibility (deterministic migration of persisted state across architecture versions).
const StateVersion = 1

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
//
// SpecVersion is the one exception to "only execution state": it is FORMAT metadata (see
// StateVersion), persisted so future engines can migrate old records deterministically. The Store
// stamps it on Save; recovery validates it via migrate().
type Assignment struct {
	IssueKey    string `json:"issue_key"`
	State       State  `json:"state"`
	Attempt     int    `json:"attempt"`
	SpecVersion int    `json:"spec_version"`
}

// migrate deterministically upgrades a just-loaded record to the current StateVersion, or returns an
// error for an unknown/newer format (a version mismatch is NEVER silently ignored). New format
// versions add a case here; the migration path stays deterministic and testable.
func migrate(a *Assignment) error {
	switch {
	case a.SpecVersion == StateVersion:
		return nil
	case a.SpecVersion == 0:
		// Pre-versioning record → format v1 (identical fields; just stamp the version).
		a.SpecVersion = 1
		return nil
	case a.SpecVersion > StateVersion:
		return fmt.Errorf("assignment %q: state format v%d is newer than this engine supports (v%d) — upgrade the engine; refusing to guess",
			a.IssueKey, a.SpecVersion, StateVersion)
	default:
		return fmt.Errorf("assignment %q: unknown state format v%d (no deterministic migration path)",
			a.IssueKey, a.SpecVersion)
	}
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
