// Package jiraadapter is the REAL Jira adapter (Phase 2, integration #1). It implements the
// Orchestrator's Jira port over the deterministic internal/jira REST client — search, claim
// (transition), comments, labels, the Automation field, and attachments — so the serve loop can watch
// a real Jira project. It adds no business logic; it maps port calls to client calls.
package jiraadapter

import (
	"context"

	jira "github.com/myotgo/ClaudWorkerV2/internal/jira"
	"github.com/myotgo/ClaudWorkerV2/internal/orchestrator"
)

// Adapter wraps a *jira.Client. It satisfies orchestrator.Jira.
type Adapter struct {
	c          *jira.Client
	workJQL    string
	maxResults int
}

// Option configures the Adapter.
type Option func(*Adapter)

// WithMaxResults caps how many eligible issues are fetched per poll (default 25).
func WithMaxResults(n int) Option {
	return func(a *Adapter) {
		if n > 0 {
			a.maxResults = n
		}
	}
}

// New builds the adapter from a Jira client and the work-queue JQL.
func New(c *jira.Client, workJQL string, opts ...Option) *Adapter {
	a := &Adapter{c: c, workJQL: workJQL, maxResults: 25}
	for _, o := range opts {
		o(a)
	}
	return a
}

// Eligible runs the work-queue JQL and returns each issue enriched with its acceptance criteria (what
// the worker needs). Real search over the Jira REST API.
func (a *Adapter) Eligible(ctx context.Context) ([]orchestrator.Issue, error) {
	res, err := a.c.Search(ctx, a.workJQL, nil, a.maxResults)
	if err != nil {
		return nil, err
	}
	out := make([]orchestrator.Issue, 0, len(res.Issues))
	for _, iss := range res.Issues {
		ac, _ := a.c.AcceptanceCriteria(ctx, iss.Key) // best-effort; empty AC is not fatal
		out = append(out, orchestrator.Issue{Key: iss.Key, Summary: iss.Fields.Summary, AcceptanceCriteria: ac})
	}
	return out, nil
}

// Get fetches one issue (used on recovery to re-hydrate an in-flight assignment).
func (a *Adapter) Get(ctx context.Context, key string) (orchestrator.Issue, error) {
	iss, err := a.c.GetIssue(ctx, key)
	if err != nil {
		return orchestrator.Issue{}, err
	}
	ac, _ := a.c.AcceptanceCriteria(ctx, key)
	return orchestrator.Issue{Key: iss.Key, Summary: iss.Fields.Summary, AcceptanceCriteria: ac}, nil
}

// Transition moves an issue to a named status (claim → In Progress, close → Done).
func (a *Adapter) Transition(ctx context.Context, key, to string) error {
	_, err := a.c.TransitionTo(ctx, key, to)
	return err
}

// Comment posts a comment.
func (a *Adapter) Comment(ctx context.Context, key, text string) error {
	_, err := a.c.AddComment(ctx, key, text)
	return err
}

// --- Additional real Jira capabilities (labels / Automation / attachments), available to the serve
// wiring and the Control Plane. Thin wrappers; no logic. ---

// AddLabels adds labels to an issue (e.g. a "claudworker" claim marker).
func (a *Adapter) AddLabels(ctx context.Context, key string, labels ...string) error {
	return a.c.AddLabels(ctx, key, labels...)
}

// RemoveLabels removes labels from an issue.
func (a *Adapter) RemoveLabels(ctx context.Context, key string, labels ...string) error {
	return a.c.RemoveLabels(ctx, key, labels...)
}

// Automation reads the Automation single-select gate for an issue.
func (a *Adapter) Automation(ctx context.Context, key string) (string, error) {
	v, err := a.c.GetAutomation(ctx, key)
	return string(v), err
}

// Attachment is a lightweight view of a Jira attachment for the Control Plane.
type Attachment struct {
	Filename string `json:"filename"`
	URL      string `json:"url"`
}

// Attachments lists an issue's attachments.
func (a *Adapter) Attachments(ctx context.Context, key string) ([]Attachment, error) {
	atts, err := a.c.Attachments(ctx, key)
	if err != nil {
		return nil, err
	}
	out := make([]Attachment, len(atts))
	for i, at := range atts {
		out[i] = Attachment{Filename: at.Filename, URL: at.Content}
	}
	return out, nil
}

// QueueItem is an eligible issue plus its Automation gate — the Control Plane "jira.queue" read model.
type QueueItem struct {
	Key        string `json:"key"`
	Summary    string `json:"summary"`
	Status     string `json:"status"`
	Automation string `json:"automation"`
}

// Queue returns the eligible issues with their status + Automation value for the console's Jira page.
func (a *Adapter) Queue(ctx context.Context) ([]QueueItem, error) {
	res, err := a.c.Search(ctx, a.workJQL, []string{"summary", "status"}, a.maxResults)
	if err != nil {
		return nil, err
	}
	out := make([]QueueItem, 0, len(res.Issues))
	for _, iss := range res.Issues {
		auto, _ := a.c.GetAutomation(ctx, iss.Key)
		out = append(out, QueueItem{Key: iss.Key, Summary: iss.Fields.Summary, Status: iss.Fields.Status.Name, Automation: string(auto)})
	}
	return out, nil
}

// compile-time proof the adapter satisfies the port.
var _ orchestrator.Jira = (*Adapter)(nil)
