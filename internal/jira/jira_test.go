package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const secretToken = "SUPER-SECRET-TOKEN-123"

// mockJira routes a subset of the Jira REST v3 API for tests.
func mockJira(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/rest/api/3/myself", func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "me@x.com" || p != secretToken {
			w.WriteHeader(401)
			_, _ = io.WriteString(w, `{"errorMessages":["auth"]}`)
			return
		}
		_ = json.NewEncoder(w).Encode(Myself{AccountID: "a1", EmailAddress: "me@x.com", DisplayName: "Me"})
	})

	mux.HandleFunc("/rest/api/3/search", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("jql") == "" {
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"errorMessages":["jql required"]}`)
			return
		}
		_ = json.NewEncoder(w).Encode(SearchResult{
			Total:  1,
			Issues: []Issue{{Key: "SCRUM-1", Fields: IssueFields{Summary: "Do a thing", Labels: []string{"engine"}}}},
		})
	})

	mux.HandleFunc("/rest/api/3/issue/SCRUM-1/transitions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var body map[string]map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["transition"]["id"] != "31" {
				w.WriteHeader(400)
				return
			}
			w.WriteHeader(204)
			return
		}
		_ = json.NewEncoder(w).Encode(transitionsResp{Transitions: []Transition{
			{ID: "11", Name: "Start", To: NamedField{Name: "In Progress"}},
			{ID: "31", Name: "Finish", To: NamedField{Name: "Done"}},
		}})
	})

	mux.HandleFunc("/rest/api/3/issue/SCRUM-1/comment", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if _, ok := body["body"]; !ok {
				w.WriteHeader(400)
				return
			}
			_ = json.NewEncoder(w).Encode(Comment{ID: "c1"})
			return
		}
		_ = json.NewEncoder(w).Encode(commentsResp{Comments: []Comment{{ID: "c1"}}})
	})

	mux.HandleFunc("/rest/api/3/issue/SCRUM-1", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			// accept label update or field update
			w.WriteHeader(204)
		case http.MethodGet:
			if r.URL.Query().Get("fields") == "customfield_100" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"fields": map[string]any{"customfield_100": map[string]string{"value": "Enabled"}},
				})
				return
			}
			desc := adfDoc("Intro line\nAcceptance Criteria\n- builds\n- tests pass")
			_ = json.NewEncoder(w).Encode(Issue{Key: "SCRUM-1", Fields: IssueFields{
				Summary:     "Do a thing",
				Description: mustJSON(desc),
				Status:      NamedField{Name: "To Do"},
			}})
		}
	})

	return httptest.NewServer(mux)
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func newTestClient(t *testing.T, base string, opts ...Option) *Client {
	t.Helper()
	return New(base, "me@x.com", secretToken, opts...)
}

func TestHealth(t *testing.T) {
	srv := mockJira(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	me, err := c.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if me.AccountID != "a1" {
		t.Errorf("account = %q", me.AccountID)
	}
}

func TestHealthAuthFailureIsStructured(t *testing.T) {
	srv := mockJira(t)
	defer srv.Close()
	c := New(srv.URL, "me@x.com", "wrong")
	_, err := c.Health(context.Background())
	je, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *jira.Error, got %T", err)
	}
	if je.StatusCode != 401 {
		t.Errorf("status = %d, want 401", je.StatusCode)
	}
}

func TestSearch(t *testing.T) {
	srv := mockJira(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	res, err := c.Search(context.Background(), "project = SCRUM", nil, 50)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Total != 1 || len(res.Issues) != 1 || res.Issues[0].Key != "SCRUM-1" {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestTransitionToResolvesByName(t *testing.T) {
	srv := mockJira(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	tr, err := c.TransitionTo(context.Background(), "SCRUM-1", "Done")
	if err != nil {
		t.Fatalf("TransitionTo: %v", err)
	}
	if tr.ID != "31" {
		t.Errorf("resolved transition = %s, want 31", tr.ID)
	}
}

func TestTransitionToNotFound(t *testing.T) {
	srv := mockJira(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	_, err := c.TransitionTo(context.Background(), "SCRUM-1", "Nonexistent")
	if err == nil {
		t.Fatal("expected error for missing transition")
	}
}

func TestAddCommentAndLabels(t *testing.T) {
	srv := mockJira(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	if _, err := c.AddComment(context.Background(), "SCRUM-1", "hello"); err != nil {
		t.Errorf("AddComment: %v", err)
	}
	if err := c.AddLabels(context.Background(), "SCRUM-1", "engine", "deferred"); err != nil {
		t.Errorf("AddLabels: %v", err)
	}
}

func TestAutomationField(t *testing.T) {
	srv := mockJira(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL, WithAutomationField("customfield_100"))

	if err := c.SetAutomation(context.Background(), "SCRUM-1", AutomationEnabled); err != nil {
		t.Errorf("SetAutomation: %v", err)
	}
	if err := c.SetAutomation(context.Background(), "SCRUM-1", AutomationValue("Bogus")); err == nil {
		t.Error("expected error on invalid Automation value")
	}
	got, err := c.GetAutomation(context.Background(), "SCRUM-1")
	if err != nil {
		t.Fatalf("GetAutomation: %v", err)
	}
	if got != AutomationEnabled {
		t.Errorf("GetAutomation = %q, want Enabled", got)
	}
}

func TestAutomationRequiresFieldID(t *testing.T) {
	srv := mockJira(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL) // no WithAutomationField
	if err := c.SetAutomation(context.Background(), "SCRUM-1", AutomationEnabled); err == nil {
		t.Error("expected error when automation field id not configured")
	}
}

func TestAcceptanceCriteriaExtraction(t *testing.T) {
	srv := mockJira(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	ac, err := c.AcceptanceCriteria(context.Background(), "SCRUM-1")
	if err != nil {
		t.Fatalf("AcceptanceCriteria: %v", err)
	}
	if !strings.Contains(ac, "builds") || strings.Contains(ac, "Intro line") {
		t.Errorf("AC extraction wrong: %q", ac)
	}
}

// TestTokenNeverLogged is the security gate: the auth token must never appear in structured logs.
func TestTokenNeverLogged(t *testing.T) {
	srv := mockJira(t)
	defer srv.Close()
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	c := newTestClient(t, srv.URL, WithLogger(log))
	if _, err := c.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), secretToken) {
		t.Fatalf("SECURITY: auth token leaked into logs:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), `"op":"health"`) {
		t.Errorf("expected structured op log, got: %s", buf.String())
	}
}
