package knowledge

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFileStoreIsAppendOnly proves invariant 5: appending a new version adds a line and never
// rewrites or deletes an earlier one. We inspect the raw JSONL file after several appends.
func TestFileStoreIsAppendOnly(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	b := New(s, WithClock(fixedClock()))
	_, _ = b.Create("k", "rule", "T1", "b1", SourceHuman, StatusActive)
	body2 := "b2"
	_, _ = b.Update("k", Change{Body: &body2})
	_, _ = b.Deprecate("k")

	raw, err := os.ReadFile(filepath.Join(dir, "k.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// exactly 3 append lines, in ascending version order, all three bodies preserved.
	var lines []string
	sc := bufio.NewScanner(strings.NewReader(string(raw)))
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			lines = append(lines, sc.Text())
		}
	}
	if len(lines) != 3 {
		t.Fatalf("appended %d lines, want 3 (append-only)", len(lines))
	}
	if !strings.Contains(lines[0], `"body":"b1"`) || !strings.Contains(lines[0], `"version":1`) {
		t.Errorf("line 1 = %s", lines[0])
	}
	if !strings.Contains(lines[2], `"status":"deprecated"`) || !strings.Contains(lines[2], `"version":3`) {
		t.Errorf("line 3 = %s", lines[2])
	}
}

// TestFileStorePersistsAcrossReopen proves durable recovery: a fresh FileStore over the same dir
// reads the full history purely from disk.
func TestFileStorePersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s1, _ := NewFileStore(dir)
	b1 := New(s1, WithClock(fixedClock()))
	_, _ = b1.Create("k", "decision", "D", "chose Go", SourceACP, StatusActive)
	body := "chose Go 1.26"
	_, _ = b1.Update("k", Change{Body: &body})

	s2, _ := NewFileStore(dir) // reopen: reload from disk only
	b2 := New(s2, WithClock(fixedClock()))
	cur, ok, err := b2.Get("k")
	if err != nil || !ok {
		t.Fatalf("reopen Get: ok=%v err=%v", ok, err)
	}
	if cur.Version != 2 || cur.Body != "chose Go 1.26" {
		t.Errorf("reopened current = %+v", cur)
	}
	hist, _, _ := b2.History("k")
	if len(hist) != 2 {
		t.Errorf("reopened history len = %d, want 2", len(hist))
	}
}

func TestStoreRejectsNewerFormat(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "k.jsonl"),
		[]byte(`{"id":"k","version":1,"category":"rule","title":"T","body":"x","source":"human","status":"active","schema_version":999}`+"\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	s, _ := NewFileStore(dir)
	if _, _, err := s.History("k"); err == nil {
		t.Error("History must reject a newer format version, not silently ignore it")
	}
}

func TestStoreMigratesLegacyRecord(t *testing.T) {
	dir := t.TempDir()
	// a pre-versioning record on disk (no schema_version)
	if err := os.WriteFile(filepath.Join(dir, "k.jsonl"),
		[]byte(`{"id":"k","version":1,"category":"rule","title":"T","body":"x","source":"human","status":"active"}`+"\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	s, _ := NewFileStore(dir)
	hist, ok, err := s.History("k")
	if err != nil || !ok {
		t.Fatalf("History legacy: ok=%v err=%v", ok, err)
	}
	if hist[0].SchemaVersion != SchemaVersion {
		t.Errorf("legacy record migrated to v%d, want v%d", hist[0].SchemaVersion, SchemaVersion)
	}
}

func TestMemoryStoreCopiesNotAliases(t *testing.T) {
	s := NewMemoryStore()
	e := &Entry{ID: "k", Version: 1, Category: "rule", Title: "T", Body: "x", Source: SourceHuman, Status: StatusActive}
	_ = s.Append(e)
	e.Body = "MUTATED" // mutate caller's copy after appending
	hist, _, _ := s.History("k")
	if hist[0].Body != "x" {
		t.Errorf("stored body changed via caller reference: %q", hist[0].Body)
	}
}

func TestIDsSortedFromContent(t *testing.T) {
	for name, s := range map[string]Store{"file": mustFileStore(t), "memory": NewMemoryStore()} {
		t.Run(name, func(t *testing.T) {
			b := New(s, WithClock(fixedClock()))
			_, _ = b.Create("zeta", "rule", "T", "x", SourceHuman, StatusActive)
			_, _ = b.Create("alpha", "rule", "T", "x", SourceHuman, StatusActive)
			ids, _ := s.IDs()
			if len(ids) != 2 || ids[0] != "alpha" || ids[1] != "zeta" {
				t.Errorf("IDs = %v, want [alpha zeta]", ids)
			}
		})
	}
}

func mustFileStore(t *testing.T) *FileStore {
	s, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}
