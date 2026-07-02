package assignment

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// FileStore persists Assignments as one JSON file per issue under a directory. Atomic writes
// (temp + fsync + rename) make it crash-safe. It is one implementation of Store; the engine does not
// know it exists (it sees only the interface). The emergent "state.db" (SQLite/Bolt/…) could replace
// this with no engine change.
type FileStore struct {
	dir string
	mu  sync.Mutex
}

// NewFileStore opens (creating) the directory.
func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("state store %q: %w", dir, err)
	}
	return &FileStore{dir: dir}, nil
}

func (s *FileStore) path(issueKey string) string { return filepath.Join(s.dir, issueKey+".json") }

// Save atomically writes the minimal record (temp + fsync + rename).
func (s *FileStore) Save(a *Assignment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	return os.Rename(tmpName, s.path(a.IssueKey))
}

// Load reads one record.
func (s *FileStore) Load(issueKey string) (*Assignment, bool, error) {
	b, err := os.ReadFile(s.path(issueKey))
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

// List returns all records, sorted by issue key for determinism.
func (s *FileStore) List() ([]*Assignment, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var out []*Assignment
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		key := e.Name()[:len(e.Name())-len(".json")]
		a, ok, err := s.Load(key)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IssueKey < out[j].IssueKey })
	return out, nil
}

// MemoryStore is an in-memory Store — the second implementation that proves the engine is fully
// decoupled from persistence, and the default for tests. It is NOT crash-safe (memory disappears on
// exit), which is correct: it exists to demonstrate the interface, not to survive restarts.
type MemoryStore struct {
	mu sync.Mutex
	m  map[string]Assignment
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore { return &MemoryStore{m: map[string]Assignment{}} }

func (s *MemoryStore) Save(a *Assignment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[a.IssueKey] = *a // store a copy so callers can't mutate persisted state by reference
	return nil
}

func (s *MemoryStore) Load(issueKey string) (*Assignment, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.m[issueKey]
	if !ok {
		return nil, false, nil
	}
	cp := a
	return &cp, true, nil
}

func (s *MemoryStore) List() ([]*Assignment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Assignment, 0, len(s.m))
	for _, a := range s.m {
		cp := a
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IssueKey < out[j].IssueKey })
	return out, nil
}
