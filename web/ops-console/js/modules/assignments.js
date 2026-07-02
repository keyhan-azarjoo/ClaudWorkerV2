import { listModule, badge } from "ui";

const stateTone = (s) => ({ done: "ok", failed: "danger", merging: "info", qa: "purple", developing: "warn", claimed: "" }[s] || "");

export default listModule({
  title: "Assignments",
  desc: "Every Jira issue in flight — the deterministic execution unit.",
  query: "assignments.list",
  events: ["AssignmentCreated", "AssignmentCompleted"],
  empty: "No assignments in flight",
  columns: [
    { key: "issue_key", label: "Issue", mono: true },
    { key: "state", label: "State", render: (r) => badge(r.state, stateTone(r.state)) },
    { key: "attempt", label: "Attempt", mono: true },
    { key: "spec_version", label: "Spec", mono: true },
  ],
});
