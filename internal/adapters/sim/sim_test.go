package sim

import (
	"context"
	"testing"

	"claudworker/internal/assignment"
	"claudworker/internal/controlplane"
	"claudworker/internal/knowledge"
	"claudworker/internal/lease"
	"claudworker/internal/orchestrator"
	"claudworker/internal/policy"
	"claudworker/internal/resource"
	"claudworker/internal/verify"
)

// TestSimulationModeCompletesAllWork proves Simulation Mode runs the FULL loop with no external
// services and drives the whole demo backlog to Done — the regression/demo environment.
func TestSimulationModeCompletesAllWork(t *testing.T) {
	res := resource.New()
	res.Register(resource.Resource{ID: "claude-1", Kind: resource.KindClaudeAccount, Health: resource.HealthHealthy})
	store := assignment.NewMemoryStore()
	dev := &Developer{}

	o := orchestrator.New(&orchestrator.Orchestrator{
		Resources: res,
		Policy:    policy.New(policy.Config{}),
		Leases:    lease.New(lease.NewMemoryStore()),
		Knowledge: knowledge.New(knowledge.NewMemoryStore()),
		Verify:    verify.New(),
		Store:     store,
		CP:        controlplane.NewServer(controlplane.NewBus()),
		Jira:      NewJira(),
		Developer: dev,
		Verifier:  NewVerifier(),
		Merger:    Merger{},
	})

	ctx := context.Background()
	for {
		did, err := o.ProcessOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !did {
			break
		}
	}
	all, _ := store.List()
	if len(all) != len(DemoIssues()) {
		t.Fatalf("processed %d, want %d", len(all), len(DemoIssues()))
	}
	for _, a := range all {
		if a.State != assignment.StateDone {
			t.Errorf("%s = %s, want done", a.IssueKey, a.State)
		}
	}
	if dev.Calls < len(DemoIssues()) {
		t.Errorf("developer calls = %d, want >= %d", dev.Calls, len(DemoIssues()))
	}
}
