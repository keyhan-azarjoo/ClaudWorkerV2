// aiw/optimizers.js — enable/disable, configure and try the token optimizers. Each optimizer is a
// backend plugin that self-describes (meta + config schema); this page auto-generates its config form
// and a "Try it" panel that shows real before/after token savings. Running one banks the saved tokens,
// so the Dashboard's compression + saved figures become real.
import { api } from "api";
import { el, card, sectionHead, badge, button, emptyState } from "ui";
import { fmtTokens } from "./shared/charts.js";

const CATS = [
  { key: "", label: "All" },
  { key: "content", label: "Content" },
  { key: "context", label: "Context" },
  { key: "repo", label: "Repo" },
  { key: "cache", label: "Cache" },
  { key: "filter", label: "Filter" },
];

function drawer(titleText, bodyNode, footNode, maxW = "620px") {
  const closeBtn = button("Close", {});
  const overlay = el(
    "div",
    { class: "drawer-overlay" },
    el(
      "div",
      { class: "drawer", style: { maxWidth: maxW } },
      el("div", { class: "drawer-head" }, el("span", { class: "drawer-title" }, titleText), closeBtn),
      el("div", { style: { padding: "16px", overflow: "auto" } }, bodyNode),
      el("div", { class: "drawer-foot" }, footNode)
    )
  );
  closeBtn.onclick = () => overlay.remove();
  overlay.onclick = (e) => e.target === overlay && overlay.remove();
  document.body.append(overlay);
  return overlay;
}

// fieldInput builds an input for one FieldSpec and returns {node, get()}.
function fieldInput(f, value) {
  if (f.type === "bool") {
    const cb = el("input", { type: "checkbox" });
    cb.checked = value === true;
    return { node: el("label", { class: "aiw-cfg-row" }, cb, el("span", {}, f.label)), get: () => cb.checked };
  }
  if (f.type === "select") {
    const sel = el("select", { class: "login-input" }, ...(f.options || []).map((o) => el("option", { value: o }, o)));
    sel.value = value != null ? String(value) : "";
    return { node: el("label", { class: "pf-field" }, el("span", { class: "pf-label" }, f.label), sel), get: () => sel.value };
  }
  const type = f.type === "int" ? "number" : "text";
  const inp = el("input", { class: "login-input", type });
  inp.value = value != null ? String(value) : "";
  return {
    node: el("label", { class: "pf-field" }, el("span", { class: "pf-label" }, f.label), inp, f.help ? el("span", { class: "pf-label" }, f.help) : null),
    get: () => (f.type === "int" ? Number(inp.value || 0) : inp.value),
  };
}

function configForm(o, reload) {
  const schema = (o.meta.configSchema || []);
  const fields = schema.map((f) => ({ f, ui: fieldInput(f, (o.config || {})[f.key]) }));
  const err = el("div", { class: "login-err" });
  const saveBtn = button("Save", { tone: "primary" });
  const overlay = drawer(
    "Configure · " + o.meta.name,
    el("div", {}, ...fields.map((x) => x.ui.node), fields.length ? null : el("div", { class: "pf-label" }, "This optimizer has no options."), err),
    saveBtn
  );
  saveBtn.onclick = async () => {
    const cfg = {};
    for (const x of fields) cfg[x.f.key] = x.ui.get();
    saveBtn.disabled = true;
    saveBtn.textContent = "Saving…";
    try {
      await api.command("aiw.optimizer.configure", { id: o.meta.id, config: cfg });
      overlay.remove();
      reload();
    } catch (e) {
      saveBtn.disabled = false;
      saveBtn.textContent = "Save";
      err.textContent = e && e.message ? e.message : String(e);
    }
  };
}

