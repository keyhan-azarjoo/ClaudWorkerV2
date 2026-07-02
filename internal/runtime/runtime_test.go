package runtime

import (
	"strings"
	"testing"

	"github.com/myotgo/ClaudWorkerV2/internal/assignment"
)

func sampleInput() assignment.WorkerInput {
	return assignment.WorkerInput{
		IssueKey:           "SCRUM-1",
		Summary:            "Add hello file",
		AcceptanceCriteria: "- hello.txt exists",
		KnowledgeContext:   "## Knowledge\n\n### [rule] Branch discipline\nbranch off development\n",
		RelevantFiles:      []assignment.File{{Path: "main.go", Content: "package main"}},
	}
}

func TestBuildPromptDeterministic(t *testing.T) {
	in := sampleInput()
	first := BuildPrompt(in)
	for i := 0; i < 10; i++ {
		if BuildPrompt(in) != first {
			t.Fatal("BuildPrompt is not byte-stable")
		}
	}
}

func TestBuildPromptContainsExactlyThePermittedInputs(t *testing.T) {
	in := sampleInput()
	p := BuildPrompt(in)
	for _, want := range []string{"SCRUM-1", "Add hello file", "hello.txt exists", "Branch discipline", "main.go", "package main"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing permitted input %q", want)
		}
	}
}

// TestBuildPromptExcludesExecutionState is the S5 guard: no execution-state vocabulary may leak into
// the prompt. Even though WorkerInput cannot carry such fields, this asserts the boundary explicitly.
func TestBuildPromptExcludesExecutionState(t *testing.T) {
	p := strings.ToLower(BuildPrompt(sampleInput()))
	for _, banned := range []string{"attempt", "retry", "lock", "state:", "merging", "developing", "spec_version"} {
		if strings.Contains(p, banned) {
			t.Errorf("prompt leaked execution-state token %q", banned)
		}
	}
}

func TestEstimateTokens(t *testing.T) {
	cases := map[int]int{0: 0, 1: 1, 4: 1, 5: 2, 8: 2, 9: 3}
	for in, want := range cases {
		if got := EstimateTokens(in); got != want {
			t.Errorf("EstimateTokens(%d) = %d, want %d", in, got, want)
		}
	}
}
