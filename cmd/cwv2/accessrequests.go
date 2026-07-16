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
	Resource  string `json:"resource"` // best-effort hint of what it needs (may be empty)
	Reason    string `json:"reason"`
	CreatedAt string `json:"createdAt"`
}

type accessRequestStore struct {
	mu     sync.Mutex
	reqs   map[string]*accessRequest // one pending request per issue
	grants *grantStore
	retry  func(issue string)
}

func newAccessRequestStore(grants *grantStore, retry func(string)) *accessRequestStore {
	return &accessRequestStore{reqs: map[string]*accessRequest{}, grants: grants, retry: retry}
}

// add raises (or refreshes) a pending access request for an issue.
func (s *accessRequestStore) add(issue, resource, reason string) {
	issue = strings.TrimSpace(issue)
	if issue == "" {
		return
	}
	s.mu.Lock()
	s.reqs[issue] = &accessRequest{ID: issue, Issue: issue, Resource: strings.TrimSpace(resource), Reason: reason, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
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

// allow grants a folder (always) then retries the task. path defaults to the request's resource hint.
func (s *accessRequestStore) allow(issue, path string) error {
	s.mu.Lock()
	r := s.reqs[issue]
	s.mu.Unlock()
	if r == nil {
		return fmt.Errorf("no pending access request for %s", issue)
	}
	path = strings.TrimSpace(path)
	if path == "" {
		path = r.Resource
	}
	if path == "" {
		return fmt.Errorf("enter the folder to grant (e.g. your project folder)")
	}
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") || strings.HasSuffix(path, ".git") {
		return fmt.Errorf("that's a repo URL — add it on the Git page, then grant its local folder here")
	}
	if _, err := s.grants.add(path, "always"); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.reqs, issue)
	s.mu.Unlock()
	if s.retry != nil {
		s.retry(issue)
	}
	return nil
}

func (s *accessRequestStore) deny(issue string) {
	s.mu.Lock()
	delete(s.reqs, strings.TrimSpace(issue))
	s.mu.Unlock()
}
