// Package assignment is the Assignment Engine (docs/16_WorkerStateMachine.md, docs/21 S2).
//
// The Assignment is the deterministic execution unit that carries ONE Jira issue through its
// lifecycle. It owns lifecycle, retries, progress, timing, and issue ownership. Workers own nothing
// and are disposable (Law 4). The engine contains NO AI reasoning (Law 18); AI appears only behind
// the Worker port.
//
// This is the S2 walking skeleton: it supports exactly one complete Assignment lifecycle and persists
// only the minimum needed for ownership, retry, restart, and resume. The full database emerges later
// (docs/21 S3); there is deliberately no framework here.
package assignment

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// State is a point in the Assignment lifecycle. The skeleton uses the subset needed to complete one
// issue end-to-end (a superset — Blocked/Cancelled — arrives with the full Decision Engine, S6).
type State string

const (
	StateClaimed    State = "claimed"    // issue claimed, branch + worktree ready
	StateDeveloping State = "developing" // spawn one disposable AI worker → apply result → commit
	StateQA         State = "qa"         // handed over to QA
	StateMerging    State = "merging"    // handed over to Merge (deterministic --no-ff)
	StateDone       State = "done"       // merged, Jira closed, lock released
	StateFailed     State = "failed"     // attempts exhausted / unrecoverable
)

// Note: the skeleton spawns exactly ONE worker, so Planning+Coding (docs/16) collapse into the single
// resumable StateDeveloping. The Manager/Developer split arrives with the full Worker Runner (S7).

// Terminal reports whether no further work will happen for this Assignment.
func (s State) Terminal() bool { return s == StateDone || s == StateFailed }

// Assignment is the durable record. Its existence (non-terminal) IS the issue lock (docs/15): the
// Store refuses a second active Assignment for the same issue key.
type Assignment struct {
	ID        string `json:"id"`        // == issue key (one active Assignment per issue)
	IssueKey  string `json:"issue_key"` // Jira key
	Owner     string `json:"owner"`     // engine/run id holding the claim
	State     State  `json:"state"`     // lifecycle position (checkpoint for resume)
	Attempt   int    `json:"attempt"`   // retry counter
	Branch    string `json:"branch"`    // agent/<KEY>-<slug>
	Worktree  string `json:"worktree"`  // per-Assignment worktree path
	Summary   string `json:"summary"`   // issue summary (for logs/handoff)
	Progress  string `json:"progress"`  // short human-readable progress note
	MergeSHA  string `json:"merge_sha,omitempty"`
	CreatedAt string `json:"created_at"` // timing
	UpdatedAt string `json:"updated_at"`
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

// Store is the minimum persistence: one JSON file per Assignment under dir. Atomic writes (temp +
// fsync + rename) make it restart-safe. This is the seam the emergent state.db (S3) later replaces.
type Store struct {
	dir string
	mu  sync.Mutex
}

// NewStore opens (creating) the assignments directory.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("assignment store %q: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

func (s *Store) path(id string) string { return filepath.Join(s.dir, id+".json") }

// Save atomically persists a (temp file, fsync, rename), so a crash never leaves a half-written record.
func (s *Store) Save(a *Assignment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a.UpdatedAt = nowUTC()
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, s.path(a.ID))
}

// Load returns the Assignment with id, or ok=false if none.
func (s *Store) Load(id string) (*Assignment, bool, error) {
	b, err := os.ReadFile(s.path(id))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var a Assignment
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, false, err
	}
	return &a, true, nil
}

// List returns all persisted Assignments, sorted by id for determinism.
func (s *Store) List() ([]*Assignment, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var out []*Assignment
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		id := e.Name()[:len(e.Name())-len(".json")]
		a, ok, err := s.Load(id)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Unfinished returns all non-terminal Assignments (used by Resume after a restart).
func (s *Store) Unfinished() ([]*Assignment, error) {
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	var out []*Assignment
	for _, a := range all {
		if !a.State.Terminal() {
			out = append(out, a)
		}
	}
	return out, nil
}
