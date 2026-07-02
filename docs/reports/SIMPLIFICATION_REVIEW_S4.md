# S4 — Simplification Review

Per the Continuous Engineering Rule: before implementing S4, review S0–S3 for simplification using
what later work taught; keep complexity trending **down**. This records that review plus S4's own
minimalism.

## Review of existing subsystems (S0–S3)

| Area | Finding | Action |
|---|---|---|
| `cmd/cwv2` SpecVersion | Binary reported `2.0.0` while the spec is frozen at **2.1.0** (drift after ACP-0001). | **Fixed** — `SpecVersion = "2.1.0"`. Consistency, no behaviour change. |
| `internal/assignment` Store pattern | The S3 `Store` interface + File/Memory pair is a clean, proven inversion. | **Reused** as the template for `knowledge.Store` — one pattern, not two. No new concepts introduced. |
| `internal/enginehome` | Already had `KnowledgeMD` (`knowledge/`). | **Extended minimally** with `KnowledgeEntries` (`knowledge/entries/`) — no layout redesign. |
| `emit`/`emitErr`, config Load, Selector-style flags | Existing CLI helpers cover JSON output + config loading. | **Reused** for `cwv2 knowledge`; no duplicated plumbing. |
| Format-version handling | S3's `spec_version` + `migrate()` policy (reject newer, upgrade legacy). | **Mirrored** as `schema_version` + `migrate()` — identical mental model across stores. |

The S0–S3 code was already minimal (post the pre-S3 simplification review + S3 field minimization);
the only concrete debt found was the version drift, now fixed. Complexity did not increase.

## S4's own minimalism

- **No new external dependency** — module still has exactly one (`gopkg.in/yaml.v3`).
- **No speculative abstraction** — the future search-plugin seam (mod 7) is a *documented split*
  (`candidates`/`rank`), not an unused interface or plugin scaffold (Law 17: interface only at ≥2
  impls; `Store` qualifies, a ranker does not yet).
- **One model, one lifecycle** — a single `Entry` + `Brain`; status transitions share one
  `setStatus`→`Update` path rather than bespoke code per verb.
- **Derived-not-stored** — "current version" is derived (highest version), so there is no mutable
  pointer/index to keep consistent or corrupt.
- **Small files** — `knowledge.go` (model), `store.go` (2 impls), `brain.go` (lifecycle), `prompt.go`
  (builder), `growth.go` (report). Each is single-purpose.

## Net effect

Repository left cleaner than before S4: one long-standing drift removed, one reused pattern (not a new
one), zero new dependencies. Complexity trend: **down/flat**, as required.
