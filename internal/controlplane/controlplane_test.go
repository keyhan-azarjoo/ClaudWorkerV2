package controlplane

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/myotgo/ClaudWorkerV2/internal/lease"
)

func steady() func() time.Time {
	t := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	return func() time.Time { cur := t; t = t.Add(time.Second); return cur }
}

// --- Bus ---

func TestBusPublishSubscribe(t *testing.T) {
	b := NewBus(WithClock(steady()))
	id, ch := b.Subscribe(8)
	defer b.Unsubscribe(id)
	ev := b.Publish(EventLeaseGranted, "lease", map[string]any{"resource": "acct-a"})
	if ev.Seq != 1 || ev.Type != EventLeaseGranted {
		t.Fatalf("published = %+v", ev)
	}
	select {
	case got := <-ch:
		if got.Seq != 1 || got.Subsystem != "lease" {
			t.Errorf("received = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no event delivered")
	}
}

func TestBusReplayAfterSeq(t *testing.T) {
	b := NewBus(WithClock(steady()))
	b.Publish(EventAssignmentCreated, "assignment", nil)
	b.Publish(EventAssignmentCompleted, "assignment", nil)
	b.Publish(EventPolicyDecision, "policy", nil)
	got := b.Recent(1) // everything after seq 1
	if len(got) != 2 || got[0].Seq != 2 || got[1].Seq != 3 {
		t.Errorf("replay = %+v", got)
	}
}

func TestBusNonBlockingOnFullSubscriber(t *testing.T) {
	b := NewBus(WithClock(steady()))
	_, _ = b.Subscribe(1) // never drained
	// publishing many events must not block even though the subscriber is full
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			b.Publish(EventRuntimeStarted, "runtime", i)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publish blocked on a full subscriber")
	}
}

// --- REST + auth ---

func newTestServer(t *testing.T, opts ...ServerOption) (*Server, *httptest.Server) {
	t.Helper()
	s := NewServer(NewBus(WithClock(steady())), opts...)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

func TestHealthzIsPublic(t *testing.T) {
	_, ts := newTestServer(t, WithAuth(TokenAuth{Token: "secret"}))
	resp, err := http.Get(ts.URL + "/v1/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("healthz status = %d", resp.StatusCode)
	}
}

func TestAuthRequired(t *testing.T) {
	s, ts := newTestServer(t, WithAuth(TokenAuth{Token: "secret"}))
	s.Query("ping", func(context.Context, url.Values) (any, error) { return "pong", nil })

	// no token → 401
	resp, _ := http.Get(ts.URL + "/v1/query/ping")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no-token status = %d, want 401", resp.StatusCode)
	}
	// correct token → 200
	req, _ := http.NewRequest("GET", ts.URL+"/v1/query/ping", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 200 {
		t.Errorf("token status = %d, want 200", resp.StatusCode)
	}
}

func TestQueryAndCommand(t *testing.T) {
	s, ts := newTestServer(t)
	s.Query("echo", func(_ context.Context, p url.Values) (any, error) {
		return map[string]string{"said": p.Get("msg")}, nil
	})
	called := false
	s.Command("do", func(_ context.Context, body []byte) (any, error) {
		called = true
		return map[string]any{"received": string(body)}, nil
	})

	// query with params
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Said string `json:"said"`
		} `json:"data"`
	}
	getJSON(t, ts.URL+"/v1/query/echo?msg=hi", &env)
	if !env.OK || env.Data.Said != "hi" {
		t.Errorf("query env = %+v", env)
	}
	// unknown query → 404
	resp, _ := http.Get(ts.URL + "/v1/query/nope")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown query status = %d", resp.StatusCode)
	}
	// command with body
	resp, _ = http.Post(ts.URL+"/v1/command/do", "application/json", strings.NewReader(`{"x":1}`))
	if resp.StatusCode != 200 || !called {
		t.Errorf("command status=%d called=%v", resp.StatusCode, called)
	}
}

