package lease

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// FileStore persists each lease as one JSON file (<dir>/<id>.json) with atomic writes (temp + fsync +
// rename), so leases survive a crash/restart (recovery). Release/Reap delete the file.
type FileStore struct {
	dir string
	mu  sync.Mutex
}

// NewFileStore opens (creating) the directory.
func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("lease store %q: %w", dir, err)
	}
	return &FileStore{dir: dir}, nil
}

// fileName maps a lease id ("<kind>/<resource>") to a filesystem-safe base name.
func fileName(id string) string {
	repl := strings.NewReplacer("/", "_", "\\", "_", string(os.PathSeparator), "_")
	return repl.Replace(id) + ".json"
}

func (s *FileStore) path(id string) string { return filepath.Join(s.dir, fileName(id)) }

func (s *FileStore) Save(l *Lease) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	l.SpecVersion = SpecVersion
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, s.path(l.ID))
}

func (s *FileStore) Load(id string) (*Lease, bool, error) {
	b, err := os.ReadFile(s.path(id))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var l Lease
	if err := json.Unmarshal(b, &l); err != nil {
		return nil, false, err
	}
	if err := migrate(&l); err != nil {
		return nil, false, err
	}
	return &l, true, nil
}

func (s *FileStore) List() ([]*Lease, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var out []*Lease
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var l Lease
		if err := json.Unmarshal(b, &l); err != nil {
			return nil, err
		}
		if err := migrate(&l); err != nil {
			return nil, err
		}
		out = append(out, &l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *FileStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(s.path(id))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// MemoryStore is the in-memory Store (tests / decoupling proof). Not crash-safe by design.
type MemoryStore struct {
	mu sync.Mutex
	m  map[string]Lease
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore { return &MemoryStore{m: map[string]Lease{}} }

func (s *MemoryStore) Save(l *Lease) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	l.SpecVersion = SpecVersion
	s.m[l.ID] = *l
	return nil
}

func (s *MemoryStore) Load(id string) (*Lease, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.m[id]
	if !ok {
		return nil, false, nil
	}
	cp := l
	return &cp, true, nil
}

func (s *MemoryStore) List() ([]*Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Lease, 0, len(s.m))
	for _, l := range s.m {
		cp := l
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *MemoryStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
	return nil
}
