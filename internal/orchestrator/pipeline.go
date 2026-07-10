package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"claudworker/internal/assignment"
	"claudworker/internal/controlplane"
	"claudworker/internal/improvement"
	"claudworker/internal/knowledge"
	"claudworker/internal/lease"
	"claudworker/internal/policy"
	"claudworker/internal/resource"
	"claudworker/internal/verify"
)

// runAssignment drives one claimed Assignment through the full pipeline, calling each subsystem in
// turn. Resource usage is always Policy → Resource → Lease. Every transition publishes an event.
func (o *Orchestrator) runAssignment(ctx context.Context, a *assignment.Assignment, iss Issue, preferredAccount, operatorNote string) error {
	owner := a.IssueKey

	// Double-launch guard: never run the SAME issue concurrently (e.g. two Run clicks / a manual Run
	// racing the auto loop). The first execution holds the slot; any other returns immediately.
	o.mu.Lock()
	if o.inflight == nil {
		o.inflight = map[string]bool{}
	}
	if o.inflight[iss.Key] {
		o.mu.Unlock()
		return nil // already running — do not start a second agent on this task
	}
	o.inflight[iss.Key] = true
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		delete(o.inflight, iss.Key)
		o.mu.Unlock()
	}()

	// --- Acquire required leases (Policy → Resource → Lease) for a runtime resource ---
	resID, ok, reason := o.acquireRuntime(ctx, owner, preferredAccount)
	if !ok {
		return o.deferAssignment(ctx, a, iss, "runtime unavailable: "+reason)
	}
	defer o.releaseRuntime(ctx, owner, resID)

	// --- Select runtime (Policy owns the choice; the engine never names Claude directly) ---
	runtimeName := o.Policy.RuntimeSelection.Select(policy.Capabilities{}).Runtime
	o.emit(controlplane.EventRuntimeStarted, "runtime", map[string]any{"issue": iss.Key, "runtime": runtimeName, "resource": resID})
	o.recordAction(iss.Key, "runtime", "done", resID)

	// --- Load Knowledge Context (deterministic, zero tokens) ---
	kctx := o.knowledgeContext(iss)
	o.emit(controlplane.EventKnowledgeUpdated, "knowledge", map[string]any{"issue": iss.Key, "context_bytes": len(kctx)})
	o.recordAction(iss.Key, "knowledge", "done", fmt.Sprintf("%d bytes", len(kctx)))

	// --- Run Worker (initial develop) ---
	a.State = assignment.StateDeveloping
	_ = o.Store.Save(a)
	o.recordAction(iss.Key, "develop", "running", "")
	dev, err := o.Developer.Develop(ctx, DevInput{Issue: iss.Key, Summary: iss.Summary, AcceptanceCriteria: iss.AcceptanceCriteria, KnowledgeContext: kctx, Runtime: runtimeName, Account: resID, OperatorNote: operatorNote, Rules: o.activeRules()})
	o.emit(controlplane.EventRuntimeFinished, "runtime", map[string]any{"issue": iss.Key, "ok": err == nil && dev.OK, "changed": len(dev.ChangedFiles)})
	// Only a real execution ERROR is a failure (red X). A worker that ran fine but produced no changes
	// (nothing to do / already satisfied) is NOT a failure — that was showing a misleading red X.
	devStatus := "done"
	if err != nil {
		devStatus = "failed"
	}
	detail := fmt.Sprintf("%d file(s) changed", len(dev.ChangedFiles))
	if s := strings.TrimSpace(dev.Summary); s != "" {
		detail = s + " · " + detail
	}
	o.recordAction(iss.Key, "develop", devStatus, detail)

	// --- Verification + Improvement loop (S8 + S9), stop decided by the Policy Engine (S6) ---
	a.State = assignment.StateQA
	_ = o.Store.Save(a)
	// Track the worker's own completion signal across the initial develop + every improvement iteration.
	// A passing BUILD is not enough: if the agent itself reports ok=false ("I could not complete this"),
	// the task is NOT done — we must not merge it as success (that was marking incomplete work "done").
	lastOK := err == nil && dev.OK
	imp := o.buildImprovement(iss, kctx, runtimeName, resID, operatorNote, &lastOK)
	res, _ := imp.Run(ctx, improvement.ImprovementInput{Assignment: iss.Key, KnowledgeContext: kctx})
	o.emit(controlplane.EventPolicyDecision, "policy", map[string]any{"policy": "improvement", "decision": string(res.Status), "reason": "improvement loop terminal", "iterations": res.Progress.Iterations})

	switch res.Status {
	case improvement.StatusPassed:
		if !lastOK {
			// Worker reported the task incomplete — fail (don't merge), so the operator sees it and can Continue.
			_ = o.Jira.Comment(ctx, iss.Key, "ClaudWorker: the worker reported it could NOT complete this task (ok=false) — not merged.")
			return o.fail(ctx, a, iss, "worker reported task not complete (ok=false): "+firstLine(dev.Summary))
		}
		return o.mergeAndClose(ctx, a, iss)
	case improvement.StatusDeferred:
		return o.deferAssignment(ctx, a, iss, "policy deferred")
	case improvement.StatusEscalated:
		_ = o.Jira.Comment(ctx, iss.Key, "ClaudWorker escalated this issue for human review")
		return o.finish(ctx, a, iss, assignment.StateFailed, "escalated")
	default: // failed / exhausted
		return o.fail(ctx, a, iss, "improvement did not pass: "+string(res.Status))
	}
}

