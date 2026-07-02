package policy

import "fmt"

// BudgetConfig centralises EVERY usage rule (S6): the usage guard, pause/resume hysteresis, daily
// token limits, and account selection. This replaces usage decisions scattered across the runtime.
type BudgetConfig struct {
	PausePct        int      // pause new work once 5h usage reaches this percent (e.g. 95)
	ResumePct       int      // resume only after usage falls back to/below this (hysteresis)
	FailOpen        bool     // when usage is UNKNOWN: true = allow, false = hold (safe default)
	DailyTokenLimit int      // 0 = no daily cap
	Accounts        []string // account-selection order (first is preferred)
}

// BudgetPolicy answers "may work proceed right now, and on which account?". It never queries usage
// itself — the live signals are inputs, keeping it deterministic, testable, and restart-safe. This is
// the single home for the owner's never-spend / usage-guard rules.
type BudgetPolicy struct{ cfg BudgetConfig }

// BudgetInput is the observable live state supplied by the caller.
type BudgetInput struct {
	UsagePct        int  // current 5h usage percent
	TokensUsedToday int  // tokens consumed today
	UsageKnown      bool // false when the usage signal is unavailable
	InCooldown      bool // true if currently paused (drives resume hysteresis)
}

// BudgetDecision is the observable answer.
type BudgetDecision struct {
	Allow    bool
	Pause    bool
	Cooldown bool
	Account  string
	Reason   string
}

// Decide applies the usage rules deterministically.
func (p BudgetPolicy) Decide(in BudgetInput) BudgetDecision {
	if !in.UsageKnown {
		return BudgetDecision{Allow: p.cfg.FailOpen, Pause: !p.cfg.FailOpen, Account: p.account(),
			Reason: fmt.Sprintf("usage unknown: fail-open=%v", p.cfg.FailOpen)}
	}
	if p.cfg.DailyTokenLimit > 0 && in.TokensUsedToday >= p.cfg.DailyTokenLimit {
		return BudgetDecision{Allow: false, Pause: true,
			Reason: fmt.Sprintf("daily token limit reached (%d/%d)", in.TokensUsedToday, p.cfg.DailyTokenLimit)}
	}
	if in.UsagePct >= p.cfg.PausePct {
		return BudgetDecision{Allow: false, Pause: true,
			Reason: fmt.Sprintf("usage %d%% >= pause %d%%", in.UsagePct, p.cfg.PausePct)}
	}
	// Hysteresis: while cooling down, stay paused until usage drops to/below ResumePct.
	if in.InCooldown && in.UsagePct > p.cfg.ResumePct {
		return BudgetDecision{Allow: false, Cooldown: true,
			Reason: fmt.Sprintf("cooldown: usage %d%% > resume %d%%", in.UsagePct, p.cfg.ResumePct)}
	}
	return BudgetDecision{Allow: true, Account: p.account(),
		Reason: fmt.Sprintf("within budget (usage %d%% < pause %d%%)", in.UsagePct, p.cfg.PausePct)}
}

func (p BudgetPolicy) account() string {
	if len(p.cfg.Accounts) > 0 {
		return p.cfg.Accounts[0]
	}
	return ""
}
