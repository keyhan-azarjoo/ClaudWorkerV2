// metrics.js — aggregated metrics from the Control Plane. Refreshes on demand and when an event
// arrives (metrics change after work happens); it does not poll on a timer.

import { api, NotWired } from "api";
import { el, card, sectionHead, emptyState, notWired, button } from "ui";

export default {
  title: "Metrics",
  async render(outlet, ctx) {
    const body = el("div");
    outlet.append(sectionHead("Metrics", "Live counters and rollups published by each subsystem.", button("Refresh", { ico: "refresh", onClick: () => load() })), body);

    async function load() {
      try {
        const m = (await api.metrics()) || {};
        const keys = Object.keys(m);
        if (!keys.length) {
          body.replaceChildren(emptyState("No metrics providers", "Subsystems register metrics with the Control Plane when the serve loop runs."));
          return;
        }
        body.replaceChildren(el("div", { class: "grid cols-3" }, ...keys.map((k) => card(k, renderMetric(m[k])))));
      } catch (e) {
        body.replaceChildren(e instanceof NotWired ? notWired("metrics") : emptyState("Metrics unavailable", e.message));
      }
    }

    function renderMetric(v) {
      if (v && typeof v === "object") {
        return el(
          "div",
          { class: "feed" },
          ...Object.entries(v).map(([k, val]) => el("div", { class: "feed-row" }, el("span", { class: "sys" }, k), el("span", { class: "mono", style: { marginLeft: "auto" } }, fmtVal(val))))
        );
      }
      return el("div", { class: "mono" }, fmtVal(v));
    }
    const fmtVal = (v) => (typeof v === "object" ? JSON.stringify(v) : String(v));

    await load();
    const off = ctx.stream.on(() => load());
    return () => off();
  },
};
