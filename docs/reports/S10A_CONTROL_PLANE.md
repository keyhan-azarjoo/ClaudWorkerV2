# S10A — Control Plane (backend)

Implements docs/09 + docs/21 S10A, **renamed Dashboard → Control Plane**. The Dashboard is now just
one client of the Control Plane. Package: `internal/controlplane`. **Backend only — no frontend.**

## What it owns (and what it does not)

The Control Plane owns the **API surface**: REST, SSE events, authentication, commands, queries,
status, metrics, and streaming updates. It owns **no business logic** — every subsystem stays
independent, and the Control Plane merely exposes them by running **injected handlers** and streaming
**events they publish**. It is a leaf package (stdlib only) and imports no subsystem, so it *cannot*
duplicate their logic.

## API (v1)

| Route | Purpose |
|---|---|
| `GET /v1/healthz` | liveness (public) |
| `GET /v1/query/{name}` | run a registered read query (params via query string) |
| `POST /v1/command/{name}` | run a registered action (JSON body) |
| `GET /v1/status` | aggregate of all registered status providers |
| `GET /v1/metrics` | aggregate of all registered metrics providers |
| `GET /v1/events` | **SSE** event stream (with replay) |
| `GET /v1/queries` · `GET /v1/commands` | discovery: list registered names |

Uniform JSON envelope: `{"ok":true,"data":…}` / `{"ok":false,"error":…}`. Everything except
`/v1/healthz` is authenticated.

## No business logic — dependency inversion

Handlers/providers are **registered by the wiring layer**, not implemented here:
`Query(name, fn)`, `Command(name, fn)`, `Status(name, fn)`, `Metric(name, fn)`. A subsystem is exposed
by registering a handler that calls it — the Control Plane never contains that logic. Proven by
`TestExposesLeaseSubsystem`: a `leases.active` query and `leases.acquire` command are wired to a real
`lease.Manager`, and the acquire publishes a `LeaseGranted` event — all through the API, with zero
lease logic in the Control Plane.

## Event model

Subsystems publish to a `Bus`; the Control Plane streams them. Documented event vocabulary (open
string set): `AssignmentCreated`, `AssignmentCompleted`, `VerificationStarted`,
`VerificationFinished`, `LeaseGranted`, `LeaseExpired`, `RuntimeStarted`, `RuntimeFinished`,
`KnowledgeUpdated`, `PolicyDecision`.

- `Bus.Publish(type, subsystem, data)` assigns a monotonic `Seq`, retains the event in a bounded ring,
  and fans out to subscribers **non-blocking** — a slow/full subscriber is skipped for that event
  (never stalls a publisher) and catches up via replay (`TestBusNonBlockingOnFullSubscriber`).
- **SSE replay:** on connect, the stream replays retained events after the client's `Last-Event-ID`
  (or `?last_event_id=`), then streams live events with no duplicates — so a reconnecting client
  misses nothing within the ring (`TestSSEStreamsLiveEvents`, `TestBusReplayAfterSeq`).

## Clients (all equal)

Web Dashboard, Flutter desktop, Flutter mobile, and the CLI are all **clients** of this API. None may
touch internal packages directly; every UI feature is an API call, so the Web UI holds no unique
business logic. (The frontends themselves are **not** built in S10A.)

## Authentication

`Authenticator` interface keeps auth strategy outside the Control Plane. `TokenAuth` ships now
(bearer token, constant-time compare; empty token = open for dev). JWT/mTLS can replace it without
touching the server. `TestAuthRequired` + `TestHealthzIsPublic` prove enforcement.

## Boundaries

- Leaf package, **stdlib only** — no new dependency; imports no subsystem (the lease import is
  test-only, to demonstrate wiring).
- SSE chosen over WebSocket to honour the zero-dependency rule; a WS transport can be added later.
- No engine rewire; the server is a library the future `cwv2 serve` mounts once subsystems are wired.
  M1 untouched.

## Gates

`gofmt`/`go vet` clean; `go test -race ./...` — **17/17 packages PASS**. Coverage: bus
publish/subscribe/replay/non-blocking, auth (public healthz + 401/200), query/command (+404),
status/metrics aggregation, discovery listing, live SSE streaming, and the real lease-subsystem wiring
demo.

## Deferrals (honest)

- **Frontend (React/Flutter/CLI clients)** — explicitly **not** implemented (S10B / later).
- **Live subsystem registration** (assignment/verify/lease/resource/knowledge/policy/runtime queries +
  their event publishes) is wired by the `cwv2 serve` command when the orchestration loop exists — the
  Control Plane exposes them generically today; a test proves the pattern with the Lease Manager.
- **WebSocket transport** and richer auth (JWT) are additive; the seams exist.
