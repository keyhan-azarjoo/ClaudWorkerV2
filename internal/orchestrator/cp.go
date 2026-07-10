package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"claudworker/internal/assignment"
	"claudworker/internal/resource"
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
	// task.stream?issue=SCRUM-123 — the live agent activity (thinking/doing/responses) for one task,
	// so a box can expand into the full transcript.
	o.CP.Query("task.stream", func(_ context.Context, q url.Values) (any, error) {
		issue := q.Get("issue")
		tin, tout := o.TaskTokens(issue)
		return map[string]any{"issue": issue, "lines": o.TaskStream(issue), "tokens_in": tin, "tokens_out": tout}, nil
	})
	// tasks.activity — per-task action timeline for the dashboard task boxes (newest task first). The
	// live assignment State is read from the Store (the log records the ordered actions, not the state).
	o.CP.Query("tasks.activity", func(context.Context, url.Values) (any, error) {
		o.mu.Lock()
		acts := make([]*TaskActivity, 0, len(o.taskLog))
		for _, t := range o.taskLog {
			cp := *t
			cp.Actions = append([]TaskAction(nil), t.Actions...)
			acts = append(acts, &cp)
		}
		o.mu.Unlock()
		for _, t := range acts {
			if a, ok, _ := o.Store.Load(t.Issue); ok {
				t.State = string(a.State)
			}
		}
		sort.Slice(acts, func(i, j int) bool { return acts[i].StartedAt.After(acts[j].StartedAt) })
		return acts, nil
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
			Issue   string `json:"issue"`
			Account string `json:"account"` // optional: run on THIS account (else auto-select)
		}
		_ = json.Unmarshal(body, &req)
		req.Issue = strings.TrimSpace(req.Issue)
		req.Account = strings.TrimSpace(req.Account)
		if req.Issue == "" {
			return nil, fmt.Errorf("issue key required")
		}
		// Project gate (synchronous so the UI sees it): a deactivated project can't be run.
		if o.WorkAllowed != nil {
			if ok, reason := o.WorkAllowed(); !ok {
				return nil, fmt.Errorf("project is deactivated: %s", reason)
			}
		}
		// Reject a double-run SYNCHRONOUSLY so the UI sees it: never run a ticket that is already
		// running, or re-run one that is already finished. (Belt to the in-flight guard in the loop.)
		if a, exists, _ := o.Store.Load(req.Issue); exists {
			if a.State.Terminal() {
				return nil, fmt.Errorf("%s is already %s — not running again", req.Issue, a.State)
			}
			o.mu.Lock()
			running := o.inflight[req.Issue]
			o.mu.Unlock()
			if running {
				return nil, fmt.Errorf("%s is already running", req.Issue)
			}
		}
		go func() {
			if _, err := o.RunIssue(context.Background(), req.Issue, req.Account, ""); err != nil {
				o.log().Error("orchestrator", "op", "manual-run", "issue", req.Issue, "error", err.Error())
				o.emit("AssignmentFailed", "orchestrator", map[string]any{"issue": req.Issue, "error": err.Error()})
			}
		}()
		return map[string]any{"issue": req.Issue, "account": req.Account, "started": true}, nil
	})
	// orchestrator.continue {"issue":"SCRUM-123"} — resume/retry a task that errored (e.g. a transient
	// rate limit / API error). A FAILED task is reset so it runs again on a fresh (non-cooled) account;
	// a stuck non-terminal task is resumed. A DONE task is left alone.
	o.CP.Command("orchestrator.continue", func(_ context.Context, body []byte) (any, error) {
		var req struct {
			Issue   string `json:"issue"`
			Account string `json:"account"`
			Message string `json:"message"` // optional operator guidance sent to the agent on resume
		}
		_ = json.Unmarshal(body, &req)
		req.Issue = strings.TrimSpace(req.Issue)
		req.Account = strings.TrimSpace(req.Account)
		req.Message = strings.TrimSpace(req.Message)
		if req.Issue == "" {
			return nil, fmt.Errorf("issue key required")
		}
		if o.WorkAllowed != nil {
			if ok, reason := o.WorkAllowed(); !ok {
				return nil, fmt.Errorf("project is deactivated: %s", reason)
			}
		}
		o.mu.Lock()
		running := o.inflight[req.Issue]
		o.mu.Unlock()
		if running {
			return nil, fmt.Errorf("%s is already running", req.Issue)
		}
		if a, exists, _ := o.Store.Load(req.Issue); exists && a.State.Terminal() {
			// Continue is an EXPLICIT operator request to run AGAIN — reset ANY terminal state (done or
			// failed) so it re-runs. Common case: the run stopped on a usage/rate limit, or merged empty,
			// and the operator wants to continue it (often on a different account).
			a.State = assignment.StateClaimed
			_ = o.Store.Save(a)
		}
		if req.Message != "" {
			o.AppendTaskLog(req.Issue, "🧑 operator: "+req.Message)
		}
		go func() {
			if _, err := o.RunIssue(context.Background(), req.Issue, req.Account, req.Message); err != nil {
				o.log().Error("orchestrator", "op", "continue", "issue", req.Issue, "error", err.Error())
				o.emit("AssignmentFailed", "orchestrator", map[string]any{"issue": req.Issue, "error": err.Error()})
			}
		}()
		return map[string]any{"issue": req.Issue, "continued": true}, nil
	})
	// accounts.pause / accounts.resume {"id":"acct-main"} — per-account operator control (V1 parity).
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
