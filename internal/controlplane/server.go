package controlplane

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
)

// QueryHandler answers a read request. It returns any JSON-serialisable value. It must not mutate
// state (that is what commands are for). The Control Plane provides no logic — the handler does, by
// calling a subsystem.
type QueryHandler func(ctx context.Context, params url.Values) (any, error)

// CommandHandler performs an action. body is the raw request body (may be empty).
type CommandHandler func(ctx context.Context, body []byte) (any, error)

// ProviderFunc returns a status/metrics snapshot for one named provider.
type ProviderFunc func(ctx context.Context) (any, error)

// Authenticator decides whether a request is allowed. Implementations stay outside the Control Plane
// so auth strategy (token, JWT, mTLS) can evolve without touching it.
type Authenticator interface {
	Authenticate(r *http.Request) bool
}

// TokenAuth is a simple bearer-token authenticator. An empty token disables auth (dev only).
type TokenAuth struct{ Token string }

// Authenticate checks the Authorization: Bearer <token> header in constant time.
func (a TokenAuth) Authenticate(r *http.Request) bool {
	if a.Token == "" {
		return true
	}
	const p = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, p) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(h[len(p):]), []byte(a.Token)) == 1
}

// Server is the Control Plane HTTP surface. It registers handlers/providers supplied by the wiring
// layer and exposes them over REST + SSE, with authentication. It holds no business logic.
type Server struct {
	bus  *Bus
	auth Authenticator

	mu       sync.RWMutex
	queries  map[string]QueryHandler
	commands map[string]CommandHandler
	status   map[string]ProviderFunc
	metrics  map[string]ProviderFunc
}

// ServerOption configures the Server.
type ServerOption func(*Server)

// WithAuth sets the authenticator (default: TokenAuth{} — open, for tests/dev).
func WithAuth(a Authenticator) ServerOption { return func(s *Server) { s.auth = a } }

// NewServer builds a Server over the given event Bus.
func NewServer(bus *Bus, opts ...ServerOption) *Server {
	s := &Server{
		bus: bus, auth: TokenAuth{},
		queries: map[string]QueryHandler{}, commands: map[string]CommandHandler{},
		status: map[string]ProviderFunc{}, metrics: map[string]ProviderFunc{},
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Bus exposes the event bus so subsystems (via the wiring layer) can publish.
func (s *Server) Bus() *Bus { return s.bus }

// Query registers a named read handler (e.g. "leases.active").
func (s *Server) Query(name string, h QueryHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queries[name] = h
}

// Command registers a named action handler (e.g. "leases.reap").
func (s *Server) Command(name string, h CommandHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands[name] = h
}

// Status registers a named status provider aggregated by GET /v1/status.
func (s *Server) Status(name string, fn ProviderFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status[name] = fn
}

// Metric registers a named metrics provider aggregated by GET /v1/metrics.
func (s *Server) Metric(name string, fn ProviderFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metrics[name] = fn
}

// Handler returns the HTTP handler for the whole Control Plane API (v1). Mount it in any server.
// Every route except /v1/healthz is authenticated.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]any{"ok": true}) })
	mux.HandleFunc("GET /v1/queries", s.withAuth(s.handleListQueries))
	mux.HandleFunc("GET /v1/commands", s.withAuth(s.handleListCommands))
	mux.HandleFunc("GET /v1/query/{name}", s.withAuth(s.handleQuery))
	mux.HandleFunc("POST /v1/command/{name}", s.withAuth(s.handleCommand))
	mux.HandleFunc("GET /v1/status", s.withAuth(s.handleStatus))
	mux.HandleFunc("GET /v1/metrics", s.withAuth(s.handleMetrics))
	mux.HandleFunc("GET /v1/events", s.withAuth(s.handleEvents))
	return withCORS(mux)
}

// withCORS reflects the request Origin so ONE Operations Console can talk to MULTIPLE project backends
// (the project switcher points at different Control Planes). Every endpoint is Bearer-token gated, so
// reflecting the origin exposes nothing that the token doesn't already gate. Handles preflight.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Last-Event-ID")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.auth.Authenticate(r) {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		h(w, r)
	}
}

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.mu.RLock()
	h, ok := s.queries[name]
	s.mu.RUnlock()
	if !ok {
		writeErr(w, http.StatusNotFound, "unknown query: "+name)
		return
	}
	data, err := h(r.Context(), r.URL.Query())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeData(w, data)
}

func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.mu.RLock()
	h, ok := s.commands[name]
	s.mu.RUnlock()
	if !ok {
		writeErr(w, http.StatusNotFound, "unknown command: "+name)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	data, err := h(r.Context(), body)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeData(w, data)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeData(w, s.runProviders(r.Context(), s.status))
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	writeData(w, s.runProviders(r.Context(), s.metrics))
}

// runProviders invokes every registered provider and collects their snapshots. A provider error is
// surfaced per-name (never hidden), not fatal to the whole aggregate.
func (s *Server) runProviders(ctx context.Context, m map[string]ProviderFunc) map[string]any {
	s.mu.RLock()
	providers := make(map[string]ProviderFunc, len(m))
	for k, v := range m {
		providers[k] = v
	}
	s.mu.RUnlock()

	out := make(map[string]any, len(providers))
	for name, fn := range providers {
		v, err := fn(ctx)
		if err != nil {
			out[name] = map[string]any{"error": err.Error()}
			continue
		}
		out[name] = v
	}
	return out
}

func (s *Server) handleListQueries(w http.ResponseWriter, r *http.Request) {
	writeData(w, s.names(true))
}

func (s *Server) handleListCommands(w http.ResponseWriter, r *http.Request) {
	writeData(w, s.names(false))
}

func (s *Server) names(query bool) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var names []string
	if query {
		for n := range s.queries {
			names = append(names, n)
		}
	} else {
		for n := range s.commands {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// --- JSON helpers (uniform envelope) ---

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeData(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": data})
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"ok": false, "error": msg})
}
