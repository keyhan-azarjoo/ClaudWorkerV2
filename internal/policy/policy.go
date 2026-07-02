// Package policy is the Policy Engine (docs/20, docs/21 S6).
//
// Renamed Decision Engine → Policy Engine to make explicit that ClaudWorker does not "think": it
// executes deterministic policies. The Policy Engine contains NO AI and owns policies ONLY. It has no
// hidden state, no Git, no Jira, no runtime execution, no QA — just policy evaluation.
//
// Every policy is a small, independent module that is:
//   - deterministic  — same inputs always produce the same outputs (pure functions over config+input);
//   - independently testable — each policy stands alone;
//   - configurable   — behaviour comes from Config, not code;
//   - restart-safe   — policies hold no mutable/persisted state, so a restart changes nothing;
//   - observable     — every decision carries a Reason string.
//
// The Assignment Engine ASKS the Policy Engine and receives a deterministic answer; it never encodes
// the numbers itself (e.g. it no longer knows the retry cap — RetryPolicy decides).
package policy

// Engine aggregates the individual policies. It is a plain value built from Config; construct it once
// (New) and share it — it is safe for concurrent use because every policy is stateless.
type Engine struct {
	Retry            RetryPolicy
	RuntimeSelection RuntimeSelectionPolicy
	Merge            MergePolicy
	Escalation       EscalationPolicy
	Split            SplitPolicy
	Defer            DeferPolicy
	Budget           BudgetPolicy
	Approval         ApprovalPolicy
	Failure          FailurePolicy
}

// New builds the Policy Engine from Config, applying defaults for any zero values.
func New(c Config) *Engine {
	c = c.withDefaults()
	retry := RetryPolicy{cfg: c.Retry}
	esc := EscalationPolicy{cfg: c.Escalation}
	return &Engine{
		Retry:            retry,
		RuntimeSelection: RuntimeSelectionPolicy{cfg: c.Runtime},
		Merge:            MergePolicy{cfg: c.Merge},
		Escalation:       esc,
		Split:            SplitPolicy{cfg: c.Split},
		Defer:            DeferPolicy{},
		Budget:           BudgetPolicy{cfg: c.Budget},
		Approval:         ApprovalPolicy{},
		// Failure composes Retry + Escalation rather than duplicating their thresholds.
		Failure: FailurePolicy{retry: retry, esc: esc},
	}
}
