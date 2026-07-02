# S3 — State Store: Persistence Review

S3 is the **State Store**: it preserves *only* the minimum information required to recover execution
after an interruption. Everything recomputable from **Git**, **Jira**, or the **Knowledge Brain** is
**not** persisted. The Assignment Engine talks only to the `Store` **interface** and never knows the
implementation.

## The rule applied to every field

For each field once held on the S2 `Assignment`, the question **"Can this be recomputed?"** was asked.
Only fields that would **otherwise be lost** survive.

### ✅ Persisted (3 fields) — each with its justification

| Field | Why it CANNOT be regenerated | Removable in future? | Restart dependency |
|---|---|---|---|
| `issue_key` | Identifies **which in-flight execution** this is. Jira knows the issue *exists*, but not that *this engine* is mid-execution on it (a human could also set an issue In Progress). The identity of an in-flight execution is not derivable from Git/Jira. | No — it is the record's identity. | **Critical**: without it, resume cannot know which issues are in flight. |
| `state` | The lifecycle checkpoint (Claimed/Developing/QA/Merging/…). Nothing in Git or Jira records "we are at QA vs Merging". | No — it is the resume point. | **Critical**: resume continues from this exact state. |
| `attempt` | The retry counter. Not present in Git or Jira. Required to enforce the bounded-retry cap **across** a restart. | No — dropping it would let a crash-loop retry forever. | **High**: preserves the retry budget across restarts (else a restart resets retries). |

On-disk record (proven by `TestPersistedRecordIsMinimal`, which fails if a field is ever added):

```json
{ "issue_key": "SCRUM-1", "state": "merging", "attempt": 2 }
```

### ❌ NOT persisted — recomputable or unneeded-for-recovery (removed in S3)

| Former field | Why it is not persisted | Regenerated from |
|---|---|---|
| `branch` | Deterministic: `agent/<issue_key>`. Also physically present in Git. | recompute (`branchFor`) / Git |
| `worktree` | Deterministic: `<WorktreeDir>/<issue_key>`. | recompute (`worktreeFor`) / config |
| `summary` | The issue summary lives in Jira. | Jira `GetIssue` (fetched in `develop`) |
| `acceptance_criteria` | Derived from the Jira description. | Jira `AcceptanceCriteria` |
| `owner` | Not needed to recover a single-engine execution; the record's existence is the lock. | (re-add with fencing when multi-engine Lock Manager lands, S5) |
| `progress` | Cosmetic human note; not required for recovery. | logs / recompute |
| `merge_sha` | Only set at the terminal Merge→Done step; a terminal Assignment needs no recovery. Discoverable from Git history if ever needed. | Git log |
| `created_at` / `updated_at` | Timing/metrics, not required to recover execution. | (metrics, if ever needed, from the event log — not the state store) |
| `id` (== issue_key) | Redundant with `issue_key`. | — (removed duplication) |

Net: **12 fields → 3.** The persisted footprint dropped to the irreducible minimum.

## The Store interface (storage-agnostic)

The engine depends only on:

```go
type Store interface {
    Save(a *Assignment) error
    Load(issueKey string) (*Assignment, bool, error)
    List() ([]*Assignment, error)
}
```

- `Unfinished` was **not** put on the interface — it is trivially derived from `List` by the engine
  (Law 17), keeping the contract to 3 methods.
- Two real implementations prove the decoupling (and justify the interface — the architecture
  explicitly requires this inversion, docs/21 S3):
  - **`FileStore`** — one JSON file per issue; atomic write (temp + `fsync` + rename); crash-safe.
  - **`MemoryStore`** — map-backed; the tests' default; stores copies (no aliasing).
- The engine has **zero** knowledge of SQLite/JSON/Postgres/Bolt/Badger/Memory. Swapping `FileStore`
  for a future `state.db` is a one-line wiring change with no engine edit.

## Estimated storage growth

- **Per Assignment:** ~60 bytes of JSON (`{issue_key,state,attempt}` + indentation). One file per
  issue.
- **Terminal records:** an issue reaches `done`/`failed` and its ~60-byte record remains as the
  "already processed, do not redo" marker (also enforced at claim time). Growth is therefore
  **O(issues ever processed)** ≈ **tens of KB per thousand issues**.
- **Compaction (future, not needed now):** terminal records could be pruned/archived once the issue
  is confirmed closed in Jira — but at ~60 B each this is not a concern at any realistic backlog
  size. No compaction is implemented (YAGNI).
- **Write frequency:** one `Save` per state transition (~5–6 per Assignment). `BenchmarkFileStoreSave`
  ≈ 3.1 ms/op (fsync-bound) → ~20 ms total persistence per Assignment. Not on any hot path.

## Restart dependency summary

Recovery needs, and only needs, the 3 persisted fields:
1. `issue_key` → which issues are in flight (from `List`, filtered to non-terminal).
2. `state` → where to resume each.
3. `attempt` → remaining retry budget.

Everything else is recomputed (`branch`, `worktree`) or re-fetched (`summary`, `acceptance_criteria`)
during `drive`. `TestRestartResumeFromDisk` proves this end-to-end: a **new `FileStore` over the same
directory** (a genuine reload from disk) resumes an Assignment from `QA` to `Done` **without
re-running** the completed development step (Law 19), using only the persisted record + recomputation.

## Verification

- `go vet` clean · `gofmt` clean · `go test -race ./...` all 9 packages pass.
- `TestPersistedRecordIsMinimal` — on-disk record has exactly `{issue_key,state,attempt}` (guard
  against future persistence creep).
- Store tests run against **both** `FileStore` and `MemoryStore` (interface parity).
- `internal/assignment` coverage 68.5%.

## Verdict

The persistent state is the smallest possible: **3 fields**, each provably non-regenerable and
required for restart. The engine is fully decoupled from storage behind a 3-method interface with two
implementations. Recommend **stop for review** before S4.