// mergeAndClose acquires the MERGE lease (Policy → Resource → Lease), merges via the Merger, updates
// Jira, and marks the Assignment done.
func (o *Orchestrator) mergeAndClose(ctx context.Context, a *assignment.Assignment, iss Issue) error {
	a.State = assignment.StateMerging
	_ = o.Store.Save(a)
	o.recordAction(iss.Key, "merge", "running", "→ "+o.Cfg.DevBranch)

	l, granted, _ := o.Leases.Acquire(lease.Request{Kind: lease.KindMerge, Resource: o.Cfg.DevBranch, Owner: iss.Key, Reason: "merge", Renewable: false})
	if !granted {
		return o.deferAssignment(ctx, a, iss, "merge lease held by another assignment")
	}
	o.emit(controlplane.EventLeaseGranted, "lease", l)
	defer func() {
		if released, _ := o.Leases.Release(lease.KindMerge, o.Cfg.DevBranch, iss.Key); released {
			o.emit(controlplane.EventLeaseExpired, "lease", map[string]any{"kind": "merge", "resource": o.Cfg.DevBranch, "owner": iss.Key})
		}
	}()

	merged, err := o.Merger.Merge(ctx, iss.Key)
	if err != nil {
		reason := "merge failed: " + firstLine(err.Error())
		o.recordAction(iss.Key, "merge", "failed", reason)
		return o.fail(ctx, a, iss, reason)
	}
	if !merged {
		reason := "merge conflict — the branch overlaps changes another agent pushed; open the task and Continue with guidance to rebase/resolve"
		o.recordAction(iss.Key, "merge", "failed", reason)
		return o.fail(ctx, a, iss, reason)
	}
	o.emit("MergeCompleted", "git", map[string]any{"issue": iss.Key, "branch": o.Cfg.DevBranch})
	o.recordAction(iss.Key, "merge", "done", "merged into "+o.Cfg.DevBranch)

	if o.Cfg.DoneStatus != "" {
		_ = o.Jira.Transition(ctx, iss.Key, o.Cfg.DoneStatus)
	}
	_ = o.Jira.Comment(ctx, iss.Key, "Merged to "+o.Cfg.DevBranch)
	return o.finish(ctx, a, iss, assignment.StateDone, "merged")
}

// acquireRuntime enforces the mandated order: Policy (budget) → Resource (reserve) → Lease (acquire).
// When preferred is set (operator picked an account), it reserves THAT account; if it is unavailable it
// falls back to any available account so the task still runs.
func (o *Orchestrator) acquireRuntime(ctx context.Context, owner, preferred string) (string, bool, string) {
	// Policy: budget
	bd := o.Policy.Budget.Decide(policy.BudgetInput{UsagePct: 0, UsageKnown: true})
	if !bd.Allow {
		return "", false, bd.Reason
	}
	// Resource: reserve a runtime/account resource (the picked one first, else any available).
	var (
		r  *resource.Resource
		ok bool
	)
	if preferred != "" {
		r, ok = o.Resources.Reserve(owner, resource.Filter{Kind: resource.KindClaudeAccount, ID: preferred})
	}
	if !ok {
		r, ok = o.Resources.Reserve(owner, resource.Filter{Kind: resource.KindClaudeAccount})
	}
	if !ok {
		return "", false, "no available runtime resource"
	}
	// Lease: durable ownership of the reserved resource
	if _, granted, _ := o.Leases.Acquire(lease.Request{Kind: lease.KindResource, Resource: r.ID, Owner: owner, Reason: "runtime", Renewable: true}); !granted {
		o.Resources.Release(r.ID)
		return "", false, "resource lease held by another owner"
	}
	o.emit(controlplane.EventLeaseGranted, "lease", map[string]any{"kind": "resource", "resource": r.ID, "owner": owner})
	return r.ID, true, ""
}

// releaseRuntime releases the resource lease and the reservation (in that order — lease first).
func (o *Orchestrator) releaseRuntime(ctx context.Context, owner, resID string) {
	if released, _ := o.Leases.Release(lease.KindResource, resID, owner); released {
		o.emit(controlplane.EventLeaseExpired, "lease", map[string]any{"kind": "resource", "resource": resID, "owner": owner})
	}
	o.Resources.RecordUse(resID)
	o.Resources.Release(resID)
	o.emit(controlplane.EventRuntimeFinished, "resource", map[string]any{"resource": resID, "owner": owner, "released": true})
}

