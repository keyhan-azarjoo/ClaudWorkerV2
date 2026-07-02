package assignment

import "testing"

func TestStoreSaveLoadListActive(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &Assignment{ID: "SCRUM-1", IssueKey: "SCRUM-1", State: StateClaimed, CreatedAt: nowUTC()}
	if err := s.Save(a); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Load("SCRUM-1")
	if err != nil || !ok {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	if got.State != StateClaimed || got.UpdatedAt == "" {
		t.Errorf("loaded = %+v", got)
	}
	list, _ := s.List()
	if len(list) != 1 {
		t.Errorf("List len = %d, want 1", len(list))
	}
}

func TestStoreUnfinished(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	_ = s.Save(&Assignment{ID: "A", IssueKey: "A", State: StateDeveloping})
	_ = s.Save(&Assignment{ID: "B", IssueKey: "B", State: StateDone})
	un, err := s.Unfinished()
	if err != nil {
		t.Fatal(err)
	}
	if len(un) != 1 || un[0].ID != "A" {
		t.Errorf("Unfinished = %+v, want [A]", un)
	}
}

// BenchmarkStoreSave measures the persistence hot path (atomic write + fsync). Every state
// transition persists once, so this bounds per-transition overhead.
func BenchmarkStoreSave(b *testing.B) {
	s, _ := NewStore(b.TempDir())
	a := &Assignment{ID: "SCRUM-1", IssueKey: "SCRUM-1", State: StateDeveloping, CreatedAt: nowUTC()}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := s.Save(a); err != nil {
			b.Fatal(err)
		}
	}
}

func TestStoreLoadMissing(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	if _, ok, err := s.Load("nope"); ok || err != nil {
		t.Errorf("Load missing = ok:%v err:%v, want false,nil", ok, err)
	}
}
