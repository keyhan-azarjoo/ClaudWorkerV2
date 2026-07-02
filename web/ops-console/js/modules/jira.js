import { listModule, badge } from "ui";

const autoTone = (a) => ({ Enabled: "ok", Disabled: "danger", "Manual Only": "warn", "Needs Review": "info" }[a] || "");

export default listModule({
  title: "Jira",
  desc: "The work queue — issues eligible for the engine, and their Automation gate.",
  query: "jira.queue",
  events: [],
  empty: "Work queue is empty",
  columns: [
    { key: "key", label: "Key", mono: true },
    { key: "summary", label: "Summary" },
    { key: "status", label: "Status", render: (r) => badge(r.status) },
    { key: "automation", label: "Automation", render: (r) => badge(r.automation || "—", autoTone(r.automation)) },
  ],
});
