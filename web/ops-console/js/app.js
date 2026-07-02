// app.js — bootstraps the Operations Console shell: sidebar navigation, topbar, live connection
// indicator, the SSE stream, and the lazy router. Presentation + navigation + state wiring only.

import { EventStream } from "events";
import { store } from "store";
import * as router from "router";
import { el, icon, statusDot } from "ui";
import { api, config, setConfig } from "api";

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
      { path: "/credentials", label: "Credentials", ico: "settings" },
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
  "/credentials": () => import("./modules/credentials.js"),
  "/knowledge": () => import("./modules/knowledge.js"),
  "/metrics": () => import("./modules/metrics.js"),
  "/logs": () => import("./modules/logs.js"),
  "/settings": () => import("./modules/settings.js"),
};

// hasValidToken tests the stored token against the Control Plane (cheap, authenticated).
async function hasValidToken() {
  if (!config().token) return false;
  try {
    await api.status();
    return true;
  } catch {
    return false;
  }
}

// ensureAuth shows a login screen until a working access token is entered. This is what makes the
// site usable on a fresh device: without it the console just shows "offline" (401 on every call).
async function ensureAuth() {
  if (await hasValidToken()) return;
  await new Promise((resolve) => {
    const input = el("input", { type: "password", class: "login-input", placeholder: "Access token", autocomplete: "current-password" });
    const err = el("div", { class: "login-err" });
    const btn = el("button", { class: "btn primary login-btn" }, "Connect");
    async function attempt() {
      setConfig({ token: input.value.trim() });
      btn.textContent = "Connecting…";
      err.textContent = "";
      if (await hasValidToken()) {
        overlay.remove();
        resolve();
      } else {
        btn.textContent = "Connect";
        err.textContent = "That token didn’t work. Check it and try again.";
      }
    }
    btn.onclick = attempt;
    input.addEventListener("keydown", (e) => {
      if (e.key === "Enter") attempt();
    });
    const overlay = el(
      "div",
      { class: "login-overlay" },
      el(
        "div",
        { class: "login-card" },
        el("div", { class: "login-logo" }, "CW"),
        el("h2", {}, "ClaudWorker V2"),
        el("p", { class: "login-sub" }, "Enter your access token to connect."),
        input,
        btn,
        err
      )
    );
    document.body.append(overlay);
    setTimeout(() => input.focus(), 50);
  });
}

async function build() {
  // Apply the persisted theme (dark-first default set in index.html).
  const savedTheme = localStorage.getItem("oc.theme");
  if (savedTheme) document.documentElement.setAttribute("data-theme", savedTheme);

  await ensureAuth();

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
  const connText = el("span", { class: "conn-text" }, "connecting…");
  const conn = el("span", { class: "conn" }, connDot, connText);
  const menuBtn = el("button", { class: "btn ghost menu-toggle", onClick: () => app.classList.toggle("nav-open") }, "☰");

  // Manual Start/Stop control (idle-by-default): the platform never auto-processes; the operator
  // presses "Start Working" to claim + process the Jira queue, and "Stop" to return to idle.
  const workBadge = el("span", { class: "workstate" }, "…");
  const workBtn = el("button", { class: "btn" }, "Start Working");
  let workBusy = false;
  async function refreshWorkState() {
    try {
      const s = await api.status();
      const active = !!(s?.orchestrator?.active);
      workBadge.textContent = active ? "● Working" : "○ Idle";
      workBadge.className = "workstate " + (active ? "on" : "off");
      workBtn.textContent = active ? "Stop" : "Start Working";
      workBtn.className = "btn " + (active ? "danger" : "primary");
    } catch { workBadge.textContent = "?"; }
  }
  workBtn.onclick = async () => {
    if (workBusy) return; workBusy = true;
    try {
      const s = await api.status();
      const active = !!(s?.orchestrator?.active);
      await api.command(active ? "orchestrator.stop" : "orchestrator.start");
    } catch (e) { /* surfaced via refresh */ }
    workBusy = false;
    refreshWorkState();
  };
  const workctl = el("span", { class: "workctl" }, workBadge, workBtn);

  const topbar = el("header", { class: "topbar" }, menuBtn, title, el("span", { class: "spacer" }), workctl, conn);
  refreshWorkState();
  setInterval(refreshWorkState, 5000);

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
