import { listModule, badge } from "ui";

const healthTone = (h) => ({ healthy: "ok", degraded: "warn", down: "danger" }[h] || "");

export default listModule({
  title: "Accounts",
  desc: "AI provider accounts — usage, pacing and cooldown. The pause decision lives in the Budget policy.",
  query: "accounts.list",
  events: ["RuntimeStarted", "RuntimeFinished"],
  empty: "No accounts registered",
  columns: [
    { key: "id", label: "Account", mono: true },
    { key: "kind", label: "Kind", render: (r) => badge(r.kind) },
    { key: "health", label: "Health", render: (r) => badge(r.health, healthTone(r.health)) },
    { key: "usage", label: "Usage", mono: true, render: (r) => (r.metrics ? (r.metrics.usage_pct ?? 0) + "%" : "—") },
    { key: "cooldown", label: "Cooldown", mono: true, render: (r) => (r.cooldown_until ? "until " + r.cooldown_until : "—") },
  ],
});
