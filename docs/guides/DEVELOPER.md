# Developer Guide

## Build & test

```sh
go build ./...
go test -race ./...          # 27 packages, all green
gofmt -l internal/ cmd/      # must be empty
go vet ./...
```

Single external dependency (`gopkg.in/yaml.v3`). Go 1.26. Reserved: no `Date.now`/`math/rand` in
deterministic paths — inject a clock (`WithClock`) for testability.

## Layout

- `internal/<subsystem>` — one package per subsystem (see Architecture Guide). Leaf packages import
  only stdlib; `orchestrator` imports the subsystems it wires; nothing imports `orchestrator`.
- `internal/adapters/<edge>` — real edges behind orchestrator ports (`jira`, `git`, `runtime`,
  `verify`, `discovery`) + `sim` (Simulation Mode).
- `cmd/cwv2` — the CLI (thin; delegates to packages).
- `web/ops-console` — framework-free SPA (ES modules, no build).

## Conventions

- **Dependency inversion at edges:** the orchestrator depends on small ports (`Jira`, `Developer`,
  `Verifier`, `Merger`, `Workspace`); real adapters and `sim` implement them.
- **Stores:** `Store` interface + FileStore (atomic temp+fsync+rename) + MemoryStore; `spec_version`
  metadata + `migrate()` that refuses newer formats.
- **Injectable clock** for deterministic timestamps/tests.
- **Determinism:** same inputs → same outputs everywhere except the Worker Runtime.

## Testing patterns

- Fakes for external edges (`sim`, `fakeDriver`, fake `claude`/`jira`/`adb` via injected seams) — no
  tokens, no hardware.
- Run both store implementations against the same contract tests.
- `internal/stress` is the large-scale deterministic regression harness.

## Adding an edge adapter (allowed; no architecture change)

1. Implement the orchestrator port (e.g. `orchestrator.Verifier`).
2. Behind an injectable seam (CmdRunner/HTTPGetter/exec) so it's testable without the real service.
3. Wire it in `cmd/cwv2/serve.go` live mode; keep the `sim` implementation for Simulation Mode.
4. Register any Control Plane query/metric that exposes its state.
5. Test, `gofmt`/`vet`/`go test -race`, keep Simulation green.

## Branch discipline

Work on a branch off `development`; commit as `keyhanazarjoo <keyhanazarjoo@gmail.com>`; merge
`--no-ff`; never commit directly to `development`/`staging`/`main`. Architecture is frozen — behaviour
changes to frozen subsystems need an ACP.
