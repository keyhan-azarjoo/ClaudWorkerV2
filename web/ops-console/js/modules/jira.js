// jira.js — the connected Jira board. Shows the READY queue (labelled `ready`) and the full open
// BACKLOG. Each backlog row reflects the task's live status (Running/Done/Failed) instead of always
// offering Run, and a Run control lets you pick WHICH account (Claude or Codex) runs it.
import { api } from "api";
import { el, card, sectionHead, badge, table, emptyState, button } from "ui";

const autoTone = (a) => ({ Enabled: "ok", Disabled: "danger", "Manual Only": "warn", "Needs Review": "info" }[a] || "");
const prioTone = (p) => ({ Highest: "danger", High: "warn", Medium: "info", Low: "", Lowest: "" }[p] || "");
// Rank for sorting highest-priority-first (unknown priority sinks to the bottom).
const prioRank = (p) => ({ Highest: 5, High: 4, Medium: 3, Low: 2, Lowest: 1 }[p] || 0);
const stateTone = (s) => ({ done: "ok", failed: "danger", merging: "info", qa: "warn", verifying: "warn", developing: "warn", claimed: "" }[s] || "");
const ACTIVE = ["claimed", "developing", "qa", "verifying", "merging"];

export default {
  title: "Jira",
  async render(outlet) {
    const readyBody = el("div", {}, el("div", { class: "notice" }, "Loading…"));
    const backlogBody = el("div", {}, el("div", { class: "notice" }, "Loading…"));

    // Sentry → Jira: create HIGH-priority Bug tickets for recent Sentry errors (deduped; no agent runs
    // automatically — the tickets just land in the backlog for you to Run).
    const syncBtn = button("🔄 Sync Sentry → Jira", {});
    syncBtn.onClick = null;
    syncBtn.onclick = async () => {
      syncBtn.textContent = "Syncing…";
      syncBtn.disabled = true;
      try {
        const r = await api.command("sentry.sync", {});
        const n = (r && r.created_count) || 0;
        syncBtn.textContent = n > 0 ? `✓ Created ${n} bug${n === 1 ? "" : "s"}` : "✓ No new errors";
        loadBacklog();
      } catch (e) {
        syncBtn.textContent = "⚠ " + (e && e.message ? e.message : "failed");
      }
      setTimeout(() => {
        syncBtn.textContent = "🔄 Sync Sentry → Jira";
        syncBtn.disabled = false;
      }, 3500);
    };

    outlet.append(
      sectionHead("Jira", "Live board. Backlog rows show each task's status; use Run (with an account picker) to work one now."),
      card("Ready to work", readyBody, { sub: "labelled ready" }),
      card("Backlog — all tasks (highest priority first)", backlogBody, { sub: "whole board, by priority", action: syncBtn })
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

    // Backlog + task status + accounts
    async function loadBacklog() {
      try {
        const [rows, tasks, resources] = await Promise.all([
          api.query("jira.backlog").catch(() => []),
          api.query("tasks.activity").catch(() => []),
          api.query("resources.snapshot").catch(() => []),
        ]);
        // Always highest-priority first (belt-and-suspenders on top of the backend ORDER BY priority).
        (rows || []).sort((a, b) => prioRank(b.priority) - prioRank(a.priority));
        const stateByIssue = {};
        (tasks || []).forEach((t) => (stateByIssue[t.issue] = t.state));
        // Only accounts that can actually run are selectable — a PAUSED (or offline) account must not
        // be pickable for a Run.
        const accounts = (resources || []).filter(
          (r) => r.kind === "claude_account" && r.availability !== "paused" && r.availability !== "offline"
        );

        // A Run control: account picker (Auto + each selectable account) + Run button.
        const runControl = (issueKey) => {
          const sel = el("select", { class: "acct-select" }, el("option", { value: "" }, "Auto"));
          accounts.forEach((a) => {
            const eng = (a.labels && a.labels.engine) || "claude";
            const opt = el("option", { value: a.id }, `${a.name} (${eng})`);
            if (a.availability !== "available") opt.textContent += " — " + a.availability;
            sel.append(opt);
          });
          const b = button("Run", { tone: "primary" });
          b.onClick = null;
          b.onclick = async () => {
            const acct = sel.value;
            const who = acct ? accounts.find((a) => a.id === acct)?.name : "an auto-selected account";
            if (!confirm(`Run ${issueKey} on ${who}?\n\nThe engine will edit the repo, verify the build, and merge to development if it passes.`)) return;
            b.textContent = "Started";
            b.disabled = true;
            sel.disabled = true;
            try {
              await api.command("orchestrator.run", { issue: issueKey, account: acct });
              setTimeout(loadBacklog, 800);
            } catch (err) {
              b.textContent = "Run";
              b.disabled = false;
              sel.disabled = false;
              backlogBody.prepend(el("div", { class: "notice danger" }, `Failed to start ${issueKey}: ` + (err && err.message ? err.message : err)));
            }
          };
          return el("span", { class: "run-control" }, sel, b);
        };

        // A Jira status that is itself terminal (Done/Closed/Resolved/Cancelled) is never runnable.
        const jiraDone = (s) => /^(done|closed|resolved|cancel)/i.test(s || "");
        const actionCell = (r) => {
          const st = stateByIssue[r.key];
          if (st && ACTIVE.includes(st)) return badge("● " + st, stateTone(st)); // working now
          if (st === "done" || jiraDone(r.status)) return badge("✓ done", "ok");
          if (st === "failed") return el("span", { class: "run-control" }, badge("failed", "danger"), runControl(r.key));
          return runControl(r.key);
        };

        backlogBody.replaceChildren(
          rows.length
            ? table(
                [
                  { key: "key", label: "Key", mono: true },
                  { key: "summary", label: "Summary" },
                  { key: "status", label: "Status", render: (r) => badge(r.status) },
                  { key: "priority", label: "Priority", render: (r) => badge(r.priority || "—", prioTone(r.priority)) },
                  { key: "action", label: "", render: actionCell },
                ],
                rows
              )
            : emptyState("Backlog empty", "No open issues on the board.")
        );
      } catch (e) {
        backlogBody.replaceChildren(el("div", { class: "notice danger" }, "Failed to load backlog: " + (e && e.message ? e.message : e)));
      }
    }
    loadBacklog();
  },
};
