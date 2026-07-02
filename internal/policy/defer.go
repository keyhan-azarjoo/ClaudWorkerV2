package policy

// DeferPolicy decides whether an Assignment should be deferred (paused pending an external
// unblocker) rather than retried or failed. It is configuration-free: the decision is a pure function
// of the blocking signals.
type DeferPolicy struct{}

// DeferInput is the observable state.
type DeferInput struct {
	Blocked           bool
	MissingDependency bool
	Kind              string // caller-supplied classification, surfaced as the follow-up kind
}

// DeferDecision is the observable answer. FollowupKind labels what must happen before resuming.
type DeferDecision struct {
	Defer        bool
	FollowupKind string
	Reason       string
}

// Decide reports whether to defer.
func (DeferPolicy) Decide(in DeferInput) DeferDecision {
	switch {
	case in.MissingDependency:
		return DeferDecision{Defer: true, FollowupKind: kindOr(in.Kind, "dependency"), Reason: "missing dependency: defer until available"}
	case in.Blocked:
		return DeferDecision{Defer: true, FollowupKind: kindOr(in.Kind, "blocked"), Reason: "blocked: defer until unblocked"}
	default:
		return DeferDecision{Defer: false, Reason: "no blocking condition"}
	}
}

func kindOr(kind, fallback string) string {
	if kind != "" {
		return kind
	}
	return fallback
}
