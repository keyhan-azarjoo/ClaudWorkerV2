import { listModule, badge } from "ui";

// Device Logs — ESP32 serial faults captured by the device monitor (backend:
// GET /v1/query/devicemonitor.faults). One row per connected board; re-queries
// live when a DeviceFaultDetected event arrives. A fault also opens a Jira Bug.
const tone = (lvl) =>
  lvl === "bug" ? "danger" : lvl === "warn" ? "warn" : lvl === "ok" ? "ok" : "";

export default listModule({
  title: "Device Logs",
  desc: "ESP32 boards watched over serial — boot, filesystem, crypto, heap and fault signals. A new fault opens a Jira Bug automatically.",
  query: "devicemonitor.faults",
  events: ["DeviceFaultDetected"],
  empty: "No devices monitored yet (waiting for the first capture cycle).",
  columns: [
    { key: "device", label: "Device", mono: true },
    { key: "level", label: "State", render: (r) => badge(r.level, tone(r.level)) },
    { key: "message", label: "Latest", render: (r) => badge(r.message, r.level === "bug" ? "danger" : r.level === "ok" ? "ok" : "") },
    { key: "heap", label: "Free heap", mono: true },
    { key: "checks", label: "Checks", mono: true },
    { key: "ticket", label: "Open faults / Jira", render: (r) => (r.ticket ? badge(r.ticket, "danger") : "—") },
    { key: "at", label: "Last", mono: true },
  ],
});
