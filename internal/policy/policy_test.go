package policy

import (
	"testing"

	"claudworker/internal/assignment"
	"claudworker/internal/config"
)

// RetryPolicy must satisfy the Assignment Engine's port so the engine can ASK it (S6).
var _ assignment.RetryDecider = RetryPolicy{}

func TestRetryPolicy(t *testing.T) {
	p := RetryPolicy{cfg: RetryConfig{MaxAttempts: 3}}
	if d := p.Decide(1, true); !d.Retry {
		t.Errorf("attempt 1/3 should retry: %+v", d)
	}
	if d := p.Decide(3, true); d.Retry {
		t.Errorf("attempt 3/3 should not retry: %+v", d)
	}
	if d := p.Decide(1, false); d.Retry {
		t.Errorf("non-transient must not retry: %+v", d)
	}
	if p.ShouldRetry(2) != true || p.ShouldRetry(3) != false {
		t.Error("ShouldRetry cap wrong")
	}
	// disabling flaky retry blocks all retries
	pd := RetryPolicy{cfg: RetryConfig{MaxAttempts: 3, DisableFlakyRetry: true}}
	if pd.Decide(1, true).Retry {
		t.Error("DisableFlakyRetry should block retry")
	}
}

func TestRuntimeSelectionPolicy(t *testing.T) {
	p := RuntimeSelectionPolicy{cfg: RuntimeConfig{
		Available: []string{"claude", "local"},
		Order:     []string{"local", "claude"},
		Default:   "claude",
		Supports:  map[string][]string{"claude": {"vision", "long-context"}, "local": {"long-context"}},
	}}
	// no caps → first in order
	if d := p.Select(Capabilities{}); d.Runtime != "local" {
		t.Errorf("no-caps select = %q, want local", d.Runtime)
	}
	// vision required → only claude supports it
	if d := p.Select(Capabilities{Vision: true}); d.Runtime != "claude" {
		t.Errorf("vision select = %q, want claude", d.Runtime)
	}
	// explicit preference honoured when capable
	if d := p.Select(Capabilities{Preferred: "claude"}); d.Runtime != "claude" {
		t.Errorf("preferred select = %q, want claude", d.Runtime)
	}
	// unsatisfiable capability → empty
	p2 := RuntimeSelectionPolicy{cfg: RuntimeConfig{Available: []string{"local"}, Supports: map[string][]string{"local": {}}}}
	if d := p2.Select(Capabilities{Vision: true}); d.Runtime != "" {
		t.Errorf("unsatisfiable select = %q, want empty", d.Runtime)
	}
}

func TestMergePolicy(t *testing.T) {
	p := MergePolicy{cfg: MergeConfig{Strategy: "no-ff", DeleteBranch: true, RequireGatesPass: true}}
	if d := p.Decide(MergeInput{GatesPassed: true}); !d.Merge || d.Strategy != "no-ff" || !d.DeleteBranch {
		t.Errorf("clean merge = %+v", d)
	}
	if d := p.Decide(MergeInput{Conflicts: true, GatesPassed: true}); d.Merge {
		t.Error("must not merge with conflicts")
	}
	if d := p.Decide(MergeInput{GatesPassed: false}); d.Merge {
		t.Error("must not merge when gates fail")
	}
}

func TestEscalationPolicy(t *testing.T) {
	p := EscalationPolicy{cfg: EscalationConfig{MaxAttempts: 3, AbandonedDays: 30}}
	if !p.Decide(EscalationInput{Blocked: true}).Escalate {
		t.Error("blocked should escalate")
	}
	if !p.Decide(EscalationInput{Failed: true, Attempts: 3}).Escalate {
		t.Error("failed at cap should escalate")
	}
	if !p.Decide(EscalationInput{AgeDays: 40}).Escalate {
		t.Error("stale should escalate")
	}
	if p.Decide(EscalationInput{Attempts: 1}).Escalate {
		t.Error("healthy should not escalate")
	}
}

func TestSplitPolicy(t *testing.T) {
	p := SplitPolicy{cfg: SplitConfig{LargeIssueThreshold: 8, SplitThreshold: 5}}
	if d := p.Decide(SplitInput{EstimatedUnits: 6}); !d.Split {
		t.Errorf("6 >= 5 should split: %+v", d)
	}
	if d := p.Decide(SplitInput{EstimatedUnits: 3}); d.Split {
		t.Errorf("3 < 5 should not split: %+v", d)
	}
	if d := p.Decide(SplitInput{EstimatedUnits: 9}); !d.Large {
		t.Error("9 >= 8 should flag large")
	}
}

func TestDeferPolicy(t *testing.T) {
	var p DeferPolicy
	if d := p.Decide(DeferInput{MissingDependency: true}); !d.Defer || d.FollowupKind != "dependency" {
		t.Errorf("missing dep defer = %+v", d)
	}
	if d := p.Decide(DeferInput{Blocked: true, Kind: "waiting-review"}); !d.Defer || d.FollowupKind != "waiting-review" {
		t.Errorf("blocked defer = %+v", d)
	}
	if p.Decide(DeferInput{}).Defer {
		t.Error("no blocker should not defer")
	}
}

