// accounts.js — AI provider accounts with operator control (V1 parity): pause/resume + real usage
// (5-hour session % and 7-day week %, with reset times). Usage is refreshed on demand (it probes the
// CLI, which is slow), so it may be blank until you press "Refresh usage".
import { api } from "api";
import { el, card, sectionHead, badge, table, button } from "ui";

const availTone = (a) => ({ available: "ok", paused: "warn", cooldown: "info", offline: "danger", reserved: "info" }[a] || "");
const healthTone = (h) => ({ healthy: "ok", degraded: "warn", down: "danger" }[h] || "");
const usageTone = (p) => (p >= 90 ? "danger" : p >= 70 ? "warn" : "ok");

// usageBar renders a V1-style progress bar: fill = % used (green/amber/red), with % left + reset time.
function usageBar(pct, reset, min) {
  if (pct == null) return el("span", { class: "sub" }, "—");
  const w = Math.min(100, Math.max(0, pct));
  const foot = reset ? `resets ${reset}` : min > 0 ? `${Math.floor(min / 60)}h ${min % 60}m` : "";
  return el(
    "div",
    { class: "usage" },
    el("div", { class: "usage-bar" }, el("div", { class: "usage-fill " + usageTone(pct), style: { width: w + "%" } })),
    el(
      "div",
      { class: "usage-meta" },
      el("span", { class: "usage-pct" }, `${pct}% used`),
      el("span", { class: "sub" }, `${100 - pct}% left${foot ? " · " + foot : ""}`)
    )
  );
}

export default {
  title: "Accounts",
  async render(outlet) {
    const body = el("div", {}, el("div", { class: "notice" }, "Loading…"));
    const refreshBtn = button("Refresh usage", { ico: "refresh", tone: "primary" });
    outlet.append(
      sectionHead("Accounts", "Claude accounts in rotation. Pause to keep one out of selection. Usage = real subscription %.", refreshBtn),
      card("Accounts", body)
    );

    async function load() {
      try {
        const [all, usage] = await Promise.all([
          api.query("resources.snapshot").catch(() => []),
          api.query("accounts.usage").catch(() => ({})),
        ]);
        const rows = (all || [])
          .filter((r) => r.kind === "claude_account")
          .map((r) => {
            const u = (usage || {})[r.name] || {};
            return {
              id: r.id,
              name: r.name,
              health: r.health,
              availability: r.availability,
              paused: r.availability === "paused",
              u,
            };
          });
        body.replaceChildren(
          table(
            [
              { key: "name", label: "Account", mono: true },
              { key: "health", label: "Health", render: (r) => badge(r.health, healthTone(r.health)) },
              { key: "availability", label: "State", render: (r) => badge(r.availability, availTone(r.availability)) },
              { key: "session", label: "5-hour", render: (r) => (r.u.ok ? usageBar(r.u.session_pct, r.u.session_reset, r.u.session_min) : el("span", { class: "sub" }, "—")) },
              { key: "week", label: "7-day", render: (r) => (r.u.ok ? usageBar(r.u.week_pct, r.u.week_reset, r.u.week_min) : el("span", { class: "sub" }, "—")) },
              {
                key: "action",
                label: "",
                render: (r) =>
                  button(r.paused ? "Resume" : "Pause", {
                    tone: r.paused ? "primary" : "",
                    onClick: async (e) => {
                      e.target.textContent = "…";
                      try {
                        await api.command(r.paused ? "accounts.resume" : "accounts.pause", { id: r.id });
                      } catch (err) {
                        body.prepend(el("div", { class: "notice danger" }, "Failed: " + (err && err.message ? err.message : err)));
                      }
                      load();
                    },
                  }),
              },
            ],
            rows
          )
        );
      } catch (e) {
        body.replaceChildren(el("div", { class: "notice danger" }, "Failed to load accounts: " + (e && e.message ? e.message : e)));
      }
    }

    refreshBtn.onclick = async () => {
      refreshBtn.textContent = "Refreshing…";
      refreshBtn.disabled = true;
      try {
        await api.command("accounts.usage.refresh");
      } catch (e) {
        body.prepend(el("div", { class: "notice danger" }, "Usage refresh failed: " + (e && e.message ? e.message : e)));
      }
      refreshBtn.textContent = "Refresh usage";
      refreshBtn.disabled = false;
      load();
    };

    load();
    // Keep the bars fresh while the page is open (the backend refreshes the real usage every 5 min).
    const timer = setInterval(load, 15000);
    return () => clearInterval(timer);
  },
};
