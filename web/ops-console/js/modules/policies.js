import { listModule, badge, fmtTime } from "ui";

const decTone = (d) => ({ continue: "ok", retry: "ok", defer: "info", escalate: "warn", fail: "danger" }[d] || "");

export default listModule({
  title: "Policies",
  desc: "Deterministic policy decisions — retry, runtime selection, merge, budget, escalation and more.",
  query: "policies.decisions",
  events: ["PolicyDecision"],
  empty: "No policy decisions yet",
  columns: [
    { key: "time", label: "Time", mono: true, render: (r) => fmtTime(r.time) },
    { key: "policy", label: "Policy", render: (r) => badge(r.policy) },
    { key: "decision", label: "Decision", render: (r) => badge(r.decision, decTone(r.decision)) },
    { key: "reason", label: "Reason" },
  ],
});
