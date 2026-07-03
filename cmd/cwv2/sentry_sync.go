package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"claudworker/internal/jira"
	"claudworker/internal/sentry"
)

// sentryClients builds a Sentry client per configured org — the project uses two Sentry orgs
// (SENTRY_ORG/SENTRY_TOKEN and SENTRY_ORG_2/SENTRY_TOKEN_2), so "any of the projects" means scanning
// both. Only configured (org+token present) clients are returned.
func sentryClients() []*sentry.Client {
	base := os.Getenv("SENTRY_API_BASE")
	var out []*sentry.Client
	for _, p := range [][2]string{
		{os.Getenv("SENTRY_ORG"), os.Getenv("SENTRY_TOKEN")},
		{os.Getenv("SENTRY_ORG_2"), os.Getenv("SENTRY_TOKEN_2")},
	} {
		if c := sentry.New(base, p[0], p[1]); c.Configured() {
			out = append(out, c)
		}
	}
	return out
}

// recentFromAll gathers recent unresolved issues from every client, de-duplicated by short id.
func recentFromAll(ctx context.Context, scs []*sentry.Client, period string, limit int) ([]sentry.Issue, error) {
	seen := map[string]bool{}
	var all []sentry.Issue
	var firstErr error
	for _, sc := range scs {
		iss, err := sc.RecentIssues(ctx, period, limit)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, is := range iss {
			key := firstNonEmpty(is.ShortID, is.ID)
			if seen[key] {
				continue
			}
			seen[key] = true
			all = append(all, is)
		}
	}
	if len(all) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return all, nil
}

// sentrySync turns recent Sentry errors into Jira bug tickets — one HIGH-priority Bug per new Sentry
// issue, labelled from the Sentry data. It is idempotent (skips issues that already have a ticket via
// the "sentry:<shortId>" label) and capped per run so it can never flood the board. It deliberately
// does NOT add the "ready" label, so no agent auto-runs it — the ticket just sits in the backlog for
// the operator to Run (or check) manually.
func sentrySync(ctx context.Context, jc *jira.Client, scs []*sentry.Client, projectKey string, max int) (map[string]any, error) {
	if jc == nil {
		return nil, fmt.Errorf("jira not configured")
	}
	if len(scs) == 0 {
		return nil, fmt.Errorf("sentry not configured (need SENTRY_ORG + SENTRY_TOKEN)")
	}
	if projectKey == "" {
		projectKey = "SCRUM"
	}
	if max <= 0 {
		max = 10
	}
	// 14-day window so the manual sync catches the whole unresolved backlog, not just the last day.
	issues, err := recentFromAll(ctx, scs, "14d", 25)
	if err != nil {
		return nil, err
	}

	created := []string{}
	skipped := 0
	for _, is := range issues {
		if len(created) >= max {
			break
		}
		idLabel := "sentry:" + sanitizeLabel(firstNonEmpty(is.ShortID, is.ID))
		// Idempotency: if a ticket already carries this Sentry id label, don't create a duplicate.
		if ex, err := jc.Search(ctx, fmt.Sprintf(`project = %s AND labels = "%s"`, projectKey, idLabel), []string{"key"}, 1); err == nil && len(ex.Issues) > 0 {
			skipped++
			continue
		}

		title := firstNonEmpty(is.Title, is.Metadata.Value, is.Metadata.Type, is.ShortID)
		summary := "🟡 [BUG][Sentry] " + title
		if len([]rune(summary)) > 240 {
			summary = string([]rune(summary)[:240])
		}
		desc := strings.Join([]string{
			"Auto-created by ClaudWorker from a Sentry error. No agent has run yet — Run this ticket to have an agent investigate + fix it.",
			"",
			"Sentry short ID: " + is.ShortID,
			"Project: " + firstNonEmpty(is.Project.Slug, is.Project.Name),
			"Level: " + is.Level,
			"Type: " + is.Metadata.Type,
			"Culprit: " + is.Culprit,
			"Events (24h): " + is.Count,
			"Link: " + is.Permalink,
		}, "\n")

		labels := []string{"sentry", "bug", idLabel}
		if p := sanitizeLabel(firstNonEmpty(is.Project.Slug, is.Project.Name)); p != "" {
			labels = append(labels, "sentry-"+p)
		}

		in := jira.CreateIssueInput{
			ProjectKey:  projectKey,
			Summary:     summary,
			Description: desc,
			IssueType:   "Bug",
			Priority:    "High",
			Labels:      labels,
		}
		key, err := jc.CreateIssue(ctx, in)
		if err != nil {
			// Retry once without priority (some projects don't expose the priority field on create).
			in.Priority = ""
			key, err = jc.CreateIssue(ctx, in)
			if err != nil {
				return map[string]any{"created": created, "created_count": len(created), "skipped": skipped, "scanned": len(issues), "error": err.Error()}, err
			}
		}
		created = append(created, key)
	}
	return map[string]any{"created": created, "created_count": len(created), "skipped": skipped, "scanned": len(issues)}, nil
}

// sanitizeLabel makes a Jira-label-safe token (labels may not contain spaces).
func sanitizeLabel(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == ':', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
