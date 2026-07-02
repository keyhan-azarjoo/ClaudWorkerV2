# Plugin Guide

ClaudWorker V2 is extended at three plugin seams — all additive, none require an architecture change.

## 1. Worker Runtime providers

`internal/runtime.WorkerRuntime` is the provider port (`Name`, `Run`). Claude Code is the first
provider (`ClaudeWorkerRuntime`). Add Codex/GPT/Gemini/local by implementing `WorkerRuntime` (build the
prompt via `BuildPrompt`, exec/call the provider, parse the result). The deterministic `Runner` adds
timeout/retry/metrics; the Assignment Engine is unchanged. Selection is the Policy Engine's
`RuntimeSelection`; execution runs under the Resource-Manager-selected account.

## 2. Verifier plugins

`internal/verify.Verifier` is the plugin contract (`Name`, `Type`, `Capabilities`, `Verify`). Register
plugins with `verify.Engine.Register`; the engine selects by `Type` + `Capabilities` and aggregates
outcomes. Built-in: `CommandVerifier` (build/unit), `HTTPVerifier` (API/web), `VisualVerifier` (over a
`VisualDriver`). Add a new verification kind by registering a plugin — the core never changes.

### Visual drivers

`internal/verify.VisualDriver` (launch/navigate/click/type/scroll/pair/screenshot/OCR/state) is the
human-like driver seam. Implement it over Appium/adb/WebDriver/simulators for real device/web
verification. `ErrVisualUnavailable` → the verifier reports `Blocked` (fall back to headless).

## 3. Resource discoverers

`internal/resource.Discoverer` (`Discover() []Resource`) feeds `resource.Manager.Discover`. Built-in
(`internal/adapters/discovery`): accounts, Ollama/LM Studio/vLLM providers, adb/simctl/serial devices,
and Static declarations. Add a discoverer for a new resource kind; compose with `Composite`. Probes
sit behind `CmdRunner`/`HTTPGetter` seams for testability.

## Future search plugins (Knowledge Brain)

The Prompt Builder's `candidates` (filter) / `rank` (score) split lets an optional future search
plugin (embeddings/vector) re-rank the same candidate set — the deterministic core never depends on it.

## Rules

- A plugin adds a capability behind an existing port; it introduces no new subsystem.
- Keep provider-specific code minimal; reuse the deterministic wrappers (Runner, verify.Engine,
  resource.Manager).
- Test with an injected seam (no live service/hardware); preserve Simulation Mode.
