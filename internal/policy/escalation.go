package policy

import "fmt"

// EscalationConfig holds the rules for handing an Assignment to a human.
type EscalationConfig struct {
	MaxAttempts   int // escalate a failing Assignment once this many attempts are used
	AbandonedDays int // escalate an Assignment older than this many days
}

// EscalationPolicy decides whether a human must be involved. Deterministic and stateless.
type EscalationPolicy struct{ cfg EscalationConfig }

// EscalationInput is the observable state.
type EscalationInput struct {
	Attempts int
	Blocked  bool
	Failed   bool
	AgeDays  int
}

// EscalationDecision is the observable answer.
type EscalationDecision struct {
	Escalate bool
	Reason   string
}

// Decide reports whether to escalate.
func (p EscalationPolicy) Decide(in EscalationInput) EscalationDecision {
	switch {
	case in.Blocked:
		return EscalationDecision{true, "blocked: needs human input"}
	case in.Failed && in.Attempts >= p.cfg.MaxAttempts:
		return EscalationDecision{true, fmt.Sprintf("failed after %d/%d attempts", in.Attempts, p.cfg.MaxAttempts)}
	case p.cfg.AbandonedDays > 0 && in.AgeDays >= p.cfg.AbandonedDays:
		return EscalationDecision{true, fmt.Sprintf("stale: %d days >= %d", in.AgeDays, p.cfg.AbandonedDays)}
	default:
		return EscalationDecision{false, "no escalation condition met"}
	}
}
