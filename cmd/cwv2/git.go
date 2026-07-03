package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"claudworker/internal/git"
	"claudworker/internal/logging"
)

// emit prints v as indented JSON to stdout (machine-readable; never text for AI parsing).
func emit(v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "encode: %v\n", err)
		return
	}
	fmt.Println(string(b))
}

// emitErr prints a structured error object and returns exit code 1.
func emitErr(err error) int {
	emit(map[string]any{"error": err.Error()})
	return 1
}

func gitUsage() {
	fmt.Fprint(os.Stderr, `cwv2 git — deterministic Git toolbelt (JSON output)

subcommands (all take --repo <path> unless noted):
  rev                                   HEAD sha
  clean                                 working tree clean?
  changed                               uncommitted changes
  tags                                  list tags
  aheadbehind --base B --branch BR      ahead/behind counts
  diff --from A --to B                  name-status changes
  conflicts                             unmerged paths
  branch-create --name N --base B       create branch (idempotent)
  branch-delete --name N [--force]      delete branch (idempotent)
  worktree-add --path X --branch BR --base B
  worktree-remove --path X
  worktree-list
  commit --message M [--all]            commit (author=keyhanazarjoo)
  merge --branch BR --message M         --no-ff merge (aborts on conflict)
  rebase --onto REF
  fetch
  push --remote R --branch BR
  clone --url U --dir D
`)
}

func cmdGit(args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		gitUsage()
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	sub := args[0]
	fs := flag.NewFlagSet("git "+sub, flag.ContinueOnError)
	repo := fs.String("repo", "", "repository path")
	name := fs.String("name", "", "branch name")
	base := fs.String("base", "", "base ref")
	branch := fs.String("branch", "", "branch")
	path := fs.String("path", "", "worktree path")
	message := fs.String("message", "", "commit/merge message")
	from := fs.String("from", "", "diff from ref")
	to := fs.String("to", "", "diff to ref")
	onto := fs.String("onto", "", "rebase onto ref")
	remote := fs.String("remote", "origin", "remote name")
	url := fs.String("url", "", "clone url")
	dir := fs.String("dir", "", "clone destination")
	force := fs.Bool("force", false, "force")
	all := fs.Bool("all", true, "stage all changes before commit")
	verbose := fs.Bool("v", false, "verbose logs to stderr")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	var opts []git.Option
	opts = append(opts, git.WithIdentity(git.Identity{Name: "keyhanazarjoo", Email: "keyhanazarjoo@gmail.com"}))
	if *verbose {
		opts = append(opts, git.WithLogger(logging.New(os.Stderr, "info", "text")))
	}
	g := git.New(opts...)
	ctx := context.Background()

	switch sub {
	case "rev":
		rev, err := g.CurrentRevision(ctx, *repo)
		if err != nil {
			return emitErr(err)
		}
		emit(map[string]string{"rev": rev})
	case "clean":
		clean, err := g.IsClean(ctx, *repo)
		if err != nil {
			return emitErr(err)
		}
		emit(map[string]bool{"clean": clean})
	case "changed":
		ch, err := g.ChangedFiles(ctx, *repo)
		if err != nil {
			return emitErr(err)
		}
		emit(map[string]any{"changed": ch})
	case "tags":
		tags, err := g.Tags(ctx, *repo)
		if err != nil {
			return emitErr(err)
		}
		emit(map[string]any{"tags": tags})
	case "aheadbehind":
		bs, err := g.AheadBehind(ctx, *repo, *base, *branch)
		if err != nil {
			return emitErr(err)
		}
		emit(bs)
	case "diff":
		ch, err := g.Diff(ctx, *repo, *from, *to)
		if err != nil {
			return emitErr(err)
		}
		emit(map[string]any{"changes": ch})
	case "conflicts":
		cf, err := g.Conflicts(ctx, *repo)
		if err != nil {
			return emitErr(err)
		}
		emit(map[string]any{"conflicts": cf})
	case "branch-create":
		if err := g.CreateBranch(ctx, *repo, *name, *base); err != nil {
			return emitErr(err)
		}
		emit(map[string]any{"created": *name, "base": *base})
	case "branch-delete":
		if err := g.DeleteBranch(ctx, *repo, *name, *force); err != nil {
			return emitErr(err)
		}
		emit(map[string]any{"deleted": *name})
	case "worktree-add":
		wt, err := g.AddWorktree(ctx, *repo, *path, *branch, *base)
		if err != nil {
			return emitErr(err)
		}
		emit(wt)
	case "worktree-remove":
		if err := g.RemoveWorktree(ctx, *repo, *path); err != nil {
			return emitErr(err)
		}
		emit(map[string]any{"removed": *path})
	case "worktree-list":
		wts, err := g.Worktrees(ctx, *repo)
		if err != nil {
			return emitErr(err)
		}
		emit(map[string]any{"worktrees": wts})
	case "commit":
		res, err := g.Commit(ctx, *repo, *message, *all)
		if err != nil {
			return emitErr(err)
		}
		emit(res)
	case "merge":
		res, err := g.Merge(ctx, *repo, *branch, *message)
		if err != nil {
			return emitErr(err)
		}
		emit(res)
	case "rebase":
		res, err := g.Rebase(ctx, *repo, *onto)
		if err != nil {
			return emitErr(err)
		}
		emit(res)
	case "fetch":
		if err := g.Fetch(ctx, *repo); err != nil {
			return emitErr(err)
		}
		emit(map[string]any{"fetched": *repo})
	case "push":
		if err := g.Push(ctx, *repo, *remote, *branch); err != nil {
			return emitErr(err)
		}
		emit(map[string]any{"pushed": *branch, "remote": *remote})
	case "clone":
		if err := g.Clone(ctx, *url, *dir); err != nil {
			return emitErr(err)
		}
		emit(map[string]any{"cloned": *url, "dir": *dir})
	default:
		fmt.Fprintf(os.Stderr, "cwv2 git: unknown subcommand %q\n\n", sub)
		gitUsage()
		return 2
	}
	return 0
}
