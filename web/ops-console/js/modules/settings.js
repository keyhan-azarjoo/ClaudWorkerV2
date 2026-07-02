// settings.js — connection to the Control Plane (base URL + token), theme, and API discovery.
// Presentation + local preferences only; no business logic.

import { api, config, setConfig, NotWired } from "api";
import { el, card, sectionHead, button, badge, emptyState } from "ui";

export default {
  title: "Settings",
  async render(outlet) {
    const cfg = config();

    const baseInput = el("input", { type: "text", value: cfg.base, placeholder: "https://engine.local:8080 (blank = same origin)" });
    const tokenInput = el("input", { type: "password", value: cfg.token, placeholder: "Bearer token (optional)" });
    const result = el("span");

    const connCard = card(
      "Control Plane connection",
      el(
        "div",
        {},
        field("API base URL", baseInput),
        field("API token", tokenInput),
        el(
          "div",
          { class: "row" },
          button("Save", { tone: "primary", onClick: () => { setConfig({ base: baseInput.value.trim(), token: tokenInput.value.trim() }); flash(result, "Saved", "ok"); } }),
          button("Test connection", { ico: "refresh", onClick: test }),
          result
        )
      )
    );

    // Theme
    const themeSel = el("select", { onchange: (e) => setTheme(e.target.value) });
    for (const t of ["dark", "light"]) themeSel.append(el("option", { value: t, selected: current() === t ? "" : null }, t));
    const themeCard = card("Appearance", field("Theme (dark-first)", themeSel));

    // Discovery
    const discBody = el("div", {}, el("div", { class: "notice" }, "Loading available queries and commands…"));
    const discCard = card("API surface", discBody, { sub: "discovery" });

    outlet.append(sectionHead("Settings", "Connect the console to a Control Plane. Everything else flows through its API."), el("div", { class: "grid cols-2 mb" }, connCard, themeCard), discCard);

    loadDiscovery();

    async function test() {
      setConfig({ base: baseInput.value.trim(), token: tokenInput.value.trim() });
      try {
        await api.health();
        flash(result, "Connected ✓", "ok");
        loadDiscovery();
      } catch (e) {
        flash(result, "Failed: " + e.message, "danger");
      }
    }

    async function loadDiscovery() {
      try {
        const [q, c] = await Promise.all([api.queries().catch(() => []), api.commands().catch(() => [])]);
        discBody.replaceChildren(
          el("div", { class: "grid cols-2" },
            listBlock("Queries", q),
            listBlock("Commands", c))
        );
      } catch (e) {
        discBody.replaceChildren(e instanceof NotWired ? el("div", { class: "notice" }, "Discovery not available.") : emptyState("Unavailable", e.message));
      }
    }
  },
};

function field(label, input) {
  return el("div", { class: "field" }, el("label", {}, label), input);
}
function listBlock(title, items) {
  return el("div", {}, el("div", { class: "nav-group-label" }, title + ` (${items.length})`), items.length ? el("div", { class: "feed" }, ...items.map((n) => el("div", { class: "feed-row" }, el("span", { class: "mono" }, n)))) : el("div", { class: "notice" }, "None registered yet."));
}
function flash(node, msg, tone) {
  node.replaceChildren(badge(msg, tone));
}
function current() {
  return document.documentElement.getAttribute("data-theme") || "dark";
}
function setTheme(t) {
  document.documentElement.setAttribute("data-theme", t);
  localStorage.setItem("oc.theme", t);
}
