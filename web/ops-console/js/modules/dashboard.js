// dashboard.js — the landing page of the Operations Console. One glance: current work, resources,
// verification, improvements, AI usage and live activity. Everything reacts to SSE events.

import { api, NotWired } from "api";
import { el, card, kpi, badge, statusDot, sectionHead, emptyState, notWired, fmtTime, ago } from "ui";

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
      el("div", { class: "grid cols-2 mb" }, card("Live activity", feed, { flush: true, sub: "SSE", action: statusDot(store.get().connected ? "ok" : "danger", true) }), card("Current work", workBody, { sub: "assignments" })),
      card("System status", statusBody, { sub: "control plane" })
    );

    renderKpis();
    renderFeed();
    loadStatus();
    loadWork();

    // React to events (never poll).
    const off = stream.on((ev) => {
      renderKpis();
      renderFeed();
      if (ev.type.startsWith("Assignment")) loadWork();
    });
    const offStore = store.subscribe(() => {}); // keep store warm

    return () => {
      off();
      offStore();
    };
  },
};
