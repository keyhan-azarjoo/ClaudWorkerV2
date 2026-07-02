// ui.js — reusable presentation components (a small design-system in code). No business logic; these
// only render data supplied by the Control Plane API.

import { api, NotWired } from "api";

// el(tag, props, ...children) — tiny hyperscript helper.
export function el(tag, props = {}, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (v == null) continue;
    if (k === "class") node.className = v;
    else if (k === "html") node.innerHTML = v;
    else if (k.startsWith("on") && typeof v === "function") node.addEventListener(k.slice(2).toLowerCase(), v);
    else if (k === "style" && typeof v === "object") Object.assign(node.style, v);
    else node.setAttribute(k, v);
  }
  for (const c of children.flat()) {
    if (c == null || c === false) continue;
    node.append(c.nodeType ? c : document.createTextNode(String(c)));
  }
  return node;
}

// Minimal stroke icon set (currentColor). Keeps the UI professional with no icon dependency.
const ICONS = {
  dashboard: "M3 3h8v8H3zM13 3h8v5h-8zM13 10h8v11h-8zM3 13h8v8H3z",
  projects: "M3 7h7l2 2h9v11H3zM3 7V4h5l2 2",
  jira: "M12 2 2 12l4 4 6-6 6 6 4-4z",
  assignments: "M9 3h6v3H9zM5 6h14v15H5zM8 11h8M8 15h5",
  verification: "M20 6 9 17l-5-5",
  improvement: "M3 17l6-6 4 4 8-8M14 6h7v7",
  policies: "M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6z",
  resources: "M4 5h16v6H4zM4 13h16v6H4zM8 8h.01M8 16h.01",
  leases: "M7 11V8a5 5 0 0110 0v3M5 11h14v10H5zM12 15v2",
  runtimes: "M8 5v14l11-7z",
  accounts: "M12 12a4 4 0 100-8 4 4 0 000 8zM4 21v-2a6 6 0 016-6h4a6 6 0 016 6v2",
  knowledge: "M4 5a2 2 0 012-2h13v16H6a2 2 0 00-2 2zM19 3v18",
  metrics: "M4 20V10M10 20V4M16 20v-7M22 20H2",
  logs: "M8 6h12M8 12h12M8 18h12M4 6h.01M4 12h.01M4 18h.01",
  settings: "M12 15a3 3 0 100-6 3 3 0 000 6zM19.4 15a7.9 7.9 0 000-6l1.6-1.2-2-3.4-1.9.8a8 8 0 00-5.2-3L11.5 0h-4l-.4 2.2a8 8 0 00-5.2 3L0 4.4l-2 3.4L-.4 9a7.9 7.9 0 000 6",
  refresh: "M21 12a9 9 0 11-3-6.7L21 8M21 3v5h-5",
};
export function icon(name, cls = "ico") {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "1.8");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  svg.setAttribute("class", cls);
  const p = document.createElementNS("http://www.w3.org/2000/svg", "path");
  p.setAttribute("d", ICONS[name] || ICONS.dashboard);
  svg.append(p);
  return svg;
}

export function card(title, body, opts = {}) {
  const head = title
    ? el("div", { class: "card-head" }, el("h3", {}, title), opts.sub ? el("span", { class: "sub" }, opts.sub) : null, opts.action || null)
    : null;
  return el("div", { class: "card" }, head, el("div", { class: "card-body" + (opts.flush ? " flush" : "") }, body));
}

export function kpi({ label, value, unit, foot, tone, ico }) {
  return el(
    "div",
    { class: "kpi" + (tone === "accent" ? " accent" : "") },
    el("div", { class: "label" }, ico ? icon(ico) : null, label),
    el("div", { class: "value" }, String(value), unit ? el("span", { class: "unit" }, " " + unit) : null),
    foot ? el("div", { class: "foot" }, foot) : null
  );
}

export function badge(text, tone) {
  return el("span", { class: "badge" + (tone ? " " + tone : "") }, text ?? "—");
}

export function statusDot(tone, pulse) {
  return el("span", { class: "dot" + (tone ? " " + tone : "") + (pulse ? " pulse" : "") });
}

// table(columns, rows) where columns = [{key,label,render?,mono?}].
export function table(columns, rows) {
  const thead = el("thead", {}, el("tr", {}, ...columns.map((c) => el("th", {}, c.label))));
  const trs = rows.map((r) =>
    el(
      "tr",
      {},
      ...columns.map((c) => {
        const v = c.render ? c.render(r) : r[c.key];
        const td = el("td", { class: c.mono ? "mono" : "" });
        if (v == null) td.textContent = "—";
        else if (v.nodeType) td.append(v);
        else td.textContent = String(v);
        return td;
      })
    )
  );
  // Wrap in a horizontal-scroll container so wide tables scroll inside the card on mobile instead of
  // scrolling the whole page.
  return el("div", { class: "tbl-wrap" }, el("table", { class: "tbl" }, thead, el("tbody", {}, ...trs)));
}

export function emptyState(big, hint) {
  return el("div", { class: "empty" }, el("div", { class: "big" }, big || "Nothing here yet"), hint ? el("div", { class: "hint" }, hint) : null);
}

export function notWired(name) {
  return el(
    "div",
    { class: "notice" },
    `No data source registered for "${name}". This module is ready — it will populate live once the engine's serve loop registers the query and starts publishing events on the Control Plane.`
  );
}

export function sectionHead(title, desc, action) {
  return el("div", { class: "section-head mb" }, el("h2", { style: { margin: 0, fontSize: "18px" } }, title), desc ? el("span", { class: "desc" }, desc) : null, action ? el("span", { style: { marginLeft: "auto" } }, action) : null);
}

export function button(label, { tone, ico, onClick } = {}) {
  return el("button", { class: "btn" + (tone ? " " + tone : ""), onClick }, ico ? icon(ico, "ico") : null, label);
}

// Time helpers
export function fmtTime(iso) {
  try {
    return new Date(iso).toLocaleTimeString();
  } catch {
    return iso || "—";
  }
}
export function ago(iso) {
  const t = new Date(iso).getTime();
  if (isNaN(t)) return "—";
  const s = Math.max(0, Math.floor((Date.now() - t) / 1000));
  if (s < 60) return s + "s ago";
  if (s < 3600) return Math.floor(s / 60) + "m ago";
  if (s < 86400) return Math.floor(s / 3600) + "h ago";
  return Math.floor(s / 86400) + "d ago";
}

// listModule — the reusable factory behind most console pages. It queries the Control Plane, renders a
// table, shows a friendly not-wired/empty state, and RE-QUERIES only when a relevant event arrives
// (event-driven, never polling).
export function listModule({ title, desc, query, columns, events = [], empty }) {
  return {
    title,
    async render(outlet, ctx) {
      const body = el("div");
      const head = sectionHead(title, desc, button("Refresh", { ico: "refresh", onClick: () => load() }));
      outlet.append(head, card(null, body, { flush: true }));

      async function load() {
        try {
          const rows = (await api.query(query)) || [];
          body.replaceChildren(rows.length ? table(columns, rows) : emptyState(empty || "No records", "Waiting for the engine to produce data."));
        } catch (e) {
          body.replaceChildren(e instanceof NotWired ? notWired(query) : emptyState("Could not load", e.message));
        }
      }
      await load();

      // React to events instead of polling.
      const off = ctx.stream.on((ev) => {
        if (events.includes(ev.type)) load();
      });
      return () => off();
    },
  };
}
