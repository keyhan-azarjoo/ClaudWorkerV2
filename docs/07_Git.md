# 07 — Git

Git/GitHub is the single source of truth for code (P3). This document defines the branch model, the
locking/ownership model that guarantees no conflicts (P8), and the merge protocol. All git operations
are deterministic Go (the `git.*` toolbelt) — zero tokens.

## Branch model

```
main         ← release only; never receives direct commits or worker merges
  ▲ promote
staging      ← pre-release; never receives direct commits or worker merges
  ▲ promote
development  ← integration trunk; advances ONLY via --no-ff merge of a verified worker branch
  ▲ merge --no-ff
agent/<KEY>-<slug>   ← tiny, short-lived, one per issue, deleted right after merge
```

- **`development`** is the base for all work and the only branch workers merge into (C-3, I-3).
- **`staging`/`main`** receive code only by **promotion** (dashboard action / promote scripts),
  never by a worker (C-3). Promotion is out of the per-issue loop.
- **Worker branches** are named `agent/<ISSUE-KEY>-<short-slug>`, created from the newest
  `development`, and **deleted immediately after merge** (I-4). No long-lived branches.

## Worktrees (the anti-conflict foundation — P8, NFR-5)

- Every in-flight issue gets its **own git worktree** — a separate working directory on its own
  branch. Workers **never** share a checkout.
- **Forbidden on any shared clone:** `checkout`/`switch` to another branch, `reset --hard`, `stash`,
  or anything that mutates a tree another worker may be using (this caused real incidents in V1).
- A worktree is created at CLAIM and removed at CLOSE (or on cleanup after a crash). Worktrees live
  under the engine home on the SSD, e.g. `<engine-home>/projects/<project>/worktrees/<KEY>/`.

## Ownership & locking

> The full locking design — all scopes (device/issue/repo/module/folder/file/merge), fencing,
> deadlock prevention, stealing, and crash/reboot recovery — is [15_LockManager](15_LockManager.md).
> This section states only the two git-relevant scopes.

Two lock scopes, both persisted in SQLite with TTL + heartbeat ([12_Database](12_Database.md)):

1. **Issue lock** — one worker owns one Jira issue end-to-end. Prevents two workers touching the
   same issue (FR-3, I-1).
2. **Merge lock** — a single global lock serializing the Integrator so `development` advances one
   verified merge at a time (FR-8, I-3).

Locks carry the owner (worker/run id), acquired-at, heartbeat-at, and TTL. A crashed worker's lock
is **reaped** when its heartbeat goes stale (NFR-8), its worktree cleaned, and its issue returned to
the last stable stage.

## Per-issue git protocol (deterministic)

1. **Fetch & base** — `git fetch`; fast-forward local `development`; verify it matches origin.
2. **Branch + worktree** — create `agent/<KEY>-<slug>` from `development` in a fresh worktree
   (FR-5).
3. **Work** — Developer commits small, frequent commits **on this branch only**, author fixed to
   `keyhanazarjoo <keyhanazarjoo@gmail.com>` (C-2), no `Co-Authored-By` trailers.
4. **Refresh before gates** — before Build and before Merge, `git fetch` and refresh the branch onto
   the newest `development` (rebase or merge-from-development). This catches owner commits (FR-7) and
   other merges. Owner work is never overwritten.
5. **Merge** — under the merge lock: ensure branch is up to date with `development`, run the final
   verification, then `git merge --no-ff agent/<KEY>-<slug>` into `development`.
6. **Delete** — delete the branch and remove the worktree (I-4).
7. **Push** — push `development` to origin (`keyhan-azarjoo` credentials, C-2). Note the V1 lesson:
   local merges are **not** auto-pushed by reconcile scripts — the engine pushes explicitly.

## Merge protocol details

- **Clean case (the norm):** tiny branch, refreshed base → fast, conflict-free `--no-ff` merge, no
  worker involved. Fully deterministic.
- **Conflict case:** refresh from `development` and re-run Build+QA gates on the new base. If the
  conflict is purely textual and auto-resolvable, resolve deterministically and re-verify. Only a
  **semantic** conflict needing judgment invokes the **Integrator worker** (05); it may return
  `merge`, `rebase-and-retry`, or `needs-human`. Never force, never overwrite.
- **Repeated conflict:** after `K` refresh/retry cycles still conflicting → NEEDS_HUMAN.

## Owner coexistence (FR-7, NFR-11)

- The Scheduler fetches periodically. Detecting new commits on `development` (owner or merges), it
  refreshes in-flight worktrees before their next gate.
- The engine **never** rewrites published history, never force-pushes shared branches, and never
  touches the owner's working directories. Owner commits always win; the engine adapts around them.

## Identity & hooks (owner rules)

- **Author/committer:** always `keyhanazarjoo <keyhanazarjoo@gmail.com>` (C-2). Configured per
  worktree by the git tool; never the `myotgo` author, never `Co-Authored-By: Claude`.
- **Remote/`gh`:** operations run as GitHub user `keyhan-azarjoo`.
- **Secret-scanning pre-commit** (gitleaks-style) runs in every repo. A *real* secret block → fix it
  (env/secret store), never bypass. `--no-verify` only for a genuinely broken hook.
- **Branch guard:** a direct commit while on `development`/`staging`/`main` is blocked (C-3); the
  engine only ever commits on `agent/*` branches and merges `--no-ff`.

## Multi-repo projects

A project config may declare **multiple repos** (e.g. app + backend + firmware). Each repo has its
own `development`, its own worktrees, and its own locks. An issue that spans repos gets a worktree
per touched repo, and each repo's branch is merged under that repo's merge lock. The issue only
CLOSEs when all its repos' branches are merged (or their checks deferred).

## Invariants (git-specific, reinforcing 03)

- **G-1** No commit ever lands directly on `development`/`staging`/`main`.
- **G-2** `development` advances only by `--no-ff` merge of a verified `agent/*` branch.
- **G-3** One worktree per branch; shared clones are never mutated by workers.
- **G-4** Every `agent/*` branch is deleted right after its merge.
- **G-5** No force-push, no history rewrite on shared branches, no overwrite of owner work.
- **G-6** All commits authored by `keyhanazarjoo`, no `Co-Authored-By: Claude`.
