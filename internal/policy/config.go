package policy

import "claudworker/internal/config"

// Config is the Policy Engine's own configuration — one sub-config per policy. It is independent of
// the app config type so policies stay testable in isolation; FromConfig maps the frozen
// config.Config into it (no change to the config package).
type Config struct {
	Retry      RetryConfig
	Runtime    RuntimeConfig
	Merge      MergeConfig
	Escalation EscalationConfig
	Split      SplitConfig
	Budget     BudgetConfig
}

// withDefaults fills zero values with safe defaults so a partial Config still yields sane policies.
func (c Config) withDefaults() Config {
	if c.Retry.MaxAttempts == 0 {
		c.Retry.MaxAttempts = 3
	}
	if c.Escalation.MaxAttempts == 0 {
		c.Escalation.MaxAttempts = c.Retry.MaxAttempts
	}
	if c.Budget.PausePct == 0 {
		c.Budget.PausePct = 95 // owner usage-guard default
	}
	if c.Budget.ResumePct == 0 {
		c.Budget.ResumePct = 80
	}
	if c.Merge.Strategy == "" {
		c.Merge.Strategy = "no-ff"
	}
	if len(c.Runtime.Available) == 0 {
		c.Runtime.Available = []string{"claude"}
	}
	if c.Runtime.Default == "" {
		c.Runtime.Default = c.Runtime.Available[0]
	}
	return c
}

// FromConfig builds a policy.Config from the application config, pulling every value that already
// exists there (retry limits, thresholds, merge, usage guard). Values absent from config.Config use
// policy defaults.
func FromConfig(app config.Config) Config {
	d := app.Defaults
	return Config{
		Retry: RetryConfig{
			MaxAttempts: d.RetryLimits.MaxAttempts,
		},
		Escalation: EscalationConfig{
			MaxAttempts:   d.RetryLimits.MaxAttempts,
			AbandonedDays: d.AbandonedDays,
		},
		Split: SplitConfig{
			LargeIssueThreshold: d.LargeIssueThreshold,
			SplitThreshold:      d.SplitThreshold,
		},
		Merge: MergeConfig{
			Strategy:     app.Workflow.Merge.Strategy,
			DeleteBranch: app.Workflow.Merge.DeleteBranch,
		},
		Budget: BudgetConfig{
			PausePct:  app.UsageGuard.PausePct,
			ResumePct: app.UsageGuard.ResumePct,
			FailOpen:  app.UsageGuard.FailOpen,
		},
		Runtime: RuntimeConfig{
			Available: []string{"claude"},
			Default:   "claude",
		},
	}
}
