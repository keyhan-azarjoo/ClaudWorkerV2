// logs.js — a live event log, driven purely by the SSE stream (via the shared store). No polling.
// Filterable by free text over type/subsystem.

import { el, table, badge, sectionHead, emptyState, fmtTime } from "ui";

export default {
  title: "Logs",
  async render(outlet, ctx) {
    const { store } = ctx;
    let filter = "";

    const input = el("input", { type: "search", placeholder: "Filter by type or subsystem…", oninput: (e) => { filter = e.target.value.toLowerCase(); render(); }, style: { minWidth: "260px" } });
    const body = el("div");
    outlet.append(sectionHead("Event log", "Every Control Plane event, newest first — streamed live.", input), el("div", { class: "card" }, body));

    function render() {
      const evs = store
        .get()
        .events.filter((ev) => !filter || (ev.type + " " + (ev.subsystem || "")).toLowerCase().includes(filter))
        .slice(-300)
        .reverse();
      if (!evs.length) {
        body.replaceChildren(emptyState("No events", filter ? "No events match the filter." : "Events appear here as the engine runs."));
        return;
      }
      body.replaceChildren(
        table(
          [
            { key: "seq", label: "#", mono: true },
            { key: "time", label: "Time", mono: true, render: (r) => fmtTime(r.time) },
            { key: "subsystem", label: "Subsystem", render: (r) => badge(r.subsystem || "—") },
            { key: "type", label: "Type", render: (r) => el("span", { style: { fontWeight: 600 } }, r.type) },
            { key: "data", label: "Data", mono: true, render: (r) => (r.data == null ? "—" : truncate(JSON.stringify(r.data), 80)) },
          ],
          evs
        )
      );
    }

    const truncate = (s, n) => (s.length > n ? s.slice(0, n) + "…" : s);

    render();
    const off = store.subscribe(() => render());
    return () => off();
  },
};
