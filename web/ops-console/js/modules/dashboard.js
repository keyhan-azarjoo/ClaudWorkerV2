// dashboard.js — the landing page of the Operations Console. One glance: current work, resources,
// verification, improvements, AI usage and live activity. Everything reacts to SSE events.

import { api, NotWired } from "api";
import { el, card, kpi, badge, statusDot, sectionHead, emptyState, notWired, fmtTime, ago } from "ui";

// State → badge tone (matches assignments.js so the whole console reads the same).
const stateTone = (s) => ({ done: "ok", failed: "danger", merging: "info", qa: "warn", verifying: "warn", developing: "warn", claimed: "" }[s] || "");
// Action status → glyph + tone for the per-task timeline.
const actionGlyph = { done: { text: "✓", tone: "ok" }, running: { text: "running", tone: "warn" }, failed: { text: "✗", tone: "danger" } };
const hhmmss = (iso) => String(iso || "").slice(11, 19) || "—";

// One task box: header (issue + state badge + account) and the ordered action timeline.
function taskBox(t, agentCount) {
  const actions = Array.isArray(t.actions) ? t.actions : [];
  // Currently-running action = the LAST action whose status is "running".
  let running = null;
  for (const a of actions) if (a && a.status === "running") running = a;

  const head = el(
    "div",
    { class: "task-box-head" },
    el("span", { class: "task-issue mono" }, t.issue || "—"),
    badge(t.state || "—", stateTone(t.state)),
    agentCount > 0 ? el("span", { class: "task-agents" }, "⚡ " + agentCount + " agent" + (agentCount === 1 ? "" : "s")) : null,
    t.account ? el("span", { class: "task-acct" }, "on " + t.account) : null
  );

  const nowLine = running ? el("div", { class: "task-now running" }, "▶ now: " + (running.stage || "—")) : null;

  const rows = actions.map((a) => {
    const g = actionGlyph[a.status] || { text: a.status || "?", tone: "" };
    const isRunning = a.status === "running";
    return el(
      "div",
      { class: "task-action" + (isRunning ? " running" : "") },
      badge(g.text, g.tone),
      el("span", { class: "task-stage" }, a.stage || "—"),
      a.detail ? el("span", { class: "task-detail" }, a.detail) : null,
      el("span", { class: "task-time mono" }, hhmmss(a.at))
    );
  });

  const body = el("div", { class: "task-actions" }, ...(rows.length ? rows : [el("span", { class: "task-detail" }, "No actions yet")]));

  return el("div", { class: "task-box" }, head, nowLine, body);
}

export default {
  title: "Dashboard",
  async render(outlet, ctx) {
    const { stream, store } = ctx;

    // KPI tiles (derived live from the event stream — no polling).
    const kAssign = el("div");
    const kVerify = el("div");
    const kLease = el("div");
    const kEvents = el("div");
    const kpiGrid = el("div", { class: "grid cols-4 mb" }, kAssign, kVerify, kLease, kEvents);

    function renderKpis() {
      const c = store.get().counts;
      kAssign.replaceChildren(kpi({ label: "Assignments", ico: "assignments", value: c.AssignmentCreated || 0, foot: `${c.AssignmentCompleted || 0} completed`, tone: "accent" }));
      kVerify.replaceChildren(kpi({ label: "Verifications", ico: "verification", value: c.VerificationFinished || 0, foot: `${c.VerificationStarted || 0} started` }));
      kLease.replaceChildren(kpi({ label: "Leases granted", ico: "leases", value: c.LeaseGranted || 0, foot: `${c.LeaseExpired || 0} expired` }));
      kEvents.replaceChildren(kpi({ label: "Events (session)", ico: "metrics", value: store.get().events.length, foot: store.get().connected ? "live" : "offline" }));
    }

    // Live activity feed.
    const feed = el("div", { class: "feed" });
    function renderFeed() {
      const evs = store.get().events.slice(-14).reverse();
      if (!evs.length) {
        feed.replaceChildren(emptyState("No activity yet", "Events from the engine appear here in real time."));
        return;
      }
      feed.replaceChildren(
        ...evs.map((ev) =>
          el("div", { class: "feed-row" }, el("span", { class: "t" }, fmtTime(ev.time)), el("span", { class: "sys" }, ev.subsystem || "—"), el("span", { class: "type" }, ev.type))
        )
      );
    }

    // System status (from the Control Plane aggregate).
    const statusBody = el("div");
    async function loadStatus() {
      try {
        const s = (await api.status()) || {};
        const keys = Object.keys(s);
        statusBody.replaceChildren(
          keys.length
            ? el("div", { class: "grid cols-2" }, ...keys.map((k) => card(k, el("pre", { class: "mono", style: { margin: 0, whiteSpace: "pre-wrap", fontSize: "12px" } }, JSON.stringify(s[k], null, 2)))))
            : emptyState("No status providers", "Subsystems register status with the Control Plane at serve time.")
        );
      } catch (e) {
        statusBody.replaceChildren(e instanceof NotWired ? notWired("status") : emptyState("Status unavailable", e.message));
      }
    }

    // Per-task activity (one box per task, with its action timeline + currently-running action).
    const tasksBody = el("div");
    async function loadTasks() {
      try {
        const [tasks, agents] = await Promise.all([
          api.query("tasks.activity").then((x) => x || []),
          api.query("tasks.agents").catch(() => ({})),
        ]);
        tasksBody.replaceChildren(
          tasks.length
            ? el("div", { class: "task-grid" }, ...tasks.map((t) => taskBox(t, (agents || {})[t.issue] || 0)))
            : emptyState("No tasks yet", "Press Run on a Jira ticket or Start Working.")
        );
      } catch (e) {
        tasksBody.replaceChildren(e instanceof NotWired ? notWired("tasks.activity") : emptyState("Unavailable", e.message));
      }
    }

    // Current work (assignments).
    const workBody = el("div");
    async function loadWork() {
      try {
        const rows = (await api.query("assignments.list")) || [];
        workBody.replaceChildren(
          rows.length
            ? el("div", { class: "feed" }, ...rows.slice(0, 8).map((r) => el("div", { class: "feed-row" }, el("span", { class: "sys" }, r.issue_key), badge(r.state), el("span", { class: "t" }, "attempt " + r.attempt))))
            : emptyState("No assignments in flight", "The engine claims Jira issues when the serve loop runs.")
        );
      } catch (e) {
        workBody.replaceChildren(e instanceof NotWired ? notWired("assignments.list") : emptyState("Unavailable", e.message));
      }
    }

    outlet.append(
      sectionHead("Operations overview", "The whole engineering system at a glance — live."),
      kpiGrid,
      card("Tasks", tasksBody, { flush: true, sub: "per-task activity" }),
      el("div", { class: "grid cols-2 mb" }, card("Live activity", feed, { flush: true, sub: "SSE", action: statusDot(store.get().connected ? "ok" : "danger", true) }), card("Current work", workBody, { sub: "assignments" })),
      card("System status", statusBody, { sub: "control plane" })
    );

    renderKpis();
    renderFeed();
    loadStatus();
    loadWork();
    loadTasks();

    // React to events (never poll).
    const off = stream.on((ev) => {
      renderKpis();
      renderFeed();
      if (ev.type.startsWith("Assignment")) loadWork();
      // Re-render the Tasks section on every event so a task's timeline updates live.
      try {
        loadTasks();
      } catch (e) {
        /* keep the dashboard alive even if a reload throws */
      }
    });
    const offStore = store.subscribe(() => {}); // keep store warm

    return () => {
      off();
      offStore();
    };
  },
};
