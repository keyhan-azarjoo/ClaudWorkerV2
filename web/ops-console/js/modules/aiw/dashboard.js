// aiw/dashboard.js — AI Workspace overview. Live tiles for the current provider/model/workspace, proxy
// and health; today/month token usage with a 14-day sparkline; compression + cache-hit ratio donuts.
// Everything is real Control-Plane data (aiw.dashboard.summary); values not yet produced show honest
// zero/absent states until their phase lands. Auto-refreshes while the page is open.
import { api, NotWired } from "api";
import { el, card, sectionHead, badge, button, emptyState, notWired } from "ui";
import { sparkline, donut, fmtTokens } from "./shared/charts.js";

function tile(label, value, sub, tone) {
  return el(
    "div",
    { class: "aiw-tile" + (tone ? " " + tone : "") },
    el("div", { class: "aiw-tile-label" }, label),
    el("div", { class: "aiw-tile-value" }, value ?? "—"),
    sub ? el("div", { class: "aiw-tile-sub" }, sub) : null
  );
}

export default {
  title: "AI Workspace",
  async render(outlet, ctx) {
    const grid = el("div", { class: "aiw-dash" }, el("div", { class: "notice" }, "Loading…"));
    outlet.append(
      sectionHead("AI Workspace", "Optimize AI usage, manage providers, and track tokens — all local-first.", button("Providers →", { tone: "primary", onClick: () => (location.hash = "#/aiw/providers") })),
      grid
    );

    async function load() {
      let d;
      try {
        d = await api.query("aiw.dashboard.summary");
      } catch (e) {
        grid.replaceChildren(e instanceof NotWired ? notWired("aiw.dashboard.summary") : emptyState("Could not load", e.message));
        return;
      }
      const u = d.usage || {};
      const days = (u.days || []).map((p) => p.tokens);
      const hasProviders = (d.providersCount || 0) > 0;

      const tiles = el(
        "div",
        { class: "aiw-tiles" },
        tile("Provider", d.provider, d.providerLocal ? "local · free" : (hasProviders ? "cloud" : "none configured"), d.providerLocal ? "ok" : ""),
        tile("Model", d.model),
        tile("Workspace", d.workspace, "coming soon"),
        tile("Proxy", (d.proxy && d.proxy.running) ? "● on" : "○ off", d.companion && d.companion.present ? "companion up" : "no companion"),
        tile("Health", d.health === "ok" ? "● good" : (d.health === "setup" ? "○ setup" : "● " + d.health), hasProviders ? d.enabledCount + " enabled" : "add a provider", d.health === "ok" ? "ok" : (d.health === "setup" ? "warn" : "danger"))
      );

      const usageCard = card(
        "Token usage",
        el(
          "div",
          { class: "aiw-usage" },
          el(
            "div",
            { class: "aiw-usage-nums" },
            el("div", { class: "aiw-metric" }, el("span", { class: "n" }, fmtTokens(u.todayTokens)), el("span", { class: "l" }, "today")),
            el("div", { class: "aiw-metric" }, el("span", { class: "n" }, fmtTokens(u.monthTokens)), el("span", { class: "l" }, "this month")),
            el("div", { class: "aiw-metric accent" }, el("span", { class: "n" }, "~" + fmtTokens(u.monthSaved)), el("span", { class: "l" }, "saved (est)"))
          ),
          sparkline(days, { w: 320, h: 52 }),
          el("div", { class: "aiw-usage-foot" }, (u.events || 0) === 0 ? "No usage recorded yet — optimizers and the proxy will populate this." : (u.events + " events · 14-day trend"))
        ),
        { sub: "estimated" }
      );

      const ratioCard = card(
        "Optimization",
        el(
          "div",
          { class: "aiw-ratios" },
          donut(d.compressionRatio || 0, { label: "Compression" }),
          donut(d.cacheHitRatio || 0, { label: "Cache hit" })
        ),
        { sub: "arrives with Optimizers + Cache" }
      );

      grid.replaceChildren(tiles, el("div", { class: "aiw-cols" }, usageCard, ratioCard));
      if (!hasProviders) {
        grid.append(el("div", { class: "aiw-cta" }, "No providers yet. ", el("a", { href: "#/aiw/providers" }, "Add your first provider"), " to get started — the local Ollama option is free."));
      }
    }

    await load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  },
};
