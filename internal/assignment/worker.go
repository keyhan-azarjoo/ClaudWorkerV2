package assignment

import "context"

// Worker is the ONE justified interface in S2: it has two realistic implementations — the real
// Worker Runtime (`internal/runtime`, wrapping `claude -p`) and an in-memory fake (tests). AI lives
// only behind this port; the engine stays deterministic (Law 18).
type Worker interface {
	Run(ctx context.Context, in WorkerInput) (WorkerResult, error)
}

// WorkerInput is EXACTLY what the AI receives (docs/05, docs/16): the Assignment identity, acceptance
// criteria, relevant files, and Knowledge-Brain context. It deliberately carries NO execution state,
// NO Git logic, NO Jira logic, and NO lock logic.
type WorkerInput struct {
	IssueKey           string
	Summary            string
	AcceptanceCriteria string
	RelevantFiles      []File
	KnowledgeContext   string
	OperatorNote       string // optional operator guidance passed on a manual Continue (what to do next)
}

// File is a path + content pair used both for relevant-file context and for proposed writes.
type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// WorkerResult is the structured result the engine consumes. No free-form text is parsed elsewhere.
type WorkerResult struct {
	OK      bool   `json:"ok"`
	Summary string `json:"summary"`
	Files   []File `json:"files"` // files to write into the worktree
	Notes   string `json:"notes"`
}
