import { listModule, badge } from "ui";

export default listModule({
  title: "Projects",
  desc: "Onboarded projects — each has its own engine home, Knowledge Brain and execution state.",
  query: "projects.list",
  events: [],
  empty: "No projects onboarded",
  columns: [
    { key: "project", label: "Project", mono: true },
    { key: "repos", label: "Repos", render: (r) => (Array.isArray(r.repos) ? r.repos.length : r.repos ?? "—") },
    { key: "dev_branch", label: "Dev branch", mono: true },
    { key: "status", label: "Status", render: (r) => badge(r.status || "active", "ok") },
  ],
});
