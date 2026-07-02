# API Guide

The Control Plane is the single API surface (`internal/controlplane`). All clients — web console,
Flutter, CLI — use it; none touch internal packages. Uniform envelope: `{"ok":true,"data":…}` or
`{"ok":false,"error":…}`. All routes except `/v1/healthz` require `Authorization: Bearer <token>` when
a token is configured.

## Routes (v1)

| Method + path | Purpose |
|---|---|
| `GET /v1/healthz` | liveness (public) |
| `GET /v1/query/{name}` | run a registered read query (params via query string) |
| `POST /v1/command/{name}` | run a registered action (JSON body) |
| `GET /v1/status` | aggregate status providers |
| `GET /v1/metrics` | aggregate metrics providers |
| `GET /v1/events` | SSE event stream (replay via `Last-Event-ID`) |
| `GET /v1/queries` · `GET /v1/commands` | discover registered names |

## Queries (registered by the orchestrator)

`assignments.list`, `leases.active`, `resources.snapshot`, `accounts.list`, `runtimes.list`,
`knowledge.list`, `policies.decisions`, and in live mode `jira.queue`, `git.worktrees`, `git.status`,
`runtime.state`.

## Commands

`orchestrator.tick` (process one issue), `leases.reap` (delete expired leases).

## Events (the timeline)

`AssignmentCreated`, `AssignmentCompleted`, `VerificationStarted`, `VerificationFinished`,
`LeaseGranted`, `LeaseExpired`, `RuntimeStarted`, `RuntimeFinished`, `RuntimeMetrics`,
`KnowledgeUpdated`, `PolicyDecision`, `MergeCompleted`, `WorkspaceCleaned`, `AssignmentResumed`,
`AssignmentDeferred`. Each event: `{seq, type, subsystem, time, data}`.

## Adding to the API (no core change)

Register a handler that delegates to a subsystem:

```go
cp.Query("my.read", func(ctx, params) (any, error) { return subsystem.Read() })
cp.Command("my.action", func(ctx, body) (any, error) { return subsystem.Do(body) })
cp.Status("my", func(ctx) (any, error) { return subsystem.Status() })
cp.Metric("my", func(ctx) (any, error) { return subsystem.Metrics() })
```

The Control Plane holds no business logic — handlers do, by calling subsystems.
