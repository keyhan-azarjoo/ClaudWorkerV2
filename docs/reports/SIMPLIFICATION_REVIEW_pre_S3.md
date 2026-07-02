# Simplification Review — before S3

Review of every package implemented in S0–S2 (`config`, `doctor`, `enginehome`, `logging`, `secrets`,
`git`, `jira`, `assignment`, `cmd/cwv2`) against the requested criteria. Goal: reduce complexity
before adding S3. All changes below are **behavior-preserving** — the full suite (`go test -race
./...`), `go vet`, and `gofmt` pass unchanged after each.

## Changes made (behavior-preserving)

| # | Finding | Category | Action |
|---|---|---|---|
| 1 | `discardHandler` no-op slog handler copied in **3** packages (`git`, `jira`, `assignment`) | duplicated code | Replaced all three with the stdlib **`slog.DiscardHandler`** (Go 1.24+). Deleted `internal/assignment/helpers.go` entirely (it held only that struct); removed the struct + now-unused `context`/`log/slog` imports from `git/helpers.go` and `jira/helpers.go`. |
| 2 | `git.WithBinary(...)` option | unused public API / speculative | Removed. The `bin` field still defaults to `"git"` in `New`; no caller ever overrode it. |
| 3 | `jira.WithHTTPClient(...)` option | unused public API / speculative | Removed. The client still defaults to a 30 s-timeout `http.Client`; tests drive it via the base URL (httptest), never this option. |
| 4 | `git.Pull(...)` method | unused public API (no consumer anywhere) | Removed. `Fetch` covers current needs; the engine uses `Fetch`+`Merge`. Trivial to re-add if refresh-before-merge (S5) needs it. |
| 5 | `config.Config.Path()` + private `path` field (+ its assignment in `Load`) | state that should disappear | Removed. Nothing read the loaded-from path; the field was dead state. |

Net: **−1 file, −~40 LOC**, 3 fewer duplicated type declarations, 2 fewer exported options, 1 fewer
exported method, 1 fewer struct field — with identical behavior.

## Reviewed and deliberately kept (with reason)

- **`assignment.Worker` interface** — the *only* interface in the S0–S2 code. Justified: two real
  implementations (`ClaudeWorker` production `claude -p` runner + `fakeWorker` in tests) and it is the
  architecturally-required seam that keeps AI out of the deterministic engine (Law 18). Not
  speculative. Kept.
- **`ClaudeWorker`** (production Worker) — currently has no CLI wiring (no `run`/`serve` command yet,
  that lands in a later subsystem), but it is the genuine production adapter that justifies the Worker
  port and is the real AI path. Removing it would make the port test-only (a smell) and delete a
  required seam. Kept; noted as "wired later".
- **git toolbelt breadth** (`Diff`, `AheadBehind`, `Rebase`, `Conflicts`, `Tags`, `Worktrees`, …) —
  each has a consumer: the `cwv2 git` CLI subcommands and/or the engine. Only `Pull` was consumer-less
  (removed, #4). The rest stay.
- **Config schema fields parsed-but-not-yet-read** (`workflow.merge`, `usage_guard`, `qa`,
  `dashboard`, `component_map`, `labels`, `status_map`, …) — these are the **documented portability
  surface** ([13_Config](../13_Config.md)) validated at load; they are configuration, not dead code,
  and are consumed by later subsystems. Removing them would break the config contract and `doctor`.
  Kept. (This is the opposite of "config that can become deterministic" — it is intentionally
  project-overridable per Law 16.)
- **`config.StringList`** custom YAML type — justified: `status_map` legitimately mixes scalar and
  list forms (doc 13). Kept.
- **`secrets.Resolver` struct with function fields** — the fields exist for provider-order testing and
  are exercised by `secrets_test.go`; not speculative. Kept.

## Criterion-by-criterion

- **Duplicated code:** found (the 3 `discardHandler`s) → removed via stdlib (#1). No other cross-package
  duplication (git/jira helper funcs are package-local and distinct).
- **Duplicated concepts:** "deferral", "issue lock", "Assignment" each have a single home; the
  Assignment-as-issue-lock was already consolidated in S2 (no separate lock component). None found.
- **Interfaces that no longer justify existence:** only `Worker` exists; it justifies itself (2 impls).
  None removed.
- **Unnecessary abstractions:** the two unused `With…` options (#2, #3). Removed.
- **Packages that can be merged:** none. The dependency graph is a clean tree
  (`cmd → assignment → {git,jira}`; `doctor → {config,enginehome,secrets}`; leaves have no internal
  deps). Each package is cohesive and single-purpose; merging any would reduce clarity, not complexity.
- **Public APIs that can become private:** the leftover exported-but-unused API was removed (#2–#4)
  rather than hidden. Remaining exported symbols all have external callers (CLI, engine, or tests
  asserting typed errors). No further down-scoping needed.
- **Configuration that can become deterministic:** none — the config surface is the intended
  portability boundary (Law 16); it is not disguised constants.
- **State that should disappear:** the dead `config.path` field (#5). Removed. All `Assignment` fields
  have a live producer and consumer (persisted + read by engine/CLI/tests).

## Verification

- `gofmt -l` → clean · `go vet ./...` → clean · `go build ./cmd/cwv2` → OK.
- `go test -race ./...` → all 9 packages pass (unchanged from pre-review).
- `grep discardHandler{` → none; `slog.DiscardHandler` now used in git/jira/assignment.

## Verdict

Complexity reduced with zero behavior change. The codebase is minimal and cycle-free going into S3.
**S3 (emergent `state.db`) may now begin.**
