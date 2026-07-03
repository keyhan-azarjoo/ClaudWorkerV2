// dashboard.js — the landing page of the Operations Console. One glance: current work, resources,
// verification, improvements, AI usage and live activity. Everything reacts to SSE events.

import { api, NotWired } from "api";
import { el, card, kpi, badge, statusDot, sectionHead, emptyState, notWired, fmtTime, ago } from "ui";

// State → badge tone (matches assignments.js so the whole console reads the same).
const stateTone = (s) => ({ done: "ok", failed: "danger", merging: "info", qa: "warn", verifying: "warn", developing: "warn", claimed: "" }[s] || "");
// Action status → glyph + tone for the per-task timeline.
const actionGlyph = { done: { text: "✓", tone: "ok" }, running: { text: "running", tone: "warn" }, failed: { text: "✗", tone: "danger" } };
const hhmmss = (iso) => String(iso || "").slice(11, 19) || "—";
const fmtTok = (n) => (n >= 1000 ? (n / 1000).toFixed(n >= 10000 ? 0 : 1) + "k" : String(n || 0));

// One task box: header (issue + state badge + account) and the ordered action timeline.
function taskBox(t, agentCount) {
  const rawActions = Array.isArray(t.actions) ? t.actions : [];
  // Collapse the timeline to ONE row per stage (the latest status), so a stage no longer shows both a
  // "running" row AND a "done" row. Order follows each stage's first appearance (the pipeline order).
  const byStage = new Map();
  for (const a of rawActions) if (a && a.stage) byStage.set(a.stage, a);
  const actions = [...byStage.values()];
  // Once the TASK is finished (done/failed), nothing is "running" any more — a stage left in "running"
  // just never got its terminal record (e.g. a timed-out develop). Resolve those to the task's outcome
  // so the box turns green/red and STOPS blinking instead of pulsing "running" forever.
  const terminal = t.state === "done" || t.state === "failed";
  const effStatus = (s) => (terminal && s === "running" ? (t.state === "done" ? "done" : "failed") : s);
  // Currently-running action = the LAST action whose (effective) status is "running".
  let running = null;
  if (!terminal) for (const a of actions) if (a && a.status === "running") running = a;
  // How many agents worked on this task — live lsof count while running, else the recorded historical count.
  const agents = Math.max(agentCount || 0, t.agents || 0);

  // A failed task gets a Continue button right on the box (retry on a fresh account after an error).
  let continueBtn = null;
  if (t.state === "failed") {
    continueBtn = el("button", { class: "btn primary task-continue" }, "▶ Continue");
    continueBtn.onclick = async (e) => {
      e.stopPropagation(); // don't open the drawer
      continueBtn.textContent = "…";
      continueBtn.disabled = true;
      try {
        await api.command("orchestrator.continue", { issue: t.issue });
      } catch (err) {
        /* shown in drawer if opened */
      }
    };
  }

  const head = el(
    "div",
    { class: "task-box-head" },
    el("span", { class: "task-issue mono" }, t.issue || "—"),
    badge(t.state || "—", stateTone(t.state)),
    agents > 0 ? el("span", { class: "task-agents", title: terminal ? "agents that worked on this task" : "agents working now" }, "⚡ " + agents + " agent" + (agents === 1 ? "" : "s")) : null,
    // Token chip: always visible for a live (non-terminal) task so the counter is present and ticks up;
    // for finished tasks show it only if there were tokens.
    t.tokens_in || t.tokens_out || (t.state && t.state !== "done" && t.state !== "failed")
      ? el("span", { class: "task-tok", title: "tokens sent / received (live)" }, "↑" + fmtTok(t.tokens_in) + " ↓" + fmtTok(t.tokens_out))
      : null,
    t.account ? el("span", { class: "task-acct" }, "on " + t.account) : null,
    continueBtn
  );

  const nowLine = running ? el("div", { class: "task-now running" }, "▶ now: " + (running.stage || "—") + (running.detail ? " " + running.detail : "")) : null;

  const rows = actions.map((a) => {
    const status = effStatus(a.status); // terminal task → no lingering "running" (green/red, no blink)
    const g = actionGlyph[status] || { text: status || "?", tone: "" };
    const isRunning = status === "running";
    return el(
      "div",
      { class: "task-action" + (isRunning ? " running" : "") },
      badge(g.text, g.tone),
      el("span", { class: "task-stage" }, a.stage || "—"),
      a.detail ? el("span", { class: "task-detail" }, a.detail) : null,
      el("span", { class: "task-time mono" }, hhmmss(a.at))
    );
  });

  const body = el("div", { class: "task-actions" }, ...(rows.length ? rows : [el("span", { class: "task-detail" }, "No actions yet")]));

  return el(
    "div",
    { class: "task-box clickable", title: "Click to see live agent output", onClick: () => openTaskDrawer(t.issue) },
    head,
    nowLine,
    body,
    el("div", { class: "task-expand-hint" }, "▸ click to see what the agents are doing")
  );
}

