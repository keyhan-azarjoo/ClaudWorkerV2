package policy

// Automation mirrors the Jira Automation single-select field values (owner decision 1). It is the
// input that gates whether ClaudWorker may act autonomously.
type Automation string

const (
	AutomationEnabled     Automation = "Enabled"
	AutomationDisabled    Automation = "Disabled"
	AutomationManualOnly  Automation = "Manual Only"
	AutomationNeedsReview Automation = "Needs Review"
)

// ApprovalPolicy decides whether an action may proceed autonomously or needs human approval, based on
// the Assignment's Automation setting. Configuration-free and deterministic; unknown values are
// treated conservatively (require approval).
type ApprovalPolicy struct{}

// ApprovalInput is the observable state.
type ApprovalInput struct {
	Automation Automation
	ActionKind string // e.g. "develop", "merge" (surfaced in the reason)
}

// ApprovalDecision is the observable answer.
type ApprovalDecision struct {
	Proceed         bool
	RequireApproval bool
	Reason          string
}

// Decide maps the Automation value to an action decision.
func (ApprovalPolicy) Decide(in ApprovalInput) ApprovalDecision {
	switch in.Automation {
	case AutomationEnabled:
		return ApprovalDecision{Proceed: true, Reason: "automation enabled: proceed"}
	case AutomationDisabled:
		return ApprovalDecision{Proceed: false, Reason: "automation disabled: do not act"}
	case AutomationManualOnly:
		return ApprovalDecision{RequireApproval: true, Reason: "manual only: require human action"}
	case AutomationNeedsReview:
		return ApprovalDecision{RequireApproval: true, Reason: "needs review: require approval before proceeding"}
	default:
		return ApprovalDecision{RequireApproval: true, Reason: "unknown automation value: require approval (safe default)"}
	}
}
