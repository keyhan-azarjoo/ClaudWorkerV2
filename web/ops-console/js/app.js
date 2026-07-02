// app.js — bootstraps the Operations Console shell: sidebar navigation, topbar, live connection
// indicator, the SSE stream, and the lazy router. Presentation + navigation + state wiring only.

import { EventStream } from "events";
import { store } from "store";
import * as router from "router";
import { el, icon, statusDot } from "ui";

// Navigation model. The Dashboard is only the landing page of the console.
const NAV = [
  { group: "Overview", items: [{ path: "/dashboard", label: "Dashboard", ico: "dashboard" }] },
  {
    group: "Work",
    items: [
      { path: "/projects", label: "Projects", ico: "projects" },
      { path: "/jira", label: "Jira", ico: "jira" },
      { path: "/assignments", label: "Assignments", ico: "assignments", countType: "AssignmentCreated" },
    ],
  },
  {
    group: "Quality",
    items: [
      { path: "/verification", label: "Verification", ico: "verification", countType: "VerificationFinished" },
      { path: "/improvement", label: "Improvement", ico: "improvement" },
      { path: "/policies", label: "Policies", ico: "policies" },
    ],
  },
  {
    group: "Infrastructure",
    items: [
      { path: "/resources", label: "Resources", ico: "resources" },
      { path: "/leases", label: "Leases", ico: "leases", countType: "LeaseGranted" },
      { path: "/git", label: "Git", ico: "projects", countType: "MergeCompleted" },
      { path: "/runtimes", label: "AI Runtimes", ico: "runtimes" },
      { path: "/accounts", label: "Accounts", ico: "accounts" },
    ],
  },
  { group: "Knowledge", items: [{ path: "/knowledge", label: "Knowledge", ico: "knowledge" }] },
  {
    group: "System",
    items: [
      { path: "/metrics", label: "Metrics", ico: "metrics" },
      { path: "/logs", label: "Logs", ico: "logs" },
      { path: "/settings", label: "Settings", ico: "settings" },
    ],
  },
];

// Lazy route registration — each module imported only when first visited.
const MODULES = {
  "/dashboard": () => import("./modules/dashboard.js"),
  "/projects": () => import("./modules/projects.js"),
  "/jira": () => import("./modules/jira.js"),
  "/assignments": () => import("./modules/assignments.js"),
  "/verification": () => import("./modules/verification.js"),
  "/improvement": () => import("./modules/improvement.js"),
  "/policies": () => import("./modules/policies.js"),
  "/resources": () => import("./modules/resources.js"),
  "/leases": () => import("./modules/leases.js"),
  "/git": () => import("./modules/git.js"),
  "/runtimes": () => import("./modules/runtimes.js"),
  "/accounts": () => import("./modules/accounts.js"),
  "/knowledge": () => import("./modules/knowledge.js"),
  "/metrics": () => import("./modules/metrics.js"),
  "/logs": () => import("./modules/logs.js"),
  "/settings": () => import("./modules/settings.js"),
};

function build() {
  // Apply the persisted theme (dark-first default set in index.html).
  const savedTheme = localStorage.getItem("oc.theme");
  if (savedTheme) document.documentElement.setAttribute("data-theme", savedTheme);

  const app = document.getElementById("app");
  app.removeAttribute("aria-busy");
  app.replaceChildren();

  // Sidebar
  const navEl = el("nav", { class: "nav" });
  const navRefs = [];
  for (const g of NAV) {
    navEl.append(el("div", { class: "nav-group-label" }, g.group));
    for (const it of g.items) {
      const count = el("span", { class: "count" });
      const node = el(
        "a",
        { class: "nav-item", href: "#" + it.path },
        icon(it.ico),
        el("span", {}, it.label),
        count
      );
      navRefs.push({ path: it.path, node, count, countType: it.countType });
      navEl.append(node);
    }
  }

  const sidebar = el(
    "aside",
    { class: "sidebar" },
    el(
      "div",
      { class: "brand" },
      el("div", { class: "logo" }, "CW"),
      el("div", {}, el("div", { class: "name" }, "ClaudWorker"), el("div", { class: "sub" }, "Operations Console"))
    ),
    navEl,
    el("div", { class: "sidebar-foot" }, "Control Plane client · v0.1")
  );

  // Topbar
  const title = el("h1", {}, "Dashboard");
  const connDot = statusDot("", true);
  const connText = el("span", {}, "connecting…");
  const conn = el("span", { class: "conn" }, connDot, connText);
  const menuBtn = el("button", { class: "btn ghost menu-toggle", onClick: () => app.classList.toggle("nav-open") }, "☰");
  const topbar = el("header", { class: "topbar" }, menuBtn, title, el("span", { class: "spacer" }), conn);

  const outlet = el("div", { class: "content" });
  const main = el("main", { class: "main" }, topbar, outlet);
  app.append(sidebar, main);

  // Live connection state
  const stream = new EventStream();
  stream.onState((connected) => {
    store.setConnected(connected);
    connDot.className = "dot " + (connected ? "ok" : "danger") + (connected ? "" : " pulse");
    connText.textContent = connected ? "live" : "offline";
  });
  // Feed every event into the shared store (Logs/Dashboard read from it) + update nav counts.
  stream.on((ev) => {
    store.pushEvent(ev);
    for (const r of navRefs) {
      if (r.countType) {
        const c = store.get().counts[r.countType] || 0;
        r.count.textContent = c ? String(c) : "";
      }
    }
  });

  // Router
  router.setContext({ stream, store });
  for (const [path, importer] of Object.entries(MODULES)) router.register(path, importer);
  router.start({
    mount: outlet,
    onRouteChange: (path) => {
      app.classList.remove("nav-open");
      for (const r of navRefs) r.node.classList.toggle("active", r.path === path);
      const item = NAV.flatMap((g) => g.items).find((i) => i.path === path);
      title.textContent = item ? item.label : "Operations Console";
    },
  });

  stream.start();
}

build();
