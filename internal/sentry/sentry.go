// Package sentry is a tiny read-only client for the Sentry API — just enough to list recent
// unresolved issues across an organization so they can be turned into Jira bug tickets. It never
// mutates Sentry; the token travels only in the Authorization header and is never logged.
package sentry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to one Sentry organization.
type Client struct {
	base  string // e.g. https://de.sentry.io/api/0
	org   string // organization slug
	token string // API auth token (Bearer)
	http  *http.Client
}

// New builds a Client. base defaults to the SaaS API root.
func New(base, org, token string) *Client {
	base = strings.TrimRight(base, "/")
	if base == "" {
		base = "https://sentry.io/api/0"
	}
	// Accept a bare host or the ingest host; ensure it ends with /api/0.
	if !strings.HasSuffix(base, "/api/0") {
		base = base + "/api/0"
	}
	return &Client{base: base, org: org, token: token, http: &http.Client{Timeout: 20 * time.Second}}
}

// Configured reports whether the client has the org + token it needs.
func (c *Client) Configured() bool { return c != nil && c.org != "" && c.token != "" }

// Issue is the subset of a Sentry issue we surface.
type Issue struct {
	ID        string `json:"id"`
	ShortID   string `json:"shortId"`
	Title     string `json:"title"`
	Culprit   string `json:"culprit"`
	Level     string `json:"level"`
	Count     string `json:"count"`
	Permalink string `json:"permalink"`
	Project   struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	} `json:"project"`
	Metadata struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"metadata"`
}

// RecentIssues lists unresolved issues for the org seen in the given period (e.g. "24h", "14d"),
// most-frequent first. limit caps the page size.
func (c *Client) RecentIssues(ctx context.Context, period string, limit int) ([]Issue, error) {
	if !c.Configured() {
		return nil, fmt.Errorf("sentry: not configured (need SENTRY_ORG + SENTRY_TOKEN)")
	}
	if period == "" {
		period = "24h"
	}
	if limit <= 0 {
		limit = 25
	}
	q := url.Values{}
	q.Set("query", "is:unresolved")
	q.Set("statsPeriod", period)
	q.Set("limit", fmt.Sprint(limit))
	q.Set("sort", "freq")
	endpoint := fmt.Sprintf("%s/organizations/%s/issues/?%s", c.base, url.PathEscape(c.org), q.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("sentry issues: http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out []Issue
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("sentry issues: decode: %w", err)
	}
	return out, nil
}
