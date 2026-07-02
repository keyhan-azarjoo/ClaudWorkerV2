import { listModule, badge, statusDot, el } from "ui";

export default listModule({
  title: "AI Runtimes",
  desc: "Reasoning-engine providers behind the Worker Runtime port — Claude first; Codex/GPT/Gemini/local next.",
  query: "runtimes.list",
  events: ["RuntimeStarted", "RuntimeFinished"],
  empty: "No runtimes registered",
  columns: [
    { key: "name", label: "Runtime", mono: true },
    { key: "provider", label: "Provider", render: (r) => badge(r.provider || r.name) },
    { key: "capabilities", label: "Capabilities", render: (r) => (r.capabilities || []).join(", ") || "—" },
    {
      key: "status",
      label: "Status",
      render: (r) => el("span", { class: "row" }, statusDot(r.busy ? "warn" : "ok"), r.busy ? "busy" : "idle"),
    },
  ],
});
