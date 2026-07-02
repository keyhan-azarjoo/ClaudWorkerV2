package policy

import "fmt"

// SplitConfig holds the size thresholds (sourced from defaults).
type SplitConfig struct {
	LargeIssueThreshold int // flag as "large" (advisory) at/above this many units
	SplitThreshold      int // recommend splitting at/above this many units
}

// SplitPolicy decides whether an issue is too large to attempt as one Assignment. "Units" is a
// deterministic size proxy supplied by the caller (e.g. acceptance-criteria count or an estimate).
type SplitPolicy struct{ cfg SplitConfig }

// SplitInput is the observable state.
type SplitInput struct {
	EstimatedUnits int
}

// SplitDecision is the observable answer.
type SplitDecision struct {
	Split  bool
	Large  bool
	Reason string
}

// Decide reports whether to split (and flags "large" as advisory context).
func (p SplitPolicy) Decide(in SplitInput) SplitDecision {
	d := SplitDecision{Large: p.cfg.LargeIssueThreshold > 0 && in.EstimatedUnits >= p.cfg.LargeIssueThreshold}
	if p.cfg.SplitThreshold > 0 && in.EstimatedUnits >= p.cfg.SplitThreshold {
		d.Split = true
		d.Reason = fmt.Sprintf("estimated %d units >= split threshold %d", in.EstimatedUnits, p.cfg.SplitThreshold)
		return d
	}
	d.Reason = fmt.Sprintf("estimated %d units < split threshold %d", in.EstimatedUnits, p.cfg.SplitThreshold)
	return d
}
