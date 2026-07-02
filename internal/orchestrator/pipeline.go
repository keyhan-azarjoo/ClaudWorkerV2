package orchestrator

import (
	"context"
	"fmt"

	"github.com/myotgo/ClaudWorkerV2/internal/assignment"
	"github.com/myotgo/ClaudWorkerV2/internal/controlplane"
	"github.com/myotgo/ClaudWorkerV2/internal/improvement"
	"github.com/myotgo/ClaudWorkerV2/internal/knowledge"
	"github.com/myotgo/ClaudWorkerV2/internal/lease"
	"github.com/myotgo/ClaudWorkerV2/internal/policy"
	"github.com/myotgo/ClaudWorkerV2/internal/resource"
	"github.com/myotgo/ClaudWorkerV2/internal/verify"
)

// runAssignment drives one claimed Assignment through the full pipeline, calling each subsystem in
// turn. Resource usage is always Policy → Resource → Lease. Every transition publishes an event.
func (o *Orchestrator) runAssignment(ctx context.Context, a *assignment.Assignment, iss Issue) error {
	owner := a.IssueKey

	// --- Acquire required leases (Policy → Resource → Lease) for a runtime resource ---
	resID, ok, reason := o.acquireRuntime(ctx, owner)
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
	dev, err := o.Developer.Develop(ctx, DevInput{Issue: iss.Key, Summary: iss.Summary, AcceptanceCriteria: iss.AcceptanceCriteria, KnowledgeContext: kctx, Runtime: runtimeName, Account: resID})
	o.emit(controlplane.EventRuntimeFinished, "runtime", map[string]any{"issue": iss.Key, "ok": err == nil && dev.OK, "changed": len(dev.ChangedFiles)})
	devStatus := "done"
	if err != nil || !dev.OK {
		devStatus = "failed"
	}
	o.recordAction(iss.Key, "develop", devStatus, fmt.Sprintf("%d file(s) changed", len(dev.ChangedFiles)))

	// --- Verification + Improvement loop (S8 + S9), stop decided by the Policy Engine (S6) ---
	a.State = assignment.StateQA
	_ = o.Store.Save(a)
	imp := o.buildImprovement(iss, kctx, runtimeName, resID)
	res, _ := imp.Run(ctx, improvement.ImprovementInput{Assignment: iss.Key, KnowledgeContext: kctx})
	o.emit(controlplane.EventPolicyDecision, "policy", map[string]any{"policy": "improvement", "decision": string(res.Status), "reason": "improvement loop terminal", "iterations": res.Progress.Iterations})

	switch res.Status {
	case improvement.StatusPassed:
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
	o.recordAction(iss.Key, "merge", "running", "")

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
	if err != nil || !merged {
		return o.fail(ctx, a, iss, "merge failed")
	}
	o.emit("MergeCompleted", "git", map[string]any{"issue": iss.Key, "branch": o.Cfg.DevBranch})
	o.recordAction(iss.Key, "merge", "done", o.Cfg.DevBranch)

	if o.Cfg.DoneStatus != "" {
		_ = o.Jira.Transition(ctx, iss.Key, o.Cfg.DoneStatus)
	}
	_ = o.Jira.Comment(ctx, iss.Key, "Merged to "+o.Cfg.DevBranch)
	return o.finish(ctx, a, iss, assignment.StateDone, "merged")
}

// acquireRuntime enforces the mandated order: Policy (budget) → Resource (reserve) → Lease (acquire).
func (o *Orchestrator) acquireRuntime(ctx context.Context, owner string) (string, bool, string) {
	// Policy: budget
	bd := o.Policy.Budget.Decide(policy.BudgetInput{UsagePct: 0, UsageKnown: true})
	if !bd.Allow {
		return "", false, bd.Reason
	}
	// Resource: reserve a runtime/account resource
	r, ok := o.Resources.Reserve(owner, resource.Filter{Kind: resource.KindClaudeAccount})
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
	o       *Orchestrator
	iss     Issue
	kctx    string
	runtime string
	account string
}

func (im *impImprover) Improve(ctx context.Context, in improvement.ImprovementInput) (improvement.Change, error) {
	dev, err := im.o.Developer.Develop(ctx, DevInput{Issue: im.iss.Key, Summary: im.iss.Summary, AcceptanceCriteria: im.iss.AcceptanceCriteria, KnowledgeContext: im.kctx, Runtime: im.runtime, Account: im.account})
	if err != nil {
		return improvement.Change{}, err
	}
	return improvement.Change{Category: improvement.CatDefect, Reason: dev.Summary, ChangedFiles: dev.ChangedFiles}, nil
}
