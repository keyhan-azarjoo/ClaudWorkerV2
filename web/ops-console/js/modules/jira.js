// jira.js — the connected Jira board. Shows the READY queue (issues the engine will claim, labelled
// `ready`) and the full open BACKLOG (all To Do / in-progress issues) so the operator sees real work.
import { api } from "api";
import { el, card, sectionHead, badge, table, emptyState, button } from "ui";

const autoTone = (a) => ({ Enabled: "ok", Disabled: "danger", "Manual Only": "warn", "Needs Review": "info" }[a] || "");
const prioTone = (p) => ({ Highest: "danger", High: "warn", Medium: "info", Low: "", Lowest: "" }[p] || "");

export default {
  title: "Jira",
  async render(outlet) {
    const readyBody = el("div", {}, el("div", { class: "notice" }, "Loading…"));
    const backlogBody = el("div", {}, el("div", { class: "notice" }, "Loading…"));
    outlet.append(
      sectionHead("Jira", "Live from myotgo.atlassian.net. “Ready” is what the engine will process when you press Start Working; the backlog is the full open board."),
      card("Ready to work", readyBody, { sub: "labelled ready" }),
      card("Backlog (open issues)", backlogBody, { sub: "all To Do / in progress" })
    );

    // Ready queue
    try {
      const rows = (await api.query("jira.queue")) || [];
      readyBody.replaceChildren(
        rows.length
          ? table(
              [
                { key: "key", label: "Key", mono: true },
                { key: "summary", label: "Summary" },
                { key: "status", label: "Status", render: (r) => badge(r.status) },
                { key: "automation", label: "Automation", render: (r) => badge(r.automation || "—", autoTone(r.automation)) },
              ],
              rows
            )
          : emptyState("No ready issues", "Label a Jira issue `ready` to queue it for the engine.")
      );
    } catch (e) {
      readyBody.replaceChildren(el("div", { class: "notice danger" }, "Failed: " + (e && e.message ? e.message : e)));
    }

    // Backlog
    try {
      const rows = (await api.query("jira.backlog")) || [];
      backlogBody.replaceChildren(
        rows.length
          ? table(
              [
                { key: "key", label: "Key", mono: true },
                { key: "summary", label: "Summary" },
                { key: "status", label: "Status", render: (r) => badge(r.status) },
                { key: "priority", label: "Priority", render: (r) => badge(r.priority || "—", prioTone(r.priority)) },
                { key: "ready", label: "Ready", render: (r) => (r.ready ? badge("ready", "ok") : "—") },
                {
                  key: "run",
                  label: "",
                  render: (r) =>
                    button("Run", {
                      tone: "primary",
                      onClick: async (e) => {
                        const b = e.target;
                        if (!confirm(`Run ${r.key} for real?\n\nThe engine will edit the repo, verify the build, and merge to development if it passes.`)) return;
                        b.textContent = "Started";
                        b.disabled = true;
                        try {
                          await api.command("orchestrator.run", { issue: r.key });
                        } catch (err) {
                          b.textContent = "Run";
                          b.disabled = false;
                          backlogBody.prepend(el("div", { class: "notice danger" }, `Failed to start ${r.key}: ` + (err && err.message ? err.message : err)));
                        }
                      },
                    }),
                },
              ],
              rows
            )
          : emptyState("Backlog empty", "No open issues on the board.")
      );
    } catch (e) {
      backlogBody.replaceChildren(el("div", { class: "notice danger" }, "Failed to load backlog: " + (e && e.message ? e.message : e)));
    }
  },
};
