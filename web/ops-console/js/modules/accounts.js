// accounts.js — worker accounts (V1 parity): REAL login status + sign in (OAuth URL/paste-code) + sign
// out, plus pause/resume and real subscription usage. The login state is the actual CLI auth (not the
// resource health), so an account that can't run jobs shows "Not logged in" here instead of a false
// "healthy".
import { api } from "api";
import { el, card, sectionHead, badge, table, button } from "ui";

const availTone = (a) => ({ available: "ok", paused: "warn", cooldown: "info", offline: "danger", reserved: "info" }[a] || "");
const healthTone = (h) => ({ healthy: "ok", degraded: "warn", down: "danger" }[h] || "");
const usageTone = (p) => (p >= 90 ? "danger" : p >= 70 ? "warn" : "ok");

function usageBar(pct, reset, min) {
  if (pct == null) return el("span", { class: "sub" }, "—");
  const w = Math.min(100, Math.max(0, pct));
  const foot = reset ? `resets ${reset}` : min > 0 ? `${Math.floor(min / 60)}h ${min % 60}m` : "";
  return el(
    "div",
    { class: "usage" },
    el("div", { class: "usage-bar" }, el("div", { class: "usage-fill " + usageTone(pct), style: { width: w + "%" } })),
    el("div", { class: "usage-meta" }, el("span", { class: "usage-pct" }, `${pct}% used`), el("span", { class: "sub" }, `${100 - pct}% left${foot ? " · " + foot : ""}`))
  );
}

// loginFlow drives the OAuth sign-in: begin → show URL → user authorizes → paste code → submit.
async function loginFlow(name, onDone) {
  let r;
  try {
    r = await api.command("accounts.login.begin", { name });
  } catch (e) {
    alert("Login failed to start: " + (e.message || e));
    return;
  }
  if (r.alreadyLoggedIn) {
    alert(name + " is already logged in.");
    onDone && onDone();
    return;
  }
  if (!r.url) {
    alert(r.note || "Could not get a login URL. Try again, or run the login on the host.");
    onDone && onDone();
    return;
  }
  try { window.open(r.url, "_blank", "noopener"); } catch {}

  const link = el("a", { href: r.url, target: "_blank", rel: "noopener", style: { wordBreak: "break-all", fontSize: "12px", flex: "1", color: "var(--accent)" } }, r.url);
  const copyBtn = button("Copy URL", {});
  copyBtn.onclick = () => { navigator.clipboard && navigator.clipboard.writeText(r.url); copyBtn.textContent = "Copied"; setTimeout(() => (copyBtn.textContent = "Copy URL"), 1200); };
  const code = el("input", { class: "login-input", placeholder: "Paste the code from the login page", autocomplete: "off" });
  const err = el("div", { class: "login-err" });
  const submit = button("Sign in", { tone: "primary" });
  const cancel = button("Cancel", {});

  const overlay = el(
    "div",
    { class: "drawer-overlay" },
    el(
      "div",
      { class: "drawer", style: { maxWidth: "560px" } },
      el("div", { class: "drawer-head" }, el("span", { class: "drawer-title" }, "Sign in · " + name), cancel),
      el(
        "div",
        { style: { padding: "16px", overflow: "auto" } },
        el("ol", { class: "acct-login-steps" },
          el("li", {}, "Open this URL (a new tab may have opened already) and authorize:"),
          el("div", { style: { display: "flex", gap: "8px", alignItems: "center", margin: "8px 0", padding: "8px", background: "var(--bg)", borderRadius: "6px" } }, link, copyBtn),
          el("li", {}, "Copy the code the page gives you."),
          el("li", {}, "Paste it below and Sign in.")
        ),
        code,
        err
      ),
      el("div", { class: "drawer-foot" }, submit)
    )
  );
  function close() { overlay.remove(); }
  cancel.onclick = async () => { close(); try { await api.command("accounts.login.cancel", { name }); } catch {} onDone && onDone(); };
  overlay.onclick = (e) => { if (e.target === overlay) cancel.onclick(); };
  submit.onclick = async () => {
    err.textContent = "";
    submit.disabled = true;
    submit.textContent = "Signing in…";
    try {
      const res = await api.command("accounts.login.submit", { name, code: code.value });
      if (res.ok) { close(); onDone && onDone(); return; }
      err.textContent = res.message || "Sign in did not complete.";
    } catch (e) {
      err.textContent = e.message || String(e);
    }
    submit.disabled = false;
    submit.textContent = "Sign in";
  };
  document.body.append(overlay);
  setTimeout(() => code.focus(), 50);
}

export default {
  title: "Accounts",
  async render(outlet) {
    const body = el("div", {}, el("div", { class: "notice" }, "Loading…"));
    const refreshBtn = button("Refresh usage", { ico: "refresh", tone: "primary" });
    outlet.append(
      sectionHead("Accounts", "Worker accounts. Login status is the REAL CLI auth — sign in/out here. Pause keeps one out of rotation. Usage = real subscription %.", refreshBtn),
      card("Accounts", body)
    );

    async function load() {
      try {
        const [all, usage, status] = await Promise.all([
          api.query("resources.snapshot").catch(() => []),
          api.query("accounts.usage").catch(() => ({})),
          api.query("accounts.status").catch(() => ({})),
        ]);
        const rows = (all || [])
          .filter((r) => r.kind === "claude_account")
          .map((r) => {
            const st = (status || {})[r.name] || {};
            return { id: r.id, name: r.name, health: r.health, availability: r.availability, paused: r.availability === "paused", loggedIn: !!st.loggedIn, engine: st.engine || "claude", u: (usage || {})[r.name] || {} };
          });
        body.replaceChildren(
          table(
            [
              { key: "name", label: "Account", mono: true },
              { key: "login", label: "Login", render: (r) => badge(r.loggedIn ? "Logged in" : "Not logged in", r.loggedIn ? "ok" : "danger") },
              { key: "health", label: "Health", render: (r) => badge(r.health, healthTone(r.health)) },
              { key: "availability", label: "State", render: (r) => badge(r.availability, availTone(r.availability)) },
              { key: "session", label: "5-hour", render: (r) => (r.u.ok ? usageBar(r.u.session_pct, r.u.session_reset, r.u.session_min) : el("span", { class: "sub" }, "—")) },
              { key: "week", label: "7-day", render: (r) => (r.u.ok ? usageBar(r.u.week_pct, r.u.week_reset, r.u.week_min) : el("span", { class: "sub" }, "—")) },
              {
                key: "auth",
                label: "",
                render: (r) => {
                  const b = button(r.loggedIn ? "Logout" : "Login", { tone: r.loggedIn ? "" : "primary" });
                  b.onclick = async () => {
                    if (r.loggedIn) {
                      if (!confirm(`Sign out of ${r.name}? It won't run jobs until you sign back in.`)) return;
                      b.textContent = "…"; b.disabled = true;
                      try { await api.command("accounts.logout", { name: r.name }); } catch (e) { body.prepend(el("div", { class: "notice danger" }, "Logout failed: " + (e.message || e))); }
                      load();
                    } else {
                      loginFlow(r.name, load);
                    }
                  };
                  return b;
                },
              },
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
    const timer = setInterval(load, 15000);
    return () => clearInterval(timer);
  },
};
