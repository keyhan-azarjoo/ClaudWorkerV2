# 05 — Workers

Workers are the **only** place tokens are spent. There are exactly **four** worker types. They are
short-lived, stateless, and disposable: created for one reasoning step, given a small prompt, and
torn down (P4, P5, P9). Everything else is deterministic Go.

## The four worker types

| Worker | Stage | Job (reasoning only) | Never does |
|---|---|---|---|
| **Manager** | PLANNED | Restate AC, pick approach, list files to touch, flag deferrals, decide if QA can be visual | Write code, run builds by hand |
| **Developer** | DEVELOPING | Modify code to satisfy the plan/AC; use tools to build/test locally | Merge, transition Jira, reimplement tools |
| **QA** | QA | Verify against AC like a human (navigate, screenshot, compare, read logs); render a verdict | Fix the code, merge |
| **Integrator** | MERGING | *Only when needed:* judge a semantic merge conflict or a risky merge | Routine merges (those are deterministic) |

There are **no** other agents. Roles like "backend", "app", "firmware", "security", "release" from
V1 are **not** separate agents in V2 — they are (a) deterministic pipelines in the toolbelt/plugins,
or (b) just the Developer/QA worker operating with a project-type plugin's tools. This is the
simplicity principle (P6) applied hard.

## Why so few

V1 had ~12 long-lived role agents that coordinated via Jira/Confluence and frequently conflicted,
worked stale code, and burned tokens on deterministic chores. V2 collapses roles because:

- **Determinism absorbs most roles.** Release, security scanning, reliability triage, code-review
  linting, indexing — all deterministic → Go, not agents.
- **Plugins absorb domain differences.** "App vs backend vs firmware" is a *toolset*, not a
  *personality*. The same Developer worker + the right plugin covers all of them.
- **Disposability beats specialization.** A fresh, small-context worker per step outperforms a
  long-lived agent carrying stale context.

## Worker lifecycle (managed by the deterministic Worker Runner)

1. Orchestrator decides a reasoning step is needed and which worker type.
2. Worker Runner assembles the **small prompt** from the Brain (P9) + the stage's **output schema**.
3. Spawn `claude -p` with:
   - `--output-format json`
   - `--permission-mode acceptEdits`
   - `--allowedTools <stage-specific set>` (least privilege)
   - `--strict-mcp-config`
   - `--append-system-prompt <role charter>`
   - a wall-clock timeout and a token budget.
4. Worker reasons, calling deterministic **tools** for every action (build, screenshot, git, …).
5. Worker returns JSON. Runner validates against the schema; on invalid output, one bounded reprompt,
   else the step fails deterministically (attempt++).
6. Worker process is killed; nothing persists in it. Durable output already went to the Brain/Git via
   tools; the returned JSON drives the next deterministic transition.

## Prompt contract (every worker, P9)

The prompt contains **only** these sections, assembled deterministically from the Brain:

```
# Role charter            (appended system prompt — short, stable per worker type)
# Task                    (Jira summary + description)
# Acceptance Criteria     (bulleted, testable)
# Relevant Files          (few files, scoped by index + dep-graph; contents truncated)
# Architecture Summary    (knowledge/architecture.md, short)
# Recent Decisions        (top-K relevant ADRs)
# Current Failures        (structured failures from prior attempts, if any)
# Output Schema           (the exact JSON the worker must return)
```

No whole-repo dumps. No unrelated history. No cross-issue chatter.

## Output schemas (validated deterministically)

Each worker must return JSON matching its schema. Illustrative shapes (final schemas live with the
implementation):

**Manager → Plan**
```json
{
  "acceptance_criteria": ["...", "..."],
  "approach": "short description",
  "files_to_touch": ["path/a", "path/b"],
  "qa_strategy": {"visual": true, "steps": ["launch", "navigate to X", "assert Y"]},
  "deferrals_expected": [{"kind": "hardware", "reason": "..."}],
  "new_decisions": [{"context": "...", "decision": "...", "consequences": "..."}]
}
```

**Developer → ChangeSummary**
```json
{
  "changed_files": ["path/a"],
  "summary": "what changed and why",
  "commands_run": ["build", "unit"],
  "self_check": {"build": "pass", "unit": "pass"},
  "notes_for_qa": "how to verify",
  "new_failures_seen": [{"signature": "...", "detail": "...", "resolution": "..."}]
}
```

**QA → Verdict**
```json
{
  "verdict": "PASS | FAIL | DEFER",
  "checked": [{"criterion": "...", "result": "pass|fail", "evidence": "screenshot-id|log-id"}],
  "failures": [{"criterion": "...", "detail": "actionable", "evidence": "..."}],
  "deferrals": [{"kind": "hardware|device|visual|human", "reason": "...", "howto": "..."}]
}
```

**Integrator → MergeJudgment** (only invoked on conflict/risk)
```json
{
  "action": "merge | rebase-and-retry | needs-human",
  "conflict_resolution": "description or per-file guidance",
  "risk": "low|medium|high",
  "reason": "..."
}
```

## Least-privilege tool sets

Each worker type gets only the tools it needs (`--allowedTools`):

- **Manager:** Brain read/query, index/search, read files. No writes to code, no git mutation.
- **Developer:** read/write files in its worktree, build/format/test tools, git add/commit **on its
  own branch only**, Brain write (failures/decisions). No merge, no Jira transition.
- **QA:** app-launch/navigate, screenshot, imgdiff, OCR, log parse, device tools, Brain read + QA-map
  write, deferral write. No code writes, no merge.
- **Integrator:** git merge/rebase within the serialized merge lock. Invoked only on conflict.

This enforces P8 (no conflicts) and NFR-6 (safety) at the tool boundary, not by trusting prose.

## Cost controls per worker

- Small prompt by construction (P9) → low input tokens.
- Deterministic actions are tool calls → **zero** tokens for the action itself (P5).
- Per-worker token budget + wall-clock timeout; overruns fail the step deterministically.
- Usage guard can prevent new workers from spawning entirely (NFR-2) without touching in-flight
  deterministic work.

## Charters (appended system prompts)

Each worker type has one short, stable charter file (role, boundaries, "call tools, don't do it
yourself", identity rule C-2, "never touch shared trees" NFR-6). Charters are versioned in the repo
under `plugins/_core/charters/` and are project-agnostic; project specifics come from the Brain and
config, never from the charter (P10).
