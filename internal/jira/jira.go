// Package jira is the deterministic Jira toolbelt (docs/08_Jira.md).
//
// Jira is the single source of truth for WORK (P2). Every future worker reads/writes Jira THROUGH
// this client — a worker never calls the Jira REST API itself (Law 5/6/18). All methods return typed
// results and machine-readable errors; nothing returns text intended for AI parsing. The client holds
// no hidden global state, has no side effects beyond the documented API call, emits structured logs
// (op, duration, result, error, affected resource), and NEVER logs the auth token.
package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a deterministic Jira Cloud REST v3 client.
type Client struct {
	base  string
	email string
	token string // never logged
	http  *http.Client
	log   *slog.Logger

	// automationField is the custom field id for the Automation eligibility field
	// (single-select: Enabled/Disabled/Manual Only/Needs Review — owner decision 1). Optional.
	automationField string
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient overrides the HTTP client (e.g. for tests / timeouts).
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// WithLogger sets the structured logger.
func WithLogger(l *slog.Logger) Option { return func(c *Client) { c.log = l } }

// WithAutomationField sets the custom field id used for the Automation field.
func WithAutomationField(id string) Option { return func(c *Client) { c.automationField = id } }

// New builds a Client. baseURL is e.g. https://site.atlassian.net; email+token are Jira Cloud basic
// auth (the token is a resolved secret VALUE and is never logged).
func New(baseURL, email, token string, opts ...Option) *Client {
	c := &Client{
		base:  strings.TrimRight(baseURL, "/"),
		email: email,
		token: token,
		http:  &http.Client{Timeout: 30 * time.Second},
		log:   slog.New(discardHandler{}),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Error is a machine-readable Jira API failure.
type Error struct {
	Op         string   `json:"op"`
	StatusCode int      `json:"status_code"`
	Messages   []string `json:"messages"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("jira %s: http %d: %s", e.Op, e.StatusCode, strings.Join(e.Messages, "; "))
}

// do performs an authenticated request, decoding a 2xx JSON body into out (if non-nil). It logs one
// structured line without the token.
func (c *Client) do(ctx context.Context, op, method, path string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("jira %s: marshal: %w", op, err)
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reqBody)
	if err != nil {
		return fmt.Errorf("jira %s: request: %w", op, err)
	}
	req.SetBasicAuth(c.email, c.token) // token travels only in the header, never logged
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	start := time.Now()
	resp, err := c.http.Do(req)
	dur := time.Since(start)
	if err != nil {
		c.log.Error("jira", "op", op, "method", method, "path", redactPath(path),
			"duration_ms", dur.Milliseconds(), "result", "error", "error", err.Error())
		return &Error{Op: op, StatusCode: 0, Messages: []string{err.Error()}}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msgs := parseErrorMessages(raw)
		c.log.Error("jira", "op", op, "method", method, "path", redactPath(path),
			"duration_ms", dur.Milliseconds(), "result", "error", "status", resp.StatusCode)
		return &Error{Op: op, StatusCode: resp.StatusCode, Messages: msgs}
	}
	c.log.Info("jira", "op", op, "method", method, "path", redactPath(path),
		"duration_ms", dur.Milliseconds(), "result", "ok", "status", resp.StatusCode)
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return &Error{Op: op, StatusCode: resp.StatusCode, Messages: []string{"decode: " + err.Error()}}
		}
	}
	return nil
}

// ---- Health / auth ----

// Myself is the authenticated user.
type Myself struct {
	AccountID    string `json:"accountId"`
	EmailAddress string `json:"emailAddress"`
	DisplayName  string `json:"displayName"`
}

// Health verifies authentication by fetching the current user (GET /myself).
func (c *Client) Health(ctx context.Context) (Myself, error) {
	var m Myself
	err := c.do(ctx, "health", http.MethodGet, "/rest/api/3/myself", nil, &m)
	return m, err
}

// ---- Issues ----

// Issue is a decoded Jira issue (the fields the engine needs; docs/08_Jira.md).
type Issue struct {
	Key    string      `json:"key"`
	ID     string      `json:"id"`
	Fields IssueFields `json:"fields"`
}

// IssueFields are the fields the engine reads.
type IssueFields struct {
	Summary     string          `json:"summary"`
	Description json.RawMessage `json:"description"`
	Status      NamedField      `json:"status"`
	Priority    *NamedField     `json:"priority"`
	Labels      []string        `json:"labels"`
	IssueLinks  []IssueLink     `json:"issuelinks"`
	Attachment  []Attachment    `json:"attachment"`
}

// NamedField is the common {name} Jira shape.
type NamedField struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

// SearchResult is a page of issues.
type SearchResult struct {
	StartAt    int     `json:"startAt"`
	MaxResults int     `json:"maxResults"`
	Total      int     `json:"total"`
	Issues     []Issue `json:"issues"`
}

// Search runs a JQL query (GET /search) requesting the given fields (nil = a sensible default set).
func (c *Client) Search(ctx context.Context, jql string, fields []string, maxResults int) (SearchResult, error) {
	if len(fields) == 0 {
		fields = []string{"summary", "status", "priority", "labels", "description", "issuelinks"}
	}
	q := url.Values{}
	q.Set("jql", jql)
	q.Set("maxResults", fmt.Sprintf("%d", maxResults))
	q.Set("fields", strings.Join(fields, ","))
	var res SearchResult
	err := c.do(ctx, "search", http.MethodGet, "/rest/api/3/search?"+q.Encode(), nil, &res)
	return res, err
}

// GetIssue fetches one issue (GET /issue/{key}).
func (c *Client) GetIssue(ctx context.Context, key string) (Issue, error) {
	var iss Issue
	err := c.do(ctx, "get_issue", http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(key), nil, &iss)
	return iss, err
}

// ---- Transitions ----

// Transition is an available workflow transition.
type Transition struct {
	ID   string     `json:"id"`
	Name string     `json:"name"`
	To   NamedField `json:"to"`
}

type transitionsResp struct {
	Transitions []Transition `json:"transitions"`
}

// Transitions lists available transitions for an issue (GET /issue/{key}/transitions).
func (c *Client) Transitions(ctx context.Context, key string) ([]Transition, error) {
	var r transitionsResp
	err := c.do(ctx, "transitions", http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(key)+"/transitions", nil, &r)
	return r.Transitions, err
}

// DoTransition applies a transition by id (POST /issue/{key}/transitions).
func (c *Client) DoTransition(ctx context.Context, key, transitionID string) error {
	body := map[string]any{"transition": map[string]string{"id": transitionID}}
	return c.do(ctx, "do_transition", http.MethodPost, "/rest/api/3/issue/"+url.PathEscape(key)+"/transitions", body, nil)
}

// TransitionTo finds a transition whose target status matches one of statusNames (case-insensitive)
// and applies it. Returns the applied transition. Deterministic: it resolves names to ids so callers
// never hardcode transition ids (docs/08_Jira.md).
func (c *Client) TransitionTo(ctx context.Context, key string, statusNames ...string) (Transition, error) {
	trs, err := c.Transitions(ctx, key)
	if err != nil {
		return Transition{}, err
	}
	for _, tr := range trs {
		for _, want := range statusNames {
			if strings.EqualFold(tr.To.Name, want) || strings.EqualFold(tr.Name, want) {
				if err := c.DoTransition(ctx, key, tr.ID); err != nil {
					return Transition{}, err
				}
				return tr, nil
			}
		}
	}
	return Transition{}, &Error{Op: "transition_to", StatusCode: 404,
		Messages: []string{"no transition to any of: " + strings.Join(statusNames, ", ")}}
}

// ---- Comments ----

// Comment is a decoded comment (body rendered as plain text where possible).
type Comment struct {
	ID     string          `json:"id"`
	Body   json.RawMessage `json:"body"`
	Author NamedAuthor     `json:"author"`
}

// NamedAuthor is a comment/issue author.
type NamedAuthor struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
}

type commentsResp struct {
	Comments []Comment `json:"comments"`
}

// Comments lists comments on an issue.
func (c *Client) Comments(ctx context.Context, key string) ([]Comment, error) {
	var r commentsResp
	err := c.do(ctx, "comments", http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(key)+"/comment", nil, &r)
	return r.Comments, err
}

// AddComment posts a plain-text comment (wrapped in the ADF document Jira Cloud requires).
func (c *Client) AddComment(ctx context.Context, key, text string) (Comment, error) {
	body := map[string]any{"body": adfDoc(text)}
	var out Comment
	err := c.do(ctx, "add_comment", http.MethodPost, "/rest/api/3/issue/"+url.PathEscape(key)+"/comment", body, &out)
	return out, err
}

// ---- Labels & Automation field ----

// AddLabels adds labels to an issue without removing existing ones (PUT with the add operation).
func (c *Client) AddLabels(ctx context.Context, key string, labels ...string) error {
	var ops []map[string]string
	for _, l := range labels {
		ops = append(ops, map[string]string{"add": l})
	}
	body := map[string]any{"update": map[string]any{"labels": ops}}
	return c.do(ctx, "add_labels", http.MethodPut, "/rest/api/3/issue/"+url.PathEscape(key), body, nil)
}

// RemoveLabels removes labels from an issue (PUT with the remove operation).
func (c *Client) RemoveLabels(ctx context.Context, key string, labels ...string) error {
	var ops []map[string]string
	for _, l := range labels {
		ops = append(ops, map[string]string{"remove": l})
	}
	body := map[string]any{"update": map[string]any{"labels": ops}}
	return c.do(ctx, "remove_labels", http.MethodPut, "/rest/api/3/issue/"+url.PathEscape(key), body, nil)
}

// AutomationValue is the value of the Automation eligibility field.
type AutomationValue string

const (
	AutomationEnabled     AutomationValue = "Enabled"
	AutomationDisabled    AutomationValue = "Disabled"
	AutomationManualOnly  AutomationValue = "Manual Only"
	AutomationNeedsReview AutomationValue = "Needs Review"
)

// Valid reports whether v is one of the four allowed Automation values (owner decision 1).
func (v AutomationValue) Valid() bool {
	switch v {
	case AutomationEnabled, AutomationDisabled, AutomationManualOnly, AutomationNeedsReview:
		return true
	}
	return false
}

// SetAutomation sets the single-select Automation field to value. Requires WithAutomationField.
func (c *Client) SetAutomation(ctx context.Context, key string, value AutomationValue) error {
	if c.automationField == "" {
		return &Error{Op: "set_automation", StatusCode: 400,
			Messages: []string{"automation field id not configured (WithAutomationField)"}}
	}
	if !value.Valid() {
		return &Error{Op: "set_automation", StatusCode: 400,
			Messages: []string{"invalid Automation value: " + string(value)}}
	}
	body := map[string]any{"fields": map[string]any{
		c.automationField: map[string]string{"value": string(value)},
	}}
	return c.do(ctx, "set_automation", http.MethodPut, "/rest/api/3/issue/"+url.PathEscape(key), body, nil)
}

// GetAutomation reads the Automation field value from an issue. Empty string if unset/absent.
func (c *Client) GetAutomation(ctx context.Context, key string) (AutomationValue, error) {
	if c.automationField == "" {
		return "", &Error{Op: "get_automation", StatusCode: 400,
			Messages: []string{"automation field id not configured"}}
	}
	q := url.Values{}
	q.Set("fields", c.automationField)
	var raw struct {
		Fields map[string]json.RawMessage `json:"fields"`
	}
	if err := c.do(ctx, "get_automation", http.MethodGet,
		"/rest/api/3/issue/"+url.PathEscape(key)+"?"+q.Encode(), nil, &raw); err != nil {
		return "", err
	}
	f, ok := raw.Fields[c.automationField]
	if !ok || string(f) == "null" {
		return "", nil
	}
	var sel struct {
		Value string `json:"value"`
	}
	_ = json.Unmarshal(f, &sel)
	return AutomationValue(sel.Value), nil
}

// ---- Attachments & links ----

// Attachment is an issue attachment.
type Attachment struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Size     int    `json:"size"`
	MimeType string `json:"mimeType"`
	Content  string `json:"content"` // download URL
}

// IssueLink is a link between issues.
type IssueLink struct {
	ID   string `json:"id"`
	Type struct {
		Name    string `json:"name"`
		Inward  string `json:"inward"`
		Outward string `json:"outward"`
	} `json:"type"`
	InwardIssue  *linkedIssue `json:"inwardIssue"`
	OutwardIssue *linkedIssue `json:"outwardIssue"`
}

type linkedIssue struct {
	Key string `json:"key"`
}

// LinkedIssues returns the keys of issues linked to key.
func (c *Client) LinkedIssues(ctx context.Context, key string) ([]string, error) {
	iss, err := c.GetIssue(ctx, key)
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, l := range iss.Fields.IssueLinks {
		if l.InwardIssue != nil {
			keys = append(keys, l.InwardIssue.Key)
		}
		if l.OutwardIssue != nil {
			keys = append(keys, l.OutwardIssue.Key)
		}
	}
	return keys, nil
}

// Attachments returns the attachments of key.
func (c *Client) Attachments(ctx context.Context, key string) ([]Attachment, error) {
	q := url.Values{}
	q.Set("fields", "attachment")
	var iss Issue
	err := c.do(ctx, "attachments", http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(key)+"?"+q.Encode(), nil, &iss)
	return iss.Fields.Attachment, err
}

// AcceptanceCriteria extracts acceptance criteria from an issue. It prefers a labeled
// "Acceptance Criteria" section in the description; if absent, returns the whole description text.
// Deterministic parsing — the Manager worker later refines AC where this is empty (docs/08_Jira.md).
func (c *Client) AcceptanceCriteria(ctx context.Context, key string) (string, error) {
	iss, err := c.GetIssue(ctx, key)
	if err != nil {
		return "", err
	}
	desc := adfToText(iss.Fields.Description)
	return extractAC(desc), nil
}

// ---- Project configuration ----

// ProjectStatus is one status available in a project (per issue type).
type ProjectStatus struct {
	IssueType string   `json:"issue_type"`
	Statuses  []string `json:"statuses"`
}

type projectStatusesResp []struct {
	Name     string `json:"name"`
	Statuses []struct {
		Name string `json:"name"`
	} `json:"statuses"`
}

// ProjectConfig returns the statuses available per issue type for a project (GET
// /project/{key}/statuses) — used to validate the status_map at doctor/migration time.
func (c *Client) ProjectConfig(ctx context.Context, projectKey string) ([]ProjectStatus, error) {
	var raw projectStatusesResp
	if err := c.do(ctx, "project_config", http.MethodGet,
		"/rest/api/3/project/"+url.PathEscape(projectKey)+"/statuses", nil, &raw); err != nil {
		return nil, err
	}
	out := make([]ProjectStatus, 0, len(raw))
	for _, it := range raw {
		ps := ProjectStatus{IssueType: it.Name}
		for _, s := range it.Statuses {
			ps.Statuses = append(ps.Statuses, s.Name)
		}
		out = append(out, ps)
	}
	return out, nil
}