func TestStatusAndMetricsAggregate(t *testing.T) {
	s, ts := newTestServer(t)
	s.Status("engine", func(context.Context) (any, error) { return map[string]any{"up": true}, nil })
	s.Metric("counts", func(context.Context) (any, error) { return map[string]any{"assignments": 3}, nil })

	var env struct {
		Data map[string]any `json:"data"`
	}
	getJSON(t, ts.URL+"/v1/status", &env)
	if _, ok := env.Data["engine"]; !ok {
		t.Errorf("status missing engine: %+v", env.Data)
	}
	getJSON(t, ts.URL+"/v1/metrics", &env)
	if _, ok := env.Data["counts"]; !ok {
		t.Errorf("metrics missing counts: %+v", env.Data)
	}
}

func TestListDiscovery(t *testing.T) {
	s, ts := newTestServer(t)
	s.Query("a.query", func(context.Context, url.Values) (any, error) { return nil, nil })
	s.Command("a.command", func(context.Context, []byte) (any, error) { return nil, nil })
	var env struct {
		Data []string `json:"data"`
	}
	getJSON(t, ts.URL+"/v1/queries", &env)
	if len(env.Data) != 1 || env.Data[0] != "a.query" {
		t.Errorf("queries = %v", env.Data)
	}
	getJSON(t, ts.URL+"/v1/commands", &env)
	if len(env.Data) != 1 || env.Data[0] != "a.command" {
		t.Errorf("commands = %v", env.Data)
	}
}

// --- SSE ---

func TestSSEStreamsLiveEvents(t *testing.T) {
	s, ts := newTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/v1/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}
	// publish after the client connected
	go func() {
		time.Sleep(20 * time.Millisecond)
		s.Bus().Publish(EventVerificationFinished, "verify", map[string]any{"outcome": "pass"})
	}()
	line := readUntil(t, resp.Body, "VerificationFinished", 2*time.Second)
	if !strings.Contains(line, "VerificationFinished") {
		t.Errorf("stream missing event, got: %q", line)
	}
}

// --- Real subsystem wiring demo (Control Plane exposes a subsystem; holds no logic) ---

func TestExposesLeaseSubsystem(t *testing.T) {
	lm := lease.New(lease.NewMemoryStore(), lease.WithClock(steady()))
	s, ts := newTestServer(t)

	// Wire the subsystem: a query delegating to the Lease Manager, and events on the bus.
	s.Query("leases.active", func(context.Context, url.Values) (any, error) {
		return lm.Active()
	})
	s.Command("leases.acquire", func(_ context.Context, body []byte) (any, error) {
		var req struct{ Resource, Owner string }
		_ = json.Unmarshal(body, &req)
		l, ok, err := lm.Acquire(lease.Request{Kind: lease.KindIssue, Resource: req.Resource, Owner: req.Owner, Reason: "cp"})
		if err == nil && ok {
			s.Bus().Publish(EventLeaseGranted, "lease", l)
		}
		return map[string]any{"granted": ok}, err
	})

	// Acquire a lease via the API command.
	resp, _ := http.Post(ts.URL+"/v1/command/leases.acquire", "application/json", strings.NewReader(`{"Resource":"SCRUM-1","Owner":"SCRUM-1"}`))
	if resp.StatusCode != 200 {
		t.Fatalf("acquire status = %d", resp.StatusCode)
	}
	// Query it back through the API.
	var env struct {
		Data []lease.Lease `json:"data"`
	}
	getJSON(t, ts.URL+"/v1/query/leases.active", &env)
	if len(env.Data) != 1 || env.Data[0].Resource != "SCRUM-1" {
		t.Errorf("active leases via API = %+v", env.Data)
	}
	// The event was published to the bus.
	if got := s.Bus().Recent(0); len(got) != 1 || got[0].Type != EventLeaseGranted {
		t.Errorf("expected a LeaseGranted event, got %+v", got)
	}
}

// helpers

func getJSON(t *testing.T, url string, out any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
}

func readUntil(t *testing.T, r io.Reader, substr string, timeout time.Duration) string {
	t.Helper()
	type res struct{ line string }
	found := make(chan res, 1)
	go func() {
		sc := bufio.NewScanner(r)
		var b strings.Builder
		for sc.Scan() {
			b.WriteString(sc.Text())
			b.WriteString("\n")
			if strings.Contains(sc.Text(), substr) {
				found <- res{b.String()}
				return
			}
		}
		found <- res{b.String()}
	}()
	select {
	case r := <-found:
		return r.line
	case <-time.After(timeout):
		t.Fatal("timed out waiting for SSE event")
		return ""
	}
}
