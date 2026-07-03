package improvement

import (
	"claudworker/internal/policy"
	"claudworker/internal/verify"
)

// PolicyDecider adapts the S6 Policy Engine's FailurePolicy to the StopDecider port, so the loop's
// stop decision is genuinely owned by the Policy Engine (the Improvement Engine never decides).
//
// Mapping of loop state → FailurePolicy inputs:
//   - Iteration      → Attempts (the retry cap lives in RetryPolicy, not here);
//   - Stuck          → treated as NON-transient (repeated identical failure is not worth retrying);
//   - Blocked outcome→ Blocked (→ defer).
type PolicyDecider struct{ Failure policy.FailurePolicy }

// NewPolicyDecider builds a decider from a policy.Engine.
func NewPolicyDecider(p *policy.Engine) PolicyDecider { return PolicyDecider{Failure: p.Failure} }

// Decide maps the composed FailurePolicy action to a loop Decision.
func (d PolicyDecider) Decide(in StopInput) Decision {
	fd := d.Failure.Classify(policy.FailureInput{
		Attempts:  in.Iteration,
		Transient: !in.Stuck,
		Blocked:   in.Outcome == verify.Blocked,
	})
	switch fd.Action {
	case policy.ActionRetry:
		return Continue
	case policy.ActionDefer:
		return Defer
	case policy.ActionEscalate:
		return Escalate
	default:
		return Fail
	}
}