func TestBudgetPolicy(t *testing.T) {
	p := BudgetPolicy{cfg: BudgetConfig{PausePct: 95, ResumePct: 80, DailyTokenLimit: 1000, Accounts: []string{"acct-a"}}}
	if d := p.Decide(BudgetInput{UsagePct: 50, UsageKnown: true}); !d.Allow || d.Account != "acct-a" {
		t.Errorf("under budget = %+v", d)
	}
	if d := p.Decide(BudgetInput{UsagePct: 96, UsageKnown: true}); d.Allow || !d.Pause {
		t.Errorf("over pause pct = %+v", d)
	}
	if d := p.Decide(BudgetInput{UsagePct: 10, TokensUsedToday: 1000, UsageKnown: true}); d.Allow {
		t.Error("daily token limit should block")
	}
	// hysteresis: in cooldown, usage above resume stays paused
	if d := p.Decide(BudgetInput{UsagePct: 85, UsageKnown: true, InCooldown: true}); d.Allow || !d.Cooldown {
		t.Errorf("cooldown hysteresis = %+v", d)
	}
	// unknown usage: fail-closed by default
	if d := p.Decide(BudgetInput{UsageKnown: false}); d.Allow {
		t.Error("unknown usage with FailOpen=false must not allow")
	}
	// fail-open
	po := BudgetPolicy{cfg: BudgetConfig{PausePct: 95, FailOpen: true}}
	if d := po.Decide(BudgetInput{UsageKnown: false}); !d.Allow {
		t.Error("FailOpen=true should allow when usage unknown")
	}
}

func TestApprovalPolicy(t *testing.T) {
	var p ApprovalPolicy
	if d := p.Decide(ApprovalInput{Automation: AutomationEnabled}); !d.Proceed {
		t.Error("enabled should proceed")
	}
	if d := p.Decide(ApprovalInput{Automation: AutomationDisabled}); d.Proceed || d.RequireApproval {
		t.Error("disabled should neither proceed nor need approval")
	}
	if d := p.Decide(ApprovalInput{Automation: AutomationNeedsReview}); !d.RequireApproval {
		t.Error("needs review should require approval")
	}
	if d := p.Decide(ApprovalInput{Automation: "???"}); !d.RequireApproval {
		t.Error("unknown should require approval (safe default)")
	}
}

func TestFailurePolicyComposes(t *testing.T) {
	e := New(Config{Retry: RetryConfig{MaxAttempts: 2}, Escalation: EscalationConfig{MaxAttempts: 2}})
	if d := e.Failure.Classify(FailureInput{Blocked: true}); d.Action != ActionDefer {
		t.Errorf("blocked → %s, want defer", d.Action)
	}
	if d := e.Failure.Classify(FailureInput{Attempts: 1, Transient: true}); d.Action != ActionRetry {
		t.Errorf("attempt 1/2 → %s, want retry", d.Action)
	}
	if d := e.Failure.Classify(FailureInput{Attempts: 2, Transient: true}); d.Action != ActionEscalate {
		t.Errorf("attempt 2/2 → %s, want escalate (exhausted → human)", d.Action)
	}
	// non-transient failure BELOW the escalation cap: no retry, not yet escalation-worthy → fail.
	if d := e.Failure.Classify(FailureInput{Attempts: 1, Transient: false}); d.Action != ActionFail {
		t.Errorf("non-transient early failure → %s, want fail", d.Action)
	}
}

func TestDeterminism(t *testing.T) {
	e := New(Config{})
	// same inputs → same outputs, repeatedly
	for i := 0; i < 50; i++ {
		if e.Retry.Decide(1, true) != (RetryDecision{Retry: true, Reason: "attempt 1 < max 3"}) {
			t.Fatal("retry non-deterministic")
		}
		if e.Budget.Decide(BudgetInput{UsagePct: 50, UsageKnown: true}).Allow != true {
			t.Fatal("budget non-deterministic")
		}
	}
}

func TestFromConfigMapping(t *testing.T) {
	app := config.Config{}
	app.Defaults.RetryLimits.MaxAttempts = 4
	app.Defaults.AbandonedDays = 30
	app.Defaults.LargeIssueThreshold = 8
	app.Defaults.SplitThreshold = 5
	app.Workflow.Merge.Strategy = "no-ff"
	app.Workflow.Merge.DeleteBranch = true
	app.UsageGuard.PausePct = 95
	app.UsageGuard.ResumePct = 80

	e := New(FromConfig(app))
	if !e.Retry.ShouldRetry(3) || e.Retry.ShouldRetry(4) {
		t.Error("retry cap not mapped from config (want max 4)")
	}
	if e.Split.Decide(SplitInput{EstimatedUnits: 5}).Split != true {
		t.Error("split threshold not mapped")
	}
	if e.Merge.Decide(MergeInput{GatesPassed: true}).Strategy != "no-ff" {
		t.Error("merge strategy not mapped")
	}
	if e.Budget.Decide(BudgetInput{UsagePct: 96, UsageKnown: true}).Allow {
		t.Error("budget pause pct not mapped")
	}
}

func TestNewAppliesDefaults(t *testing.T) {
	e := New(Config{}) // all zero
	if !e.Retry.ShouldRetry(2) || e.Retry.ShouldRetry(3) {
		t.Error("default MaxAttempts should be 3")
	}
	if e.Budget.Decide(BudgetInput{UsagePct: 95, UsageKnown: true}).Allow {
		t.Error("default PausePct should be 95")
	}
	if e.RuntimeSelection.Select(Capabilities{}).Runtime != "claude" {
		t.Error("default runtime should be claude")
	}
}
