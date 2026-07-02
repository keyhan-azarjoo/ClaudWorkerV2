// credentials.js — CREDENTIAL HEALTH dashboard. Shows name, source, subsystem, status, validation
// and last-validated time. It NEVER shows a secret value: there is no reveal, no copy, no masked
// output. A "Validate" action confirms credentials work (live Jira/GitHub checks) without exposing
// them. Secret values exist only inside the runtime's resolver.
import { api } from "api";
import { el, card, sectionHead, button, badge, table } from "ui";

const healthTone = (h) => ({ healthy: "ok", degraded: "warn", down: "danger" }[h] || "");
const statusTone = (s) => ({ Resolved: "ok", Missing: "danger", Invalid: "danger" }[s] || "");
const valTone = (v) => ({ ok: "ok", failed: "danger" }[v] || "");

export default {
  title: "Credentials",
  async render(outlet) {
    const body = el("div", {}, el("div", { class: "notice" }, "Loading…"));
    outlet.append(
      sectionHead(
        "Credentials — health",
        "Status of the credentials the platform can resolve. Secret values are never shown, copied or returned by the API — only health."
      ),
      body
    );

    async function draw(data) {
      const d = data || {};
      const accounts = d.accounts || [];
      const secrets = d.secrets || [];

      const accCard = card(
        `Claude accounts (${accounts.length})`,
        table(
          [
            { key: "id", label: "Account", mono: true },
            { key: "model", label: "Model", render: (r) => r.model || "—" },
            { key: "subsystem", label: "Subsystem" },
            { key: "health", label: "Health", render: (r) => badge(r.health, healthTone(r.health)) },
          ],
          accounts
        )
      );

      const validateBtn = button("Validate", {
        tone: "primary",
        ico: "refresh",
        onClick: async () => {
          validateBtn.textContent = "Validating…";
          try {
            const res = await api.command("credentials.validate");
            await draw(res && res.data);
          } catch (e) {
            body.prepend(el("div", { class: "notice danger" }, "Validation failed: " + (e && e.message ? e.message : e)));
          }
        },
      });

      const secCard = card(
        `Secrets (${secrets.length})`,
        el(
          "div",
          {},
          el(
            "div",
            { class: "row", style: "margin-bottom:10px; align-items:center; gap:10px" },
            validateBtn,
            el("span", { class: "sub" }, "Confirms each credential works — no value is ever revealed.")
          ),
          table(
            [
              { key: "name", label: "Name", mono: true },
              { key: "source", label: "Source" },
              { key: "subsystem", label: "Used by" },
              { key: "status", label: "Status", render: (r) => badge(r.status, statusTone(r.status)) },
              { key: "validation", label: "Validation", render: (r) => (r.validation ? badge(r.validation, valTone(r.validation)) : el("span", { class: "sub" }, "not run")) },
              { key: "last_validated", label: "Last validated", mono: true, render: (r) => (r.last_validated ? r.last_validated.replace("T", " ").replace("Z", "") : "—") },
            ],
            secrets
          )
        )
      );

      body.replaceChildren(accCard, secCard);
    }

    try {
      const res = await api.query("credentials.health");
      await draw(res && res.data);
    } catch (e) {
      body.replaceChildren(el("div", { class: "notice danger" }, "Failed to load credential health: " + (e && e.message ? e.message : e)));
    }
  },
};