// openTaskDrawer expands a task into a full-screen panel streaming the agents' live output
// (thinking / doing / tool-use / responses). Polls task.stream and auto-scrolls; close to stop.
function openTaskDrawer(issue) {
  const logEl = el("div", { class: "drawer-log" }, el("div", { class: "sub" }, "Waiting for agent output…"));
  const tokEl = el("span", { class: "drawer-tok" });
  let lastLines = []; // most recent narrative lines (for the Markdown export)
  let lastTok = { in: 0, out: 0 };
  // Export the current report as a Markdown (.md) document.
  const mdBtn = el("button", { class: "btn", title: "Download this report as Markdown" }, "⬇ Markdown");
  mdBtn.onclick = () => {
    const md =
      `# ${issue} — Agent Report\n\n` +
      `> **Tokens:** ↑ ${fmtTok(lastTok.in)} sent · ↓ ${fmtTok(lastTok.out)} received\n\n---\n\n` +
      lastLines
        .map((l) => (/^(▶|🤖|🧑|✅|✔|🎯|🚀|⚠️|✨)/.test(l) || /\b(done|merged|completed|fixed|implemented)\b/i.test(l) ? `\n### ${l}\n` : `- ${l}`))
        .join("\n") +
      "\n";
    const a = el("a", { href: "data:text/markdown;charset=utf-8," + encodeURIComponent(md), download: issue + "-report.md" });
    document.body.append(a);
    a.click();
    a.remove();
  };
  // Copy the whole report to the clipboard (selection survives even though the log refreshes live).
  const copyBtn = el("button", { class: "btn", title: "Copy the whole report" }, "⧉ Copy");
  copyBtn.onclick = async () => {
    const text = lastLines.join("\n");
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      const ta = el("textarea", { style: "position:fixed;opacity:0" });
      ta.value = text;
      document.body.append(ta);
      ta.select();
      document.execCommand("copy");
      ta.remove();
    }
    copyBtn.textContent = "✓ Copied";
    setTimeout(() => (copyBtn.textContent = "⧉ Copy"), 1500);
  };
  const closeBtn = el("button", { class: "btn" }, "✕ Close");
  // Optional message to the agent, sent with Continue (e.g. "the merge conflicted, rebase onto origin
  // and retry" or extra guidance).
  const msgInput = el("input", { class: "drawer-msg", type: "text", placeholder: "Optional message to the agent (sent on Continue)…" });
  // Continue: retry/resume the task after a transient error (rate limit / API / merge error). Sends the
  // task — plus your message — to a fresh, non-cooled account.
  const continueBtn = el("button", { class: "btn primary" }, "▶ Continue");
  continueBtn.onclick = async () => {
    const message = msgInput.value.trim();
    continueBtn.textContent = "Continuing…";
    continueBtn.disabled = true;
    try {
      await api.command("orchestrator.continue", { issue, message });
      logEl.append(el("div", { class: "drawer-line", style: "color:var(--ok,#3fb950)" }, "▶ continue sent" + (message ? " with your message" : "") + " — the agent is resuming on an available account…"));
      msgInput.value = "";
    } catch (e) {
      logEl.append(el("div", { class: "drawer-line", style: "color:var(--danger,#f85149)" }, "Continue failed: " + (e && e.message ? e.message : e)));
    }
    setTimeout(() => {
      continueBtn.textContent = "▶ Continue";
      continueBtn.disabled = false;
    }, 2500);
  };
  msgInput.addEventListener("keydown", (e) => {
    if (e.key === "Enter") continueBtn.click();
  });
  const overlay = el(
    "div",
    { class: "drawer-overlay" },
    el(
      "div",
      { class: "drawer" },
      el("div", { class: "drawer-head" }, el("span", { class: "drawer-title mono" }, issue + " — live agent report"), tokEl, copyBtn, mdBtn, closeBtn),
      logEl,
      el("div", { class: "drawer-foot" }, msgInput, continueBtn)
    )
  );
  let stopped = false;
  let atBottom = true;
  let renderedSig = "";
  logEl.addEventListener("scroll", () => {
    atBottom = logEl.scrollHeight - logEl.scrollTop - logEl.clientHeight < 40;
  });
  async function poll() {
    if (stopped) return;
    try {
      const res = await api.query("task.stream", { issue });
      const raw = (res && res.lines) || [];
      // BRIEF narrative report: show the agent's own words + milestones, hide the noisy raw tool-command
      // dumps (e.g. "🔧 Bash — grep …") so it reads like a progress narrative, not a command log.
      const lines = raw.filter((l) => !/^🔧/.test(l));
      lastLines = lines;
      if (res) {
        lastTok = { in: res.tokens_in || 0, out: res.tokens_out || 0 };
        tokEl.textContent = "↑" + fmtTok(res.tokens_in || 0) + " ↓" + fmtTok(res.tokens_out || 0) + " tokens";
      }
      const isErr = (l) => /error|rate limit|limiting requests|overloaded|429|quota|usage limit|failed/i.test(l);
      // Milestones (agent start, sub-agent fan-out, operator note, completion cues) are IMPORTANT — show
      // them bold, wrapped in "===" separator rules so they stand out from the running narrative.
      const isImportant = (l) => /^(▶|🤖|🧑|✅|✔|🎯|🚀|⚠️|✨)/.test(l) || /\b(done|merged|completed|fixed|implemented)\b/i.test(l);
      const lineEl = (l) =>
        isImportant(l)
          ? el("div", { class: "drawer-important" + (isErr(l) ? " err" : "") }, l)
          : el("div", { class: "drawer-line" + (isErr(l) ? " err" : "") }, l);
      // Only re-render when the content actually changed — otherwise a live refresh every 1.5s would wipe
      // your text selection and fight your scrolling. Signature = line count + last line.
      const sig = lines.length + "|" + (lines[lines.length - 1] || "");
      if (sig !== renderedSig) {
        renderedSig = sig;
        logEl.replaceChildren(...(lines.length ? lines.map(lineEl) : [el("div", { class: "sub" }, "No agent output yet — the task may be starting.")]));
        if (atBottom) logEl.scrollTop = logEl.scrollHeight; // follow only if you're already at the bottom
      }
    } catch (e) {
      /* transient */
    }
    if (!stopped) setTimeout(poll, 1500);
  }
  const close = () => {
    stopped = true;
    overlay.remove();
  };
  closeBtn.onclick = close;
  overlay.onclick = (e) => {
    if (e.target === overlay) close();
  };
  document.body.append(overlay);
  poll();
}

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

    // Live summary derived from the task data (updated every poll) — meaningful numbers, not session
    // event counters.
    let sum = { active: 0, done: 0, failed: 0, agents: 0, tokIn: 0, tokOut: 0 };
    function renderKpis() {
      const s = sum;
      kAssign.replaceChildren(kpi({ label: "Active tasks", ico: "assignments", value: s.active, foot: `${s.agents} agent${s.agents === 1 ? "" : "s"} working`, tone: "accent" }));
      kVerify.replaceChildren(kpi({ label: "Completed", ico: "verification", value: s.done, foot: `${s.failed} failed` }));
      kLease.replaceChildren(kpi({ label: "Tokens sent", ico: "metrics", value: fmtTok(s.tokIn), foot: `↓ ${fmtTok(s.tokOut)} received` }));
      kEvents.replaceChildren(kpi({ label: "Agents working", ico: "leases", value: s.agents, foot: store.get().connected ? "live" : "offline" }));
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

    // Per-task activity (one box per task). Active/in-flight tasks and finished (Done/Failed) tasks are
    // shown in SEPARATE sections so completed tickets have their own place.
    const tasksBody = el("div");
    const doneBody = el("div");
    const isDone = (s) => s === "done" || s === "failed";
    async function loadTasks() {
      try {
        const [tasks, agents] = await Promise.all([
          api.query("tasks.activity").then((x) => x || []),
          api.query("tasks.agents").catch(() => ({})),
        ]);
        const active = tasks.filter((t) => !isDone(t.state));
        // Done list: latest-finished first (by the time of the last action), capped at the last 100.
        // Tasks with NO recorded actions (legacy boxes whose start time is reset each restart) have an
        // unknown finish time → sort them to the BOTTOM so a freshly-finished task is always on top.
        const finishedAt = (t) => {
          if (!t.actions || !t.actions.length) return 0;
          const ms = Date.parse(t.actions[t.actions.length - 1].at || "");
          return isNaN(ms) ? 0 : ms;
        };
        const done = tasks
          .filter((t) => isDone(t.state))
          .sort((a, b) => finishedAt(b) - finishedAt(a))
          .slice(0, 100);
        // Update the live KPI summary from the same data.
        sum = {
          active: active.length,
          done: tasks.filter((t) => t.state === "done").length,
          failed: tasks.filter((t) => t.state === "failed").length,
          agents: Object.values(agents || {}).reduce((a, b) => a + (b || 0), 0),
          tokIn: tasks.reduce((a, t) => a + (t.tokens_in || 0), 0),
          tokOut: tasks.reduce((a, t) => a + (t.tokens_out || 0), 0),
        };
        renderKpis();
        tasksBody.replaceChildren(
          active.length
            ? el("div", { class: "task-grid" }, ...active.map((t) => taskBox(t, (agents || {})[t.issue] || 0)))
            : emptyState("No active tasks", "Press Run on a Jira ticket or Start Working.")
        );
        doneBody.replaceChildren(
          done.length
            ? el("div", { class: "task-grid" }, ...done.map((t) => taskBox(t, 0)))
            : emptyState("No finished tasks yet", "Completed and failed tickets land here.")
        );
      } catch (e) {
        tasksBody.replaceChildren(e instanceof NotWired ? notWired("tasks.activity") : emptyState("Unavailable", e.message));
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
      card("Active tasks", tasksBody, { flush: true, sub: "in flight — live" }),
      card("Done", doneBody, { flush: true, sub: "completed & failed tickets" }),
      el("div", { class: "grid cols-2 mb" }, card("Live activity", feed, { flush: true, sub: "SSE", action: statusDot(store.get().connected ? "ok" : "danger", true) }), card("Current work", workBody, { sub: "assignments" })),
      card("System status", statusBody, { sub: "control plane" })
    );

    renderKpis();
    renderFeed();
    loadStatus();
    loadWork();
    loadTasks();

    // React to events (SSE) for instant updates.
    const off = stream.on((ev) => {
      renderKpis();
      renderFeed();
      if (ev.type.startsWith("Assignment")) loadWork();
      // Re-render the Tasks section on every event so a task's timeline updates live.
      try {
        loadTasks();
      } catch (e) {
        /* keep the dashboard alive even if a reload throws */
      }
    });
    const offStore = store.subscribe(() => {}); // keep store warm

    // AUTO-UPDATE while the page is open: token counters (SetTaskTokens/BankTaskTokens) and the agent
    // count do NOT emit SSE events, so poll the live Tasks + Work sections on a timer. Cheap in-memory
    // reads; stops when you leave the page. This is what makes the token counter tick live.
    const timer = setInterval(() => {
      try {
        loadTasks();
        loadWork();
      } catch (e) {
        /* keep the dashboard alive even if a refresh throws */
      }
    }, 2500);

    return () => {
      off();
      offStore();
      clearInterval(timer);
    };
  },
};
