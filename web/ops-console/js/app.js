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
      { path: "/devicelogs", label: "Device Logs", ico: "logs", countType: "DeviceFaultDetected" },
      { path: "/leases", label: "Leases", ico: "leases", countType: "LeaseGranted" },
      { path: "/git", label: "Git", ico: "projects", countType: "MergeCompleted" },
      { path: "/runtimes", label: "AI Runtimes", ico: "runtimes" },
      { path: "/accounts", label: "Accounts", ico: "accounts" },
      { path: "/credentials", label: "Credentials", ico: "settings" },
    ],
  },
  {
    group: "Knowledge",
    items: [
      { path: "/rules", label: "Rules", ico: "policies" },
      { path: "/knowledge", label: "Knowledge", ico: "knowledge" },
    ],
  },
  {
    group: "AI Workspace",
    items: [
      { path: "/aiw/dashboard", label: "Dashboard", ico: "dashboard" },
      { path: "/aiw/providers", label: "Providers", ico: "resources" },
      { path: "/aiw/workspaces", label: "Workspaces", ico: "projects" },
      { path: "/aiw/optimizers", label: "Optimizers", ico: "improvement" },
      { path: "/aiw/context", label: "Context", ico: "knowledge" },
      { path: "/aiw/knowledge", label: "Knowledge", ico: "knowledge" },
      { path: "/aiw/usage", label: "Usage", ico: "metrics" },
      { path: "/aiw/cache", label: "Cache", ico: "leases" },
    ],
  },
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
  "/devicelogs": () => import("./modules/devicelog.js"),
  "/leases": () => import("./modules/leases.js"),
  "/git": () => import("./modules/git.js"),
  "/runtimes": () => import("./modules/runtimes.js"),
  "/accounts": () => import("./modules/accounts.js"),
  "/credentials": () => import("./modules/credentials.js"),
  "/rules": () => import("./modules/rules.js"),
  "/knowledge": () => import("./modules/knowledge.js"),
  "/aiw/dashboard": () => import("./modules/aiw/dashboard.js"),
  "/aiw/providers": () => import("./modules/aiw/providers.js"),
  "/aiw/workspaces": () => import("./modules/aiw/workspaces.js"),
  "/aiw/optimizers": () => import("./modules/aiw/optimizers.js"),
  "/aiw/context": () => import("./modules/aiw/context.js"),
  "/aiw/knowledge": () => import("./modules/aiw/knowledge.js"),
  "/aiw/usage": () => import("./modules/aiw/usage.js"),
  "/aiw/cache": () => import("./modules/aiw/cache.js"),
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

// --- Multi-project switcher ---------------------------------------------------------------------
// Each project is its OWN isolated cwv2 instance, served under the SAME url at /p/<slug>/ (the root is
// the default project). Switching simply NAVIGATES to that project's path — its console then talks to
// its own backend and keeps its own token. Sub-projects are added with scripts/cwv2-new-project.sh.
function projectSwitcher() {
  const sel = el("select", { class: "project-select", title: "Switch project (each is a separate, isolated backend)" });
  const curBase = config().base;
  sel.append(el("option", { value: curBase }, "Loading…"));
  sel.onchange = () => {
    const base = sel.value;
    if (base !== curBase) location.href = (base || "") + "/";
  };
  api
    .query("projects.list")
    .then((list) => {
      const projects = Array.isArray(list) && list.length ? list : [{ name: "This project", base: curBase }];
      sel.replaceChildren();
      projects.forEach((p) => sel.append(el("option", { value: p.base || "" }, p.name)));
      if (!projects.some((p) => (p.base || "") === curBase)) sel.append(el("option", { value: curBase }, "Current"));
      sel.value = curBase;
    })
    .catch(() => {
      sel.replaceChildren(el("option", { value: curBase }, "This project"));
      sel.value = curBase;
    });
  return sel;
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
      el("div", { style: { minWidth: 0, flex: 1 } }, el("div", { class: "name" }, "ClaudWorker"), projectSwitcher())
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
  // Scrim behind the mobile nav drawer — tap outside to close.
  const scrim = el("div", { class: "nav-scrim", onClick: () => app.classList.remove("nav-open") });
  app.append(sidebar, main, scrim);

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