// knowledgeContext loads the deterministic Knowledge-Brain slice for the task (zero tokens).
func (o *Orchestrator) knowledgeContext(iss Issue) string {
	entries, err := o.Knowledge.SelectContext(knowledge.Selector{Keywords: keywords(iss), MaxEntries: 8, MaxBytes: 4096})
	if err != nil {
		return ""
	}
	return knowledge.RenderContext(entries)
}

// finish persists a terminal state, releases the issue lease, and publishes completion.
func (o *Orchestrator) finish(ctx context.Context, a *assignment.Assignment, iss Issue, state assignment.State, reason string) error {
	a.State = state
	if err := o.Store.Save(a); err != nil {
		return err
	}
	if released, _ := o.Leases.Release(lease.KindIssue, iss.Key, iss.Key); released {
		o.emit(controlplane.EventLeaseExpired, "lease", map[string]any{"kind": "issue", "resource": iss.Key, "owner": iss.Key})
	}
	// Clean the Git workspace on ANY terminal state (done or failed) — safety: no leaked worktrees or
	// branches. Optional/nil-safe (simulation leaves Cleaner unset).
	if o.Cleaner != nil {
		if err := o.Cleaner.Cleanup(ctx, iss.Key); err == nil {
			o.emit("WorkspaceCleaned", "git", map[string]any{"issue": iss.Key, "state": string(state)})
		}
	}
	o.count("processed")
	o.count(string(state))
	o.emit(controlplane.EventAssignmentCompleted, "assignment", map[string]any{"issue": iss.Key, "state": string(state), "reason": reason})
	finStatus := "done"
	if state != assignment.StateDone {
		finStatus = "failed"
	}
	o.recordAction(iss.Key, "finish", finStatus, reason)
	return nil
}

func (o *Orchestrator) fail(ctx context.Context, a *assignment.Assignment, iss Issue, reason string) error {
	_ = o.Jira.Comment(ctx, iss.Key, "ClaudWorker could not complete this issue: "+reason)
	return o.finish(ctx, a, iss, assignment.StateFailed, reason)
}

// deferAssignment leaves the Assignment non-terminal (a later pass retries) but releases the runtime
// so the resource can be reclaimed. The issue lease is retained (still ours). No human step needed.
func (o *Orchestrator) deferAssignment(ctx context.Context, a *assignment.Assignment, iss Issue, reason string) error {
	a.State = assignment.StateClaimed
	_ = o.Store.Save(a)
	o.count("deferred")
	o.emit("AssignmentDeferred", "orchestrator", map[string]any{"issue": iss.Key, "reason": reason})
	return nil
}

// --- Improvement-loop adapters: bind the issue to the S8/S9 ports (no duplicated logic) ---

type impVerifier struct {
	o     *Orchestrator
	issue string
}

func (v *impVerifier) Verify(ctx context.Context) ([]verify.Result, error) {
	v.o.emit(controlplane.EventVerificationStarted, "verify", map[string]any{"issue": v.issue})
	v.o.recordAction(v.issue, "verify", "running", "")
	res, err := v.o.Verifier.Verify(ctx, v.issue)
	v.o.emit(controlplane.EventVerificationFinished, "verify", map[string]any{"issue": v.issue, "outcome": string(verify.Aggregate(res)), "results": len(res)})
	v.o.recordAction(v.issue, "verify", "done", string(verify.Aggregate(res)))
	return res, err
}

type impImprover struct {
	o            *Orchestrator
	iss          Issue
	kctx         string
	runtime      string
	account      string
	operatorNote string
	lastOK       *bool // updated with each iteration's worker completion signal (nil-safe)
}

func (im *impImprover) Improve(ctx context.Context, in improvement.ImprovementInput) (improvement.Change, error) {
	dev, err := im.o.Developer.Develop(ctx, DevInput{Issue: im.iss.Key, Summary: im.iss.Summary, AcceptanceCriteria: im.iss.AcceptanceCriteria, KnowledgeContext: im.kctx, Runtime: im.runtime, Account: im.account, OperatorNote: im.operatorNote, Rules: im.o.activeRules()})
	if err != nil {
		return improvement.Change{}, err
	}
	if im.lastOK != nil {
		*im.lastOK = dev.OK
	}
	return improvement.Change{Category: improvement.CatDefect, Reason: dev.Summary, ChangedFiles: dev.ChangedFiles}, nil
}

// firstLine returns the first non-empty, trimmed line of s (for compact error reasons in the timeline).
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			return t
		}
	}
	return strings.TrimSpace(s)
}
