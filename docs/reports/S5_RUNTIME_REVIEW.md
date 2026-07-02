# S5 — Worker Runtime (report + pre-merge runtime review)

Implements docs/05 + docs/21 S5, renamed **Workers → Worker Runtime**. The runtime executes reasoning
engines; it is **not** Claude-specific — Claude is the first provider plugin.

## What was built (`internal/runtime`)

| Piece | Role |
|---|---|
| `WorkerRuntime` (interface) | Provider port: `Name()` + `Run(ctx, WorkerInput) (Response, error)`. Stateless, honours ctx. |
| `ClaudeWorkerRuntime` | First provider. Minimal: build prompt → deliver on **stdin** → exec `claude -p --output-format json` → collect **stdout** → parse. |
| `Runner` | Deterministic, provider-agnostic wrapper owning **timeout, cancellation, retry, metrics, logging**. Implements `assignment.Worker`, so the Assignment Engine consumes it **unchanged**. |
| `BuildPrompt` | Deterministic prompt from EXACTLY the four permitted inputs. |
| `Metrics` + `EstimateTokens` | The owner-mandated measurement set; zero-token estimate. |
| `cwv2 worker prompt` | Zero-token CLI: renders the prompt + size/token estimate; never launches a provider. |

## Ownership (as mandated)

Owns: worker lifecycle, process execution, stdin/stdout, cancellation, timeout, retry, metrics,
logging, prompt delivery, response collection. **Owns nothing else** — no Git, Jira, Locks, Decisions,
QA, Knowledge, or Assignment state (verified: the package imports only `assignment` DTOs + stdlib).

## Provider extensibility

A future Codex/GPT/Gemini/local provider implements only `Name()` + `Run()` (thin exec/API + parse).
`Runner`, `BuildPrompt`, `Metrics` are reused untouched, and **the Assignment Engine does not change**
(it depends on `assignment.Worker`, which `Runner` satisfies for any provider).

## Prompt discipline

`BuildPrompt` uses only **Assignment (issue+summary), AcceptanceCriteria, KnowledgeContext,
RelevantFiles** — nothing more. Execution state (attempt, lifecycle state, retries, locks, metrics)
**never** enters the prompt. Guarded by `TestBuildPromptExcludesExecutionState` and
`TestBuildPromptContainsExactlyThePermittedInputs`; deterministic (byte-identical) per
`TestBuildPromptDeterministic`.

## Disposability (Law 4)

`ClaudeWorkerRuntime` and `Runner` hold no field that survives a call. Every `Run` builds a fresh
prompt and a fresh process — no session memory, no resume, no hidden state. Knowledge enters only via
`KnowledgeContext`.

## Retry boundary (no double-counting)

- **Runner** retries only **runtime/infra** errors (spawn, timeout, non-zero exit) up to `MaxRetries`.
- A **semantic** failure (`Result.OK=false`, nil error) is passed straight through — the **Assignment
  Engine** owns bounded *development* retries across restarts (S3).
- A cancelled parent context stops retrying immediately.
Verified: `TestRunnerRetriesTransientThenSucceeds`, `TestRunnerSemanticFailureNotRetried`,
`TestRunnerFailsAfterMaxRetries`, `TestRunnerCancellation`.

## Runtime metrics (measured, zero-token)

`Metrics` = `{runtime, startup_time, execution_time, prompt_bytes, completion_bytes, token_estimate,
retries, failed, cancelled, timed_out}`, emitted per run via an optional sink.

| Metric | Source |
|---|---|
| startup / execution time | Runner clock around spawn + whole attempt |
| prompt / completion bytes | measured by the provider (only it sees them) |
| token_estimate | deterministic `⌈bytes/4⌉` (approximation; the runtime never counts real tokens) |
| retries / failed / cancelled / timed_out | Runner, from attempt loop + `ctx.Err()` / error class |

Sample (from `cwv2 worker prompt`, a small task): `prompt_bytes=280`, `token_estimate=70`,
`relevant_files=0` — the knowledge/file slices dominate size, and both are bounded upstream (Knowledge
Brain byte budget, S4; relevant-file selection, future).

---

## Pre-merge runtime review (mandatory questions)

**Q1 — Can this runtime become smaller?**
Yes, and it was: the provider (`claude.go`) is ~90 LOC doing only exec + parse; the interface is 2
methods; `Response` is 3 fields. There is no framework, no config object, no registry. Nothing further
can be removed without losing a mandated responsibility.

**Q2 — Can any responsibility move back into deterministic Go?**
Already done — this is the core design decision. Prompt assembly, retry, timeout, cancellation
detection, metrics, and logging are all **deterministic Go** (`BuildPrompt` + `Runner`). The only
non-deterministic, provider-specific code is the exec + parse in `ClaudeWorkerRuntime`. AI does no
deterministic work (Law 18).

**Q3 — Can provider-specific code disappear behind the runtime interface?**
Yes — it fully does. Everything Claude-specific is behind `WorkerRuntime`; callers (Runner, engine,
CLI) never reference Claude. Swapping providers is a single constructor change at the wiring layer.

**Simplify-first outcome:** the answers were already "yes" by construction, so no additional
simplification pass was required before merge.

---

## Known items (honest)

- **Coupling:** `runtime` imports `assignment` for the frozen `WorkerInput`/`WorkerResult`/`File` DTOs
  (avoids duplicating them). No cycle (`assignment` does not import `runtime`). If desired post-M1,
  these DTOs could relocate to a neutral package; **not done now** to honour "do not modify M1".
- **Superseded placeholder:** the S2 `assignment.ClaudeWorker` (a thin stub) is now superseded by this
  package. It is left **untouched** (M1 frozen) and is a removal candidate at a future review.
- **Engine wiring (D3):** populating `WorkerInput.KnowledgeContext` from the Knowledge Brain happens in
  the engine's run loop, which is a later integration step (would modify M1). S5 delivers the consuming
  runtime; the field is ready. **Intentionally deferred.**

## Gates

Unit + integration + provider exec (via a fake `claude` binary — real process, stdin/stdout, timeout,
cancellation; **zero tokens**) all pass under `go test -race ./...` (11/11 packages). `gofmt`/`go vet`
clean.
