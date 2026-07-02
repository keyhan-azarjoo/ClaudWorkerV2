package policy

// FailureAction is the deterministic disposition of a failed attempt.
type FailureAction string

const (
	ActionRetry    FailureAction = "retry"
	ActionDefer    FailureAction = "defer"
	ActionEscalate FailureAction = "escalate"
	ActionFail     FailureAction = "fail"
)

// FailurePolicy classifies what to do with a failed attempt. It COMPOSES RetryPolicy and
// EscalationPolicy rather than duplicating their thresholds — a single place to turn a failure into an
// action, built from the atomic policies.
type FailurePolicy struct {
	retry RetryPolicy
	esc   EscalationPolicy
}

// FailureInput is the observable state.
type FailureInput struct {
	Attempts  int
	Transient bool
	Blocked   bool
}

// FailureDecision is the observable answer.
type FailureDecision struct {
	Action FailureAction
	Reason string
}

// Classify decides: defer if blocked; else retry while the retry policy allows; else escalate if the
// escalation policy says so; else fail. Deterministic given the same inputs and composed configs.
func (p FailurePolicy) Classify(in FailureInput) FailureDecision {
	if in.Blocked {
		return FailureDecision{ActionDefer, "blocked: defer"}
	}
	if d := p.retry.Decide(in.Attempts, in.Transient); d.Retry {
		return FailureDecision{ActionRetry, d.Reason}
	}
	if e := p.esc.Decide(EscalationInput{Attempts: in.Attempts, Failed: true}); e.Escalate {
		return FailureDecision{ActionEscalate, e.Reason}
	}
	return FailureDecision{ActionFail, "retries exhausted and no escalation condition: fail"}
}
