package runtime

import (
	"strings"
	"testing"

	"claudworker/internal/assignment"
)

func TestBuildPromptInjectsAccessGrants(t *testing.T) {
	p := BuildPrompt(assignment.WorkerInput{
		IssueKey:     "SCRUM-25",
		Summary:      "do the thing",
		AccessGrants: []string{"/Users/me/Project", "/Users/me/plans/PLAN.md"},
	})
	if !strings.Contains(p, "Granted access") {
		t.Fatalf("prompt should have a granted-access section:\n%s", p)
	}
	for _, want := range []string{"/Users/me/Project", "/Users/me/plans/PLAN.md"} {
		if !strings.Contains(p, want) {
			t.Fatalf("granted path %q missing from prompt", want)
		}
	}
	// No grants → no section.
	p2 := BuildPrompt(assignment.WorkerInput{IssueKey: "X", Summary: "s"})
	if strings.Contains(p2, "Granted access") {
		t.Fatal("no grants should mean no granted-access section")
	}
}
