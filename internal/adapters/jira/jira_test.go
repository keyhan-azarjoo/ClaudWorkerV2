package jiraadapter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"claudworker/internal/adapters/sim"
	"claudworker/internal/assignment"
	"claudworker/internal/controlplane"
	jira "claudworker/internal/jira"
	"claudworker/internal/knowledge"
	"claudworker/internal/lease"
	"claudworker/internal/orchestrator"
	"claudworker/internal/policy"
	"claudworker/internal/resource"
	"claudworker/internal/verify"
)

// mockJira is a real HTTP server shaped like the Jira REST API — it exercises the adapter's actual
// HTTP client (no live Jira needed), which is legitimate end-to-end validation for the adapter.
func mockJira(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /rest/api/3/search/jql", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jira.SearchResult{
			IsLast: true,
			Issues: []jira.Issue{{Key: "SCRUM-1", Fields: jira.IssueFields{
				Summary: "Add hello file", Status: jira.NamedField{Name: "To Do"},
			}}},
		})
	})
	mux.HandleFunc("GET /rest/api/3/issue/{key}/transitions", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"transitions": []map[string]any{
			{"id": "11", "name": "Start", "to": map[string]string{"name": "In Progress"}},
			{"id": "31", "name": "Finish", "to": map[string]string{"name": "Done"}},
		}})
	})
	mux.HandleFunc("POST /rest/api/3/issue/{key}/transitions", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("POST /rest/api/3/issue/{key}/comment", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":"c1"}`)
	})
	mux.HandleFunc("PUT /rest/api/3/issue/{key}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("GET /rest/api/3/issue/{key}", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"key": r.PathValue("key"),
			"fields": map[string]any{
				"summary":     "Add hello file",
				"description": "Acceptance Criteria\n- hello.txt exists",
				"status":      map[string]string{"name": "To Do"},
			},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newAdapter(t *testing.T) *Adapter {
	srv := mockJira(t)
	return New(jira.New(srv.URL, "me@x.com", "tok"), "project = SCRUM")
}

func TestAdapterEligibleEnrichesAC(t *testing.T) {
	a := newAdapter(t)
	issues, err := a.Eligible(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].Key != "SCRUM-1" || issues[0].Summary != "Add hello file" {
		t.Fatalf("eligible = %+v", issues)
	}
	if issues[0].AcceptanceCriteria == "" {
		t.Error("acceptance criteria not enriched from the issue")
	}
}

func TestAdapterGetTransitionComment(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()
	iss, err := a.Get(ctx, "SCRUM-1")
	if err != nil || iss.Key != "SCRUM-1" {
		t.Fatalf("get = %+v err=%v", iss, err)
	}
	if err := a.Transition(ctx, "SCRUM-1", "In Progress"); err != nil {
		t.Errorf("transition: %v", err)
	}
	if err := a.Comment(ctx, "SCRUM-1", "claimed"); err != nil {
		t.Errorf("comment: %v", err)
	}
	if err := a.AddLabels(ctx, "SCRUM-1", "claudworker"); err != nil {
		t.Errorf("labels: %v", err)
	}
}

func TestAdapterQueue(t *testing.T) {
	a := newAdapter(t)
	items, err := a.Queue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Key != "SCRUM-1" || items[0].Status != "To Do" {
		t.Errorf("queue = %+v", items)
	}
}

// TestPlatformFunctionalWithRealJira is the key Phase-2 #1 validation: swap the REAL Jira adapter into
// the orchestrator (external Jira via a real HTTP client) with the remaining edges simulated, and
// prove the platform still autonomously drives an issue claim → completion.
func TestPlatformFunctionalWithRealJira(t *testing.T) {
	a := newAdapter(t)

	res := resource.New()
	res.Register(resource.Resource{ID: "claude-1", Kind: resource.KindClaudeAccount, Health: resource.HealthHealthy})
	store := assignment.NewMemoryStore()
	cp := controlplane.NewServer(controlplane.NewBus())

	o := orchestrator.New(&orchestrator.Orchestrator{
		Resources: res,
		Policy:    policy.New(policy.Config{}),
		Leases:    lease.New(lease.NewMemoryStore()),
		Knowledge: knowledge.New(knowledge.NewMemoryStore()),
		Verify:    verify.New(),
		Store:     store,
		CP:        cp,
		Jira:      a, // REAL Jira adapter
		Developer: &sim.Developer{},
		Verifier:  sim.NewVerifier(),
		Merger:    sim.Merger{},
	})

	did, err := o.ProcessOnce(context.Background())
	if err != nil || !did {
		t.Fatalf("ProcessOnce did=%v err=%v", did, err)
	}
	got, ok, _ := store.Load("SCRUM-1")
	if !ok || got.State != assignment.StateDone {
		t.Fatalf("assignment via real Jira = %+v, want Done", got)
	}
}
