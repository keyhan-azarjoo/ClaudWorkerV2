package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/myotgo/ClaudWorkerV2/internal/resource"
)

// setAccountPaused pauses/resumes one account resource by id (body: {"id":"..."}).
func (o *Orchestrator) setAccountPaused(body []byte, paused bool) (any, error) {
	var req struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &req)
	req.ID = strings.TrimSpace(req.ID)
	if req.ID == "" {
		return nil, fmt.Errorf("account id required")
	}
	found := o.Resources.SetPaused(req.ID, paused)
	if !found {
		return nil, fmt.Errorf("unknown account %q", req.ID)
	}
	event := "AccountResumed"
	if paused {
		event = "AccountPaused"
	}
	o.emit(event, "resource", map[string]any{"id": req.ID, "paused": paused})
	return map[string]any{"id": req.ID, "paused": paused}, nil
}

// RegisterControlPlane registers every query, command, status provider and metric so the Operations
// Console becomes fully live. Each handler simply delegates to a subsystem — the Control Plane (and
// this registration) hold no business logic. Events are published throughout the loop via emit().
func (o *Orchestrator) RegisterControlPlane() {
	if o.CP == nil {
		return
	}

	// --- Queries (read models straight from the subsystems) ---
	o.CP.Query("assignments.list", func(context.Context, url.Values) (any, error) {
		return o.Store.List()
	})
	o.CP.Query("leases.active", func(context.Context, url.Values) (any, error) {
		return o.Leases.Active()
	})
	o.CP.Query("resources.snapshot", func(context.Context, url.Values) (any, error) {
		return o.Resources.Snapshot(), nil
	})
	o.CP.Query("accounts.list", func(context.Context, url.Values) (any, error) {
		return o.Resources.List(resource.Filter{Kind: resource.KindClaudeAccount}), nil
	})
	o.CP.Query("runtimes.list", func(context.Context, url.Values) (any, error) {
		return o.Resources.List(resource.Filter{Kind: resource.KindLocalRuntime}), nil
	})
	o.CP.Query("knowledge.list", func(context.Context, url.Values) (any, error) {
		return o.Knowledge.List()
	})
	o.CP.Query("policies.decisions", func(context.Context, url.Values) (any, error) {
		o.mu.Lock()
		defer o.mu.Unlock()
		out := make([]map[string]any, len(o.decisions))
		copy(out, o.decisions)
		return out, nil
	})

	// --- Commands (actions that delegate to subsystems / the loop) ---
	// orchestrator.start / .stop drive the manual "Start Working" / "Stop" buttons: start makes the
	// loop claim + process the Jira queue until empty, stop returns it to attached-but-idle.
	o.CP.Command("orchestrator.start", func(context.Context, []byte) (any, error) {
		o.SetActive(true)
		return map[string]any{"active": true}, nil
	})
	o.CP.Command("orchestrator.stop", func(context.Context, []byte) (any, error) {
		o.SetActive(false)
		return map[string]any{"active": false}, nil
	})
	o.CP.Command("orchestrator.tick", func(ctx context.Context, _ []byte) (any, error) {
		did, err := o.ProcessOnce(ctx)
		return map[string]any{"processed": did}, err
	})
	// orchestrator.run {"issue":"SCRUM-123"} — operator picks ONE ticket to run now (manual select),
	// regardless of the ready label or idle state. Runs in the background; watch via events/assignments.
	o.CP.Command("orchestrator.run", func(_ context.Context, body []byte) (any, error) {
		var req struct {
			Issue string `json:"issue"`
		}
		_ = json.Unmarshal(body, &req)
		req.Issue = strings.TrimSpace(req.Issue)
		if req.Issue == "" {
			return nil, fmt.Errorf("issue key required")
		}
		go func() {
			if _, err := o.RunIssue(context.Background(), req.Issue); err != nil {
				o.log().Error("orchestrator", "op", "manual-run", "issue", req.Issue, "error", err.Error())
				o.emit("AssignmentFailed", "orchestrator", map[string]any{"issue": req.Issue, "error": err.Error()})
			}
		}()
		return map[string]any{"issue": req.Issue, "started": true}, nil
	})
	// accounts.pause / accounts.resume {"id":"acct-myotgo"} — per-account operator control (V1 parity).
	o.CP.Command("accounts.pause", func(_ context.Context, body []byte) (any, error) {
		return o.setAccountPaused(body, true)
	})
	o.CP.Command("accounts.resume", func(_ context.Context, body []byte) (any, error) {
		return o.setAccountPaused(body, false)
	})
	o.CP.Command("leases.reap", func(context.Context, []byte) (any, error) {
		n, err := o.Leases.Reap()
		return map[string]any{"reaped": n}, err
	})

	// --- Status ---
	o.CP.Status("orchestrator", func(context.Context) (any, error) {
		o.mu.Lock()
		defer o.mu.Unlock()
		state := "idle"
		if o.active {
			state = "working"
		}
		return map[string]any{"running": o.running, "active": o.active, "state": state, "last_issue": o.lastIssue}, nil
	})

	// --- Metrics ---
	o.CP.Metric("counters", func(context.Context) (any, error) {
		o.mu.Lock()
		defer o.mu.Unlock()
		out := make(map[string]int, len(o.counters))
		for k, v := range o.counters {
			out[k] = v
		}
		return out, nil
	})
	o.CP.Metric("leases", func(context.Context) (any, error) {
		act, err := o.Leases.Active()
		if err != nil {
			return nil, err
		}
		byKind := map[string]int{}
		for _, l := range act {
			byKind[string(l.Kind)]++
		}
		return map[string]any{"active": len(act), "by_kind": byKind}, nil
	})
	o.CP.Metric("resources", func(context.Context) (any, error) {
		snap := o.Resources.Snapshot()
		byAvail := map[string]int{}
		for _, s := range snap {
			byAvail[string(s.Availability)]++
		}
		return map[string]any{"total": len(snap), "by_availability": byAvail}, nil
	})
}
