package main

import "testing"

func TestAccessRequestFillAndPersist(t *testing.T) {
	dir := t.TempDir()
	proj := "/Users/me/Project"
	s := newAccessRequestStore(nil, nil, proj, dir)

	// Access request with NO usable path from the agent → box is filled with the project folder.
	s.add("SCRUM-1", "access", "", "needs a repo")
	got := s.list()
	if len(got) != 1 || got[0].Fill != proj {
		t.Fatalf("access with no path should fill the project folder, got %+v", got)
	}

	// Access request WITH a real path → box is filled with that path.
	s.add("SCRUM-2", "access", "/Users/me/firmware", "needs firmware")
	// Placeholder path → falls back to the project folder (not the garbage).
	s.add("SCRUM-3", "access", "/<the firmware repo>", "placeholder")
	// Approval request → no folder to fill.
	s.add("SCRUM-4", "approval", "", "flash the board")

	byIssue := map[string]*accessRequest{}
	for _, r := range s.list() {
		byIssue[r.Issue] = r
	}
	if byIssue["SCRUM-2"].Fill != "/Users/me/firmware" {
		t.Fatalf("real path should fill as-is, got %q", byIssue["SCRUM-2"].Fill)
	}
	if byIssue["SCRUM-3"].Fill != proj {
		t.Fatalf("placeholder should fall back to the project folder, got %q", byIssue["SCRUM-3"].Fill)
	}
	if byIssue["SCRUM-4"].Fill != "" {
		t.Fatalf("approval should have empty fill, got %q", byIssue["SCRUM-4"].Fill)
	}

	// Persistence: a fresh store loads the same requests from disk (survives a restart).
	s2 := newAccessRequestStore(nil, nil, proj, dir)
	if len(s2.list()) != 4 {
		t.Fatalf("requests should persist across restart, got %d", len(s2.list()))
	}
}
