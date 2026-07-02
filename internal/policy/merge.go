package policy

// MergeConfig holds merge rules (sourced from workflow.merge).
type MergeConfig struct {
	Strategy         string // e.g. "no-ff"
	DeleteBranch     bool   // delete the branch after a successful merge
	RequireGatesPass bool   // refuse to merge unless QA gates passed
}

// MergePolicy decides whether (and how) to merge. It never touches Git — it only returns the
// decision; the deterministic Git toolbelt performs the merge.
type MergePolicy struct{ cfg MergeConfig }

// MergeInput is the observable state the decision depends on.
type MergeInput struct {
	GatesPassed bool
	Conflicts   bool
}

// MergeDecision is the observable answer.
type MergeDecision struct {
	Merge        bool
	Strategy     string
	DeleteBranch bool
	Reason       string
}

// Decide returns whether to merge. A conflict is never forced; gates are enforced when configured.
func (p MergePolicy) Decide(in MergeInput) MergeDecision {
	d := MergeDecision{Strategy: p.cfg.Strategy, DeleteBranch: p.cfg.DeleteBranch}
	switch {
	case in.Conflicts:
		d.Reason = "merge conflicts present: do not force"
	case p.cfg.RequireGatesPass && !in.GatesPassed:
		d.Reason = "QA gates not passed: hold merge"
	default:
		d.Merge = true
		d.Reason = "clean and gated: merge"
	}
	return d
}
