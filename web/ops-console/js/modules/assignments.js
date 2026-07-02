// assignments.js — task activity. For every issue in flight it shows the STATE, which ACCOUNT ran it,
// the OUTCOME of the last execution (success / runtime_failure / semantic / rate_limit / auth), how
// long it took and the token estimate — plus the active WORKTREES (where the work is happening). This
// is "what the agent is doing / what was done on the task".
import { api } from "api";
import { el, card, sectionHead, badge, table, emptyState } from "ui";

const stateTone = (s) => ({ done: "ok", failed: "danger", merging: "info", qa: "purple", verifying: "info", developing: "warn", claimed: "" }[s] || "");
const classTone = (c) => ({ success: "ok", runtime_failure: "danger", authentication: "danger", semantic: "warn", rate_limit: "warn", cancelled: "" }[c] || "");

export default {
  title: "Assignments",
  async render(outlet, ctx) {
    const tasksBody = el("div", {}, el("div", { class: "notice" }, "Loading…"));
    const wtBody = el("div", {}, el("div", { class: "notice" }, "Loading…"));
    outlet.append(
      sectionHead("Assignments", "What each task is doing and what has been done — state, account, outcome, timing. Updates live."),
      card("Tasks", tasksBody),
      card("Active worktrees", wtBody, { sub: "where the work is" })
    );

    async function load() {
      const [assigns, rt, wts] = await Promise.all([
        api.query("assignments.list").catch(() => []),
        api.query("runtime.state").catch(() => ({})),
        api.query("git.worktrees").catch(() => []),
      ]);

      // Latest execution per issue (recent is oldest→newest; last wins).
      const last = {};
      ((rt && rt.recent) || []).forEach((x) => (last[x.issue] = x));

      const rows = (assigns || []).map((a) => {
        const x = last[a.issue_key] || {};
        return {
          issue: a.issue_key,
          state: a.state,
          attempt: a.attempt,
          account: x.account || "—",
          outcome: x.class || "—",
          duration: x.duration || "—",
          tokens: x.token_estimate != null ? x.token_estimate : "—",
        };
      });

      tasksBody.replaceChildren(
        rows.length
          ? table(
              [
                { key: "issue", label: "Issue", mono: true },
                { key: "state", label: "State", render: (r) => badge(r.state, stateTone(r.state)) },
                { key: "account", label: "Ran on", mono: true },
                { key: "outcome", label: "Last outcome", render: (r) => (r.outcome === "—" ? "—" : badge(r.outcome, classTone(r.outcome))) },
                { key: "duration", label: "Duration", mono: true },
                { key: "tokens", label: "~Tokens", mono: true },
                { key: "attempt", label: "Attempt", mono: true },
              ],
              rows
            )
          : emptyState("No tasks yet", "Press Run on a Jira ticket (or Start Working) to begin.")
      );

      wtBody.replaceChildren(
        (wts || []).length
          ? table(
              [
                { key: "branch", label: "Branch", mono: true },
                { key: "head", label: "HEAD", mono: true, render: (r) => (r.head ? String(r.head).slice(0, 10) : "—") },
                { key: "path", label: "Path", mono: true },
              ],
              wts
            )
          : emptyState("No active worktrees", "A worktree appears here while a task is being developed.")
      );
    }

    // React to live events so state/outcome/worktrees update as work progresses.
    if (ctx && ctx.stream) ctx.stream.on(() => load());
    load();
  },
};
