// aiw/usage.js — token usage analytics. Daily trend + breakdowns by optimizer and provider over a
// selectable range. All from recorded usage events; provider/model breakdowns stay empty until the
// optimizing proxy records inference (honest, not faked).
import { api } from "api";
import { el, card, sectionHead } from "ui";
import { sparkline, hbars, fmtTokens } from "./shared/charts.js";

const RANGES = [
  { key: 7, label: "7 days" },
  { key: 30, label: "30 days" },
  { key: 90, label: "90 days" },
];

function metric(n, label, accent) {
  return el("div", { class: "aiw-metric" + (accent ? " accent" : "") }, el("span", { class: "n" }, fmtTokens(n)), el("span", { class: "l" }, label));
}

export default {
  title: "Usage",
  async render(outlet) {
    let range = 30;
    const body = el("div", {}, el("div", { class: "notice" }, "Loading…"));
    const chips = el(
      "div",
      { class: "aiw-chips" },
      ...RANGES.map((r) => el("button", { class: "aiw-chip" + (r.key === range ? " on" : ""), onClick: () => { range = r.key; sync(); load(); } }, r.label))
    );
    function sync() {
      [...chips.children].forEach((ch, i) => ch.classList.toggle("on", RANGES[i].key === range));
    }
    outlet.append(sectionHead("Usage", "Token usage and savings over time. Estimated from recorded events.", null), chips, body);

    async function load() {
      let s;
      try {
        s = await api.query("aiw.usage.series", { range });
      } catch (e) {
        body.replaceChildren(el("div", { class: "notice danger" }, "Failed: " + (e && e.message ? e.message : e)));
        return;
      }
      const days = (s.days || []).map((p) => p.tokens);
      const saved = (s.days || []).map((p) => p.saved);
      body.replaceChildren(
        el("div", { class: "aiw-usage-nums" }, metric(s.totalTokens, "tokens (range)"), metric(s.totalSaved, "saved (est)", true), metric(s.events, "events")),
        card("Daily tokens", el("div", {}, sparkline(days, { w: 640, h: 60 }), el("div", { class: "aiw-usage-foot" }, "processed tokens per day"))),
        card("Daily saved", el("div", {}, sparkline(saved.length ? saved : [0], { w: 640, h: 60, tone: "ok" }), el("div", { class: "aiw-usage-foot" }, "tokens saved by optimizers/cache per day"))),
        el(
          "div",
          { class: "aiw-cols" },
          card("Saved by optimizer", hbars((s.byOptimizer || []).map((x) => ({ label: x.name, value: x.value })), { emptyText: "Run some optimizers to see savings." })),
          card("Tokens by provider", hbars((s.byProvider || []).map((x) => ({ label: x.name, value: x.value })), { emptyText: "Populates once the proxy records inference." }))
        )
      );
    }
    load();
  },
};
