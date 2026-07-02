// accounts.js — AI provider accounts with operator control (V1 parity): pause an account to keep it
// out of rotation, resume to bring it back. State comes from resources.snapshot (derived availability).
import { api } from "api";
import { el, card, sectionHead, badge, table, button } from "ui";

const availTone = (a) => ({ available: "ok", paused: "warn", cooldown: "info", offline: "danger", reserved: "info" }[a] || "");
const healthTone = (h) => ({ healthy: "ok", degraded: "warn", down: "danger" }[h] || "");

export default {
  title: "Accounts",
  async render(outlet) {
    const body = el("div", {}, el("div", { class: "notice" }, "Loading…"));
    outlet.append(
      sectionHead("Accounts", "Claude accounts in rotation. Pause one to keep it out of selection; resume to bring it back (like V1)."),
      card("Accounts", body)
    );

    async function load() {
      try {
        const all = (await api.query("resources.snapshot")) || [];
        const rows = all
          .filter((r) => r.kind === "claude_account")
          .map((r) => ({
            id: r.id,
            model: (r.labels && r.labels.model) || "—",
            health: r.health,
            availability: r.availability,
            paused: r.availability === "paused",
          }));
        body.replaceChildren(
          table(
            [
              { key: "id", label: "Account", mono: true },
              { key: "model", label: "Model" },
              { key: "health", label: "Health", render: (r) => badge(r.health, healthTone(r.health)) },
              { key: "availability", label: "State", render: (r) => badge(r.availability, availTone(r.availability)) },
              {
                key: "action",
                label: "",
                render: (r) =>
                  button(r.paused ? "Resume" : "Pause", {
                    tone: r.paused ? "primary" : "",
                    onClick: async (e) => {
                      const b = e.target;
                      b.textContent = "…";
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
    load();
  },
};
