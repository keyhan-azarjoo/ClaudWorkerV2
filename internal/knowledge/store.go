package knowledge

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// FileStore persists each entry's version history as an append-only JSON-Lines file
// (<dir>/<id>.jsonl), one JSON record per line in append order. Appending a version literally
// appends a line — a prior record is never rewritten or deleted (invariant 5), which also makes the
// history trivially git-diffable (docs/04). The current entry is the highest-version line.
//
// ID → filename is sanitised so an id can never escape the directory; the real id lives in the
// record, so IDs()/History() read it from content, not the filename.
type FileStore struct {
	dir string
	mu  sync.Mutex
}

// NewFileStore opens (creating) the directory.
func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("knowledge store %q: %w", dir, err)
	}
	return &FileStore{dir: dir}, nil
}

// safeName maps an id to a filesystem-safe base name. It is deterministic and collision-safe for the
// id charset the Brain enforces (see Brain.validateID): letters, digits, '-', '_', '.'.
func safeName(id string) string {
	repl := strings.NewReplacer("/", "_", "\\", "_", string(os.PathSeparator), "_")
	return repl.Replace(id) + ".jsonl"
}

func (s *FileStore) path(id string) string { return filepath.Join(s.dir, safeName(id)) }

// Append writes one version record as a single JSON line, flushing to disk. os.O_APPEND makes the
// write atomic per line on local filesystems, so a crash mid-append cannot corrupt earlier versions.
func (s *FileStore) Append(e *Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e.SchemaVersion = SchemaVersion
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.path(e.ID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// History reads every version line for id, validating + migrating each, returned ascending by
// Version. Records are sorted by Version (append order is normally already ascending; sorting makes
// the contract robust to any manual edit).
func (s *FileStore) History(id string) ([]*Entry, bool, error) {
	f, err := os.Open(s.path(id))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer f.Close()

	var out []*Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // allow large knowledge bodies
	for sc.Scan() {
		b := sc.Bytes()
		if len(strings.TrimSpace(string(b))) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(b, &e); err != nil {
			return nil, false, fmt.Errorf("knowledge %q: corrupt record: %w", id, err)
		}
		if err := migrate(&e); err != nil {
			return nil, false, err
		}
		out = append(out, &e)
	}
	if err := sc.Err(); err != nil {
		return nil, false, err
	}
	if len(out) == 0 {
		return nil, false, nil
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, true, nil
}

// IDs returns the id of every history file, sorted (read from record content, not the filename).
func (s *FileStore) IDs() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		id, err := s.firstID(filepath.Join(s.dir, e.Name()))
		if err != nil {
			return nil, err
		}
		if id != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// firstID reads the id from the first record of a history file.
func (s *FileStore) firstID(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		if len(strings.TrimSpace(sc.Text())) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return "", err
		}
		return e.ID, nil
	}
	return "", sc.Err()
}

// MemoryStore is an in-memory append-only Store — the second implementation proving the Brain is
// decoupled from persistence, and the default for tests. Not crash-safe (correct: it demonstrates
// the interface, it is not meant to survive restarts).
type MemoryStore struct {
	mu sync.Mutex
	m  map[string][]*Entry // id → append-ordered versions
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore { return &MemoryStore{m: map[string][]*Entry{}} }

func (s *MemoryStore) Append(e *Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e.SchemaVersion = SchemaVersion
	s.m[e.ID] = append(s.m[e.ID], e.clone()) // store a copy; callers can't mutate persisted state
	return nil
}

func (s *MemoryStore) History(id string) ([]*Entry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[id]
	if !ok || len(v) == 0 {
		return nil, false, nil
	}
	out := make([]*Entry, len(v))
	for i, e := range v {
		out[i] = e.clone()
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, true, nil
}

func (s *MemoryStore) IDs() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.m))
	for id := range s.m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}
