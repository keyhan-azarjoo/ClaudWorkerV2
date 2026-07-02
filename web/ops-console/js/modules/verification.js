import { listModule, badge } from "ui";

const outcomeTone = (o) => ({ pass: "ok", fail: "danger", blocked: "warn", deferred: "info", inconclusive: "purple" }[o] || "");

export default listModule({
  title: "Verification",
  desc: "Capability-based verifier plugins — visual, unit, build, API, hardware and more.",
  query: "verification.recent",
  events: ["VerificationStarted", "VerificationFinished"],
  empty: "No verifications yet",
  columns: [
    { key: "verifier", label: "Verifier", mono: true },
    { key: "type", label: "Type", render: (r) => badge(r.type) },
    { key: "outcome", label: "Outcome", render: (r) => badge(r.outcome, outcomeTone(r.outcome)) },
    { key: "summary", label: "Summary" },
    { key: "duration", label: "Duration", mono: true, render: (r) => fmtDur(r.duration) },
  ],
});

function fmtDur(ns) {
  if (ns == null) return "—";
  const ms = ns / 1e6;
  return ms < 1000 ? Math.round(ms) + "ms" : (ms / 1000).toFixed(1) + "s";
}
