package orchestrator

import (
	"testing"

	"claudworker/internal/assignment"
	"claudworker/internal/resource"
)

// TestGateAccountModes — a busy (reserved) account is rejected for the default mode (so the UI can offer
// Queue / Run-in-parallel) but ACCEPTED for parallel/queue; an offline account is rejected for all
// modes; an available account and auto-select are always ok.
func TestGateAccountModes(t *testing.T) {
	res := resource.New()
	res.Register(resource.Resource{ID: "acct-free", Kind: resource.KindClaudeAccount, Name: "Free", Health: resource.HealthHealthy})
	res.Register(resource.Resource{ID: "acct-busy", Kind: resource.KindClaudeAccount, Name: "Busy", Health: resource.HealthHealthy})
	res.Register(resource.Resource{ID: "acct-off", Kind: resource.KindClaudeAccount, Name: "Off", Health: resource.HealthDown})
	// Make acct-busy reserved by another owner.
	if _, ok := res.Reserve("other-task", resource.Filter{Kind: resource.KindClaudeAccount, ID: "acct-busy"}); !ok {
		t.Fatal("could not reserve acct-busy for setup")
	}
	o := New(&Orchestrator{Store: assignment.NewMemoryStore(), Resources: res}, WithClock(steady()))

	cases := []struct {
		id, mode string
		wantOK   bool
	}{
		{"", "", true},                   // auto-select
		{"acct-free", "", true},          // available, default
		{"acct-busy", "", false},         // reserved, default → rejected (UI offers queue/parallel)
		{"acct-busy", "parallel", true},  // reserved, parallel → allowed
		{"acct-busy", "queue", true},     // reserved, queue → allowed
		{"acct-off", "", false},          // offline, default
		{"acct-off", "parallel", false},  // offline can't run in parallel
		{"acct-off", "queue", false},     // offline can't be queued (can't auth)
		{"acct-nope", "parallel", false}, // unknown
	}
	for _, c := range cases {
		ok, _, reason := o.GateAccount(c.id, c.mode)
		if ok != c.wantOK {
			t.Errorf("GateAccount(%q,%q)=%v (reason %q), want %v", c.id, c.mode, ok, reason, c.wantOK)
		}
	}
}
