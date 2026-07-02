import { listModule, badge } from "ui";

export default listModule({
  title: "Leases",
  desc: "Time-bounded ownership — who owns what, until when. Expired leases reclaim automatically.",
  query: "leases.active",
  events: ["LeaseGranted", "LeaseExpired"],
  empty: "No active leases",
  columns: [
    { key: "kind", label: "Kind", render: (r) => badge(r.kind, "info") },
    { key: "resource", label: "Resource", mono: true },
    { key: "owner", label: "Owner", mono: true },
    { key: "expires_at", label: "Expires", mono: true },
    { key: "renewable", label: "Renewable", render: (r) => badge(r.renewable ? "yes" : "no", r.renewable ? "ok" : "") },
    { key: "reason", label: "Reason" },
  ],
});
