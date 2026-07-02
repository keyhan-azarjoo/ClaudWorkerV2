// accounts.js — AI provider accounts with operator control (V1 parity): pause/resume + real usage
// (5-hour session % and 7-day week %, with reset times). Usage is refreshed on demand (it probes the
// CLI, which is slow), so it may be blank until you press "Refresh usage".
import { api } from "api";
import { el, card, sectionHead, badge, table, button } from "ui";

const availTone = (a) => ({ available: "ok", paused: "warn", cooldown: "info", offline: "danger", reserved: "info" }[a] || "");
const healthTone = (h) => ({ healthy: "ok", degraded: "warn", down: "danger" }[h] || "");
const usageTone = (p) => (p >= 90 ? "danger" : p >= 70 ? "warn" : "ok");

function usageCell(pct, reset, min) {
  if (pct == null) return el("span", { class: "sub" }, "—");
  const left = 100 - pct;
  const foot = reset ? ` · resets ${reset}` : min > 0 ? ` · ${min}m` : "";
  return el("span", {}, badge(`${pct}% used`, usageTone(pct)), el("span", { class: "sub" }, ` ${left}% left${foot}`));
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
              { key: "session", label: "5-hour", render: (r) => (r.u.ok ? usageCell(r.u.session_pct, r.u.session_reset, r.u.session_min) : el("span", { class: "sub" }, "—")) },
              { key: "week", label: "7-day", render: (r) => (r.u.ok ? usageCell(r.u.week_pct, r.u.week_reset, r.u.week_min) : el("span", { class: "sub" }, "—")) },
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
      refreshBtn.textContent = "Refreshing… (~30s)";
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
  },
};
