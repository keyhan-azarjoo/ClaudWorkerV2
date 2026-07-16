package orchestrator

import (
	"testing"

	"claudworker/internal/assignment"
	"claudworker/internal/resource"
)

// TestAccountUsable — an operator-picked account is accepted only when it can actually take work; a
// logged-out/offline or unknown account is rejected (so Run/Continue never silently switches accounts).
func TestAccountUsable(t *testing.T) {
	res := resource.New()
	res.Register(resource.Resource{ID: "acct-good", Kind: resource.KindClaudeAccount, Name: "Good", Health: resource.HealthHealthy})
	res.Register(resource.Resource{ID: "acct-off", Kind: resource.KindClaudeAccount, Name: "Off", Health: resource.HealthDown})
	o := New(&Orchestrator{Store: assignment.NewMemoryStore(), Resources: res}, WithClock(steady()))

	if ok, _ := o.AccountUsable(""); !ok {
		t.Fatal("empty account (auto-select) should be usable")
	}
	if ok, _ := o.AccountUsable("acct-good"); !ok {
		t.Fatal("healthy available account should be usable")
	}
	if ok, reason := o.AccountUsable("acct-off"); ok {
		t.Fatalf("offline account must be rejected; got usable (reason=%q)", reason)
	}
	if ok, reason := o.AccountUsable("acct-nope"); ok {
		t.Fatalf("unknown account must be rejected; got usable (reason=%q)", reason)
	}
}
