import { listModule, badge } from "ui";

const healthTone = (h) => ({ healthy: "ok", degraded: "warn", down: "danger", unknown: "" }[h] || "");
const availTone = (a) => ({ available: "ok", reserved: "info", cooldown: "warn", offline: "danger" }[a] || "");

export default listModule({
  title: "Resources",
  desc: "The fleet — accounts, runtimes, devices, build machines, worktrees. Inventory only; ownership is under Leases.",
  query: "resources.snapshot",
  events: ["RuntimeStarted", "RuntimeFinished", "LeaseGranted", "LeaseExpired"],
  empty: "No resources registered",
  columns: [
    { key: "id", label: "ID", mono: true },
    { key: "kind", label: "Kind", render: (r) => badge(r.kind) },
    { key: "health", label: "Health", render: (r) => badge(r.health, healthTone(r.health)) },
    { key: "availability", label: "Availability", render: (r) => badge(r.availability, availTone(r.availability)) },
    { key: "usage", label: "Usage", mono: true, render: (r) => (r.metrics ? (r.metrics.usage_pct ?? 0) + "%" : "—") },
  ],
});
