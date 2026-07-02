import { listModule } from "ui";

export default listModule({
  title: "Git",
  desc: "Real Git state — active worktrees and integration status (branches, merges, conflicts, cleanup).",
  query: "git.worktrees",
  events: ["MergeCompleted", "WorkspaceCleaned", "AssignmentCreated", "RuntimeStarted"],
  empty: "No active worktrees",
  columns: [
    { key: "branch", label: "Branch", mono: true, render: (r) => (r.branch || "").replace("refs/heads/", "") },
    { key: "path", label: "Worktree", mono: true },
    { key: "head", label: "HEAD", mono: true, render: (r) => (r.head || "").slice(0, 10) || "—" },
  ],
});
