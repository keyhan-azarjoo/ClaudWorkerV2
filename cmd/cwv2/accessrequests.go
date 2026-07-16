package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// accessRequest is a pending "this task needs access it doesn't have" prompt, shown as a top-of-site
// banner with Allow / Deny. On Allow the operator grants a folder (added to the access grants) and the
// task is retried automatically; on Deny it is dismissed. This is what lets the platform NOT dead-end on
// a missing-access failure.
type accessRequest struct {
	ID        string `json:"id"`
	Issue     string `json:"issue"`
	Kind      string `json:"kind"`      // "access" (grant a folder) | "approval" (hardware/action — Allow only)
	Resource  string `json:"resource"`  // clean path/URL the agent asked for (access kind; may be empty)
	Suggested string `json:"suggested"` // default folder to grant so you can just click Allow (access kind)
	Reason    string `json:"reason"`
	CreatedAt string `json:"createdAt"`
}

type accessRequestStore struct {
	mu           sync.Mutex
	reqs         map[string]*accessRequest // one pending request per issue
	grants       *grantStore
	retry        func(issue, note string)
	defaultGrant string // folder auto-suggested on the banner (the project folder)
}

func newAccessRequestStore(grants *grantStore, retry func(string, string), defaultGrant string) *accessRequestStore {
	return &accessRequestStore{reqs: map[string]*accessRequest{}, grants: grants, retry: retry, defaultGrant: defaultGrant}
}

// add raises (or refreshes) a pending request for an issue. kind defaults to "access".
func (s *accessRequestStore) add(issue, kind, resource, reason string) {
	issue = strings.TrimSpace(issue)
	if issue == "" {
		return
	}
	if kind != "approval" {
		kind = "access"
	}
	sug := ""
	if kind == "access" {
		sug = s.defaultGrant
	}
	s.mu.Lock()
	s.reqs[issue] = &accessRequest{ID: issue, Issue: issue, Kind: kind, Resource: strings.TrimSpace(resource), Suggested: sug, Reason: reason, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	s.mu.Unlock()
}

func (s *accessRequestStore) list() []*accessRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*accessRequest, 0, len(s.reqs))
	for _, r := range s.reqs {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

// allow approves a pending request and retries the task. For an "approval" request (hardware/action)
// nothing else is needed — the operator just clicked Allow; the retry carries the approval so the agent
// proceeds with what it prepared. For an "access" request it grants the folder (defaulting to the
// project folder) first.
func (s *accessRequestStore) allow(issue, path string) error {
	s.mu.Lock()
	r := s.reqs[issue]
	s.mu.Unlock()
	if r == nil {
		return fmt.Errorf("no pending request for %s", issue)
	}

	note := ""
	if r.Kind == "approval" {
		note = "The operator APPROVED your request — proceed with what you prepared. " + r.Reason
	} else {
		// access → grant a folder (the picked one, else the request's resource, else the project folder).
		path = strings.TrimSpace(path)
		if path == "" {
			path = strings.TrimSpace(r.Resource)
		}
		if path == "" {
			path = strings.TrimSpace(s.defaultGrant)
		}
		if path == "" {
			return fmt.Errorf("enter a real folder to grant (e.g. your project folder)")
		}
		if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") || strings.HasSuffix(path, ".git") {
			return fmt.Errorf("that's a repo URL — add it on the Git page (it gets cloned), then grant its local folder here")
		}
		if strings.ContainsAny(path, "<>") || strings.Contains(path, " ") {
			return fmt.Errorf("that doesn't look like a folder path — enter a real folder, e.g. /Users/you/Projects")
		}
		if _, err := s.grants.add(path, "always"); err != nil {
			return fmt.Errorf("could not grant %q: %v — enter a real folder that exists on this machine", path, err)
		}
		note = "The operator GRANTED access to " + path + " — use it and continue."
	}

	s.mu.Lock()
	delete(s.reqs, issue)
	s.mu.Unlock()
	if s.retry != nil {
		s.retry(issue, note)
	}
	return nil
}

func (s *accessRequestStore) deny(issue string) {
	s.mu.Lock()
	delete(s.reqs, strings.TrimSpace(issue))
	s.mu.Unlock()
}
