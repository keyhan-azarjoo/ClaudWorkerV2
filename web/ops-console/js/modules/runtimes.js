// AI Runtimes — live Claude Code runtime state from the Control Plane: active executions, selected
// accounts, durations, token estimates, retries, cooldowns, and failover events.

import { api, NotWired } from "api";
import { el, card, kpi, table, badge, sectionHead, emptyState, notWired, button } from "ui";

const classTone = (c) =>
  ({ success: "ok", rate_limit: "warn", timeout: "warn", cancellation: "info", semantic: "info", authentication: "danger", runtime_failure: "danger", infrastructure: "danger" }[c] || "");

export default {
  title: "AI Runtimes",
  async render(outlet, ctx) {
    const body = el("div");
    outlet.append(
      sectionHead("AI Runtimes", "Real Claude Code runtime — executions, accounts, cooldowns, failover.", button("Refresh", { ico: "refresh", onClick: () => load() })),
      body
    );

    async function load() {
      try {
        const s = (await api.query("runtime.state")) || {};
        const kpis = el(
          "div",
          { class: "grid cols-3 mb" },
          kpi({ label: "Active executions", value: s.active_executions || 0, ico: "runtimes", tone: "accent" }),
          kpi({ label: "Cooldowns", value: s.cooldowns || 0 }),
          kpi({ label: "Failover events", value: s.failover_events || 0 })
        );
        const rows = (s.recent || []).slice().reverse();
        const t = rows.length
          ? table(
              [
                { key: "issue", label: "Issue", mono: true },
                { key: "account", label: "Account", mono: true },
                { key: "runtime", label: "Runtime", render: (r) => badge(r.runtime) },
                { key: "class", label: "Outcome", render: (r) => badge(r.class, classTone(r.class)) },
                { key: "duration", label: "Duration", mono: true },
                { key: "token_estimate", label: "~Tokens", mono: true },
                { key: "retries", label: "Retries", mono: true },
              ],
              rows
            )
          : emptyState("No executions yet", "The runtime runs when the loop develops an assignment.");
        body.replaceChildren(kpis, card("Recent executions", t, { flush: true }));
      } catch (e) {
        body.replaceChildren(e instanceof NotWired ? notWired("runtime.state") : emptyState("Runtime state unavailable", e.message));
      }
    }

    await load();
    const off = ctx.stream.on((ev) => {
      if (ev.type === "RuntimeStarted" || ev.type === "RuntimeFinished" || ev.type === "RuntimeMetrics") load();
    });
    return () => off();
  },
};