function tryPanel(o) {
  const input = el("textarea", { class: "login-input", rows: "10", placeholder: "Paste text/markdown/diff/tree here and run the optimizer…" });
  const runBtn = button("Run", { tone: "primary" });
  const out = el("div", { class: "aiw-try-out" });
  const overlay = drawer(
    "Try · " + o.meta.name,
    el("div", {}, el("div", { class: "pf-label" }, o.meta.description), input, out),
    runBtn,
    "760px"
  );
  runBtn.onclick = async () => {
    runBtn.disabled = true;
    runBtn.textContent = "Running…";
    out.replaceChildren();
    try {
      const r = await api.command("aiw.optimizer.run", { id: o.meta.id, kind: (o.meta.kinds || [])[0] || "text", content: input.value });
      const pct = r.tokensBefore ? Math.round(((r.tokensBefore - r.tokensAfter) / r.tokensBefore) * 100) : 0;
      out.replaceChildren(
        el(
          "div",
          { class: "aiw-try-stats" },
          el("span", {}, "Before: " + fmtTokens(r.tokensBefore) + " tok"),
          el("span", {}, "After: " + fmtTokens(r.tokensAfter) + " tok"),
          el("span", { class: "accent" }, "Saved: " + fmtTokens(r.saved) + " (" + pct + "%)"),
          el("span", { class: "muted" }, (r.latencyMs || 0).toFixed(1) + " ms")
        ),
        (r.notes && r.notes.length) ? el("div", { class: "aiw-try-notes" }, r.notes.join(" · ")) : null,
        el("pre", { class: "aiw-try-pre" }, r.output || "")
      );
    } catch (e) {
      out.replaceChildren(el("div", { class: "login-err" }, e && e.message ? e.message : String(e)));
    }
    runBtn.disabled = false;
    runBtn.textContent = "Run";
  };
}

function optCard(o, reload) {
  const st = o.stats || {};
  const enToggle = el("input", { type: "checkbox", class: "aiw-switch" });
  enToggle.checked = o.enabled !== false;
  enToggle.onchange = async () => {
    await api.command("aiw.optimizer.enable", { id: o.meta.id, enabled: enToggle.checked });
    reload();
  };
  const cfgBtn = button("Configure", {});
  cfgBtn.onclick = () => configForm(o, reload);
  const tryBtn = button("Try it", { tone: "primary" });
  tryBtn.onclick = () => tryPanel(o);
  const healthTone = st.health === "error" ? "danger" : st.health === "degraded" ? "warn" : "ok";
  return el(
    "div",
    { class: "aiw-opt-card" + (o.enabled === false ? " off" : "") },
    el(
      "div",
      { class: "aiw-opt-head" },
      el("label", { class: "aiw-switch-wrap", title: "Enable/disable" }, enToggle),
      el("span", { class: "aiw-opt-name" }, o.meta.name),
      badge(o.meta.category, ""),
      o.meta.requiresCompanion ? badge("needs companion", "warn") : null,
      st.runs ? badge(st.health || "ok", healthTone) : null
    ),
    el("div", { class: "aiw-opt-desc" }, o.meta.description),
    el(
      "div",
      { class: "aiw-opt-stats" },
      el("span", {}, "Saved: ", el("b", {}, fmtTokens(st.savedTokens || 0))),
      el("span", {}, "Runs: ", el("b", {}, String(st.runs || 0))),
      el("span", {}, "Avg: ", el("b", {}, (st.avgLatencyMs || 0).toFixed(1) + " ms"))
    ),
    el("div", { class: "aiw-opt-actions" }, cfgBtn, tryBtn)
  );
}

export default {
  title: "Optimizers",
  async render(outlet) {
    let filter = "";
    const grid = el("div", { class: "aiw-opt-grid" }, el("div", { class: "notice" }, "Loading…"));
    const chips = el(
      "div",
      { class: "aiw-chips" },
      ...CATS.map((c) =>
        el("button", { class: "aiw-chip" + (c.key === filter ? " on" : ""), onClick: () => { filter = c.key; renderChips(); load(); } }, c.label)
      )
    );
    function renderChips() {
      [...chips.children].forEach((ch, i) => ch.classList.toggle("on", CATS[i].key === filter));
    }
    outlet.append(
      sectionHead("Optimizers", "Pluggable token-savers. Enable, configure, and try each on real text — savings feed the Dashboard.", null),
      chips,
      grid
    );

    async function load() {
      try {
        const list = (await api.query("aiw.optimizers.list")) || [];
        const shown = list.filter((o) => !filter || o.meta.category === filter);
        grid.replaceChildren(
          shown.length ? el("div", { class: "aiw-opt-grid-inner" }, ...shown.map((o) => optCard(o, load))) : emptyState("No optimizers", "None in this category.")
        );
      } catch (e) {
        grid.replaceChildren(el("div", { class: "notice danger" }, "Failed: " + (e && e.message ? e.message : e)));
      }
    }
    load();
  },
};
