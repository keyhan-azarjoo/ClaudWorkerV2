import { listModule, badge } from "ui";

const statusTone = (s) => ({ active: "ok", deprecated: "warn", archived: "", draft: "info" }[s] || "");

export default listModule({
  title: "Knowledge",
  desc: "The Knowledge Brain — durable engineering knowledge. Append-only, versioned, deterministic prompts.",
  query: "knowledge.list",
  events: ["KnowledgeUpdated"],
  empty: "No knowledge entries",
  columns: [
    { key: "id", label: "ID", mono: true },
    { key: "category", label: "Category", render: (r) => badge(r.category) },
    { key: "title", label: "Title" },
    { key: "source", label: "Source", mono: true },
    { key: "status", label: "Status", render: (r) => badge(r.status, statusTone(r.status)) },
    { key: "version", label: "Ver", mono: true },
  ],
});
