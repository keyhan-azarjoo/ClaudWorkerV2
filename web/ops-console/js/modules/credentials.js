// credentials.js — the Claude accounts and the secrets the platform can use (parity with V1's panel).
// Values are masked by default; "Reveal values" refetches with ?reveal=1. Owner-facing, auth-gated.
import { api } from "api";
import { el, card, sectionHead, button, badge, table } from "ui";

const healthTone = (h) => ({ healthy: "ok", degraded: "warn", down: "danger" }[h] || "");

export default {
  title: "Credentials",
  async render(outlet) {
    const body = el("div", {}, el("div", { class: "notice" }, "Loading…"));
    outlet.append(
      sectionHead(
        "Credentials",
        "Claude accounts and every secret the platform can resolve (as V1). Values are masked — click “Reveal values” to show them."
      ),
      body
    );

    let revealed = false;

    async function load() {
      body.replaceChildren(el("div", { class: "notice" }, "Loading…"));
      try {
        const res = await api.query("credentials.snapshot", revealed ? { reveal: "1" } : undefined);
        const d = (res && res.data) || {};
        const accounts = d.accounts || [];
        const secrets = d.secrets || [];

        const accCard = card(
          `Claude accounts (${accounts.length})`,
          table(
            [
              { key: "id", label: "Account", mono: true },
              { key: "name", label: "Name" },
              { key: "model", label: "Model", render: (r) => r.model || "—" },
              { key: "config_dir", label: "Config dir", mono: true, render: (r) => r.config_dir || "—" },
              { key: "health", label: "Health", render: (r) => badge(r.health, healthTone(r.health)) },
            ],
            accounts
          )
        );

        const revealBtn = button(revealed ? "Hide values" : "Reveal values", {
          tone: revealed ? "" : "primary",
          ico: revealed ? "lock" : "unlock",
          onClick: () => {
            revealed = !revealed;
            load();
          },
        });

        const secCard = card(
          `Secrets (${secrets.length})`,
          el(
            "div",
            {},
            el("div", { class: "row", style: "margin-bottom:10px" }, revealBtn, el("span", { class: "sub" }, revealed ? "Showing full values" : "Masked")),
            table(
              [
                { key: "name", label: "Name", mono: true },
                { key: "present", label: "Status", render: (r) => (r.present ? badge("set", "ok") : badge("missing", "danger")) },
                {
                  key: "value",
                  label: "Value",
                  mono: true,
                  render: (r) => el("span", { class: "mono" }, revealed && r.value !== undefined ? r.value : r.masked || "—"),
                },
              ],
              secrets
            )
          ),
          { sub: revealed ? "revealed" : "masked" }
        );

        body.replaceChildren(accCard, secCard);
      } catch (e) {
        body.replaceChildren(el("div", { class: "notice danger" }, "Failed to load credentials: " + (e && e.message ? e.message : e)));
      }
    }

    load();
  },
};
