package assignment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestPersistedRecordIsMinimal is the S3 guard: the on-disk record must contain EXACTLY the three
// non-recomputable fields and nothing else. If someone adds a persisted field, this fails until they
// justify it in PERSISTENCE_REVIEW_S3.md.
func TestPersistedRecordIsMinimal(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(&Assignment{IssueKey: "SCRUM-1", State: StateMerging, Attempt: 2}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "SCRUM-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"issue_key": true, "state": true, "attempt": true}
	if len(m) != len(want) {
		t.Fatalf("persisted %d fields %v, want exactly %v", len(m), keys(m), want)
	}
	for k := range m {
		if !want[k] {
			t.Errorf("unexpected persisted field %q (add justification to PERSISTENCE_REVIEW_S3.md)", k)
		}
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// storeFactories lets every Store test run against BOTH implementations, proving the engine's
// storage-agnostic contract holds for each.
func storeFactories(t *testing.T) map[string]func() Store {
	return map[string]func() Store{
		"file": func() Store {
			s, err := NewFileStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			return s
		},
		"memory": func() Store { return NewMemoryStore() },
	}
}

func TestStoreSaveLoadList(t *testing.T) {
	for name, make := range storeFactories(t) {
		t.Run(name, func(t *testing.T) {
			s := make()
			if _, ok, _ := s.Load("SCRUM-1"); ok {
				t.Fatal("expected empty store")
			}
			a := &Assignment{IssueKey: "SCRUM-1", State: StateDeveloping, Attempt: 1}
			if err := s.Save(a); err != nil {
				t.Fatal(err)
			}
			got, ok, err := s.Load("SCRUM-1")
			if err != nil || !ok {
				t.Fatalf("Load: ok=%v err=%v", ok, err)
			}
			if got.State != StateDeveloping || got.Attempt != 1 || got.IssueKey != "SCRUM-1" {
				t.Errorf("loaded = %+v", got)
			}
			list, _ := s.List()
			if len(list) != 1 {
				t.Errorf("List len = %d, want 1", len(list))
			}
		})
	}
}

func TestStoreSaveOverwrites(t *testing.T) {
	for name, make := range storeFactories(t) {
		t.Run(name, func(t *testing.T) {
			s := make()
			_ = s.Save(&Assignment{IssueKey: "K", State: StateClaimed})
			_ = s.Save(&Assignment{IssueKey: "K", State: StateDone, Attempt: 2})
			got, _, _ := s.Load("K")
			if got.State != StateDone || got.Attempt != 2 {
				t.Errorf("overwrite failed: %+v", got)
			}
			list, _ := s.List()
			if len(list) != 1 {
				t.Errorf("List len = %d, want 1 (same key)", len(list))
			}
		})
	}
}

func TestStoreLoadMissing(t *testing.T) {
	for name, make := range storeFactories(t) {
		t.Run(name, func(t *testing.T) {
			s := make()
			if _, ok, err := s.Load("nope"); ok || err != nil {
				t.Errorf("Load missing = ok:%v err:%v, want false,nil", ok, err)
			}
		})
	}
}

func TestMemoryStoreCopiesNotAliases(t *testing.T) {
	s := NewMemoryStore()
	a := &Assignment{IssueKey: "K", State: StateClaimed}
	_ = s.Save(a)
	a.State = StateFailed // mutate caller's copy after saving
	got, _, _ := s.Load("K")
	if got.State != StateClaimed {
		t.Errorf("stored state changed via caller reference: %s", got.State)
	}
}

// BenchmarkFileStoreSave measures the persistence hot path (atomic write + fsync).
func BenchmarkFileStoreSave(b *testing.B) {
	s, _ := NewFileStore(b.TempDir())
	a := &Assignment{IssueKey: "SCRUM-1", State: StateDeveloping, Attempt: 1}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := s.Save(a); err != nil {
			b.Fatal(err)
		}
	}
}
