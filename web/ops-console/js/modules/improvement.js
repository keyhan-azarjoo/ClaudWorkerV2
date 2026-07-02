import { listModule, badge } from "ui";

const statusTone = (s) => ({ passed: "ok", failed: "danger", escalated: "warn", deferred: "info", exhausted: "purple" }[s] || "");

export default listModule({
  title: "Improvement",
  desc: "The verify → improve → verify loop. The Policy Engine decides when to stop; never the loop.",
  query: "improvement.runs",
  events: ["VerificationFinished"],
  empty: "No improvement runs yet",
  columns: [
    { key: "assignment", label: "Assignment", mono: true },
    { key: "status", label: "Status", render: (r) => badge(r.status, statusTone(r.status)) },
    { key: "iterations", label: "Iterations", mono: true },
    { key: "final_outcome", label: "Final", render: (r) => badge(r.final_outcome) },
    { key: "changed_files", label: "Changed files", mono: true, render: (r) => (r.changed_files ? r.changed_files.length : 0) },
  ],
});
