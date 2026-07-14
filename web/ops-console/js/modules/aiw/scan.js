// aiw/scan.js — scan REAL on-disk folders for optimizable files (.md/.json/.yaml/.xml/.log/.txt), see
// their token savings, and rewrite them IN PLACE so your coding tools (VS Code/Cursor/terminal AI) use
// the leaner versions immediately. Every in-place change is backed up and one-click restorable.
import { api } from "api";
import { el, card, sectionHead, badge, button, emptyState } from "ui";
import { fmtTokens } from "./shared/charts.js";
import { openDrawer } from "./shared/drawer.js";

const TYPES = [
  { key: "md", label: ".md / docs" },
  { key: "json", label: ".json" },
  { key: "yaml", label: ".yaml" },
  { key: "xml", label: ".xml / .html" },
  { key: "log", label: ".log" },
  { key: "txt", label: ".txt" },
];

export default {
  title: "Scan",
  async render(outlet) {
    const state = { roots: null, workspaces: false, result: null, selected: new Set() };

    const pathInput = el("input", { class: "login-input", placeholder: "/absolute/path/to/a/project/folder", style: { minWidth: "320px", flex: 1 } });
    const scanBtn = button("Scan folder", { tone: "primary" });
    const wsBtn = button("Scan all Workspaces", {});
    const typeBoxes = TYPES.map((t) => {
      const cb = el("input", { type: "checkbox" });
      cb.checked = true;
      return { key: t.key, cb, node: el("label", { class: "aiw-cfg-row" }, cb, el("span", {}, t.label)) };
    });
    const selectedTypes = () => typeBoxes.filter((b) => b.cb.checked).map((b) => b.key);

    const warn = el("div", { class: "notice warn" }, "Optimize rewrites the real files on disk (originals are backed up — use Restore to revert). Nothing is written until you click Optimize.");
    const controls = el(
      "div",
      { class: "aiw-scan-controls" },
      el("div", { class: "aiw-scan-row" }, pathInput, scanBtn, wsBtn),
      el("div", { class: "aiw-scan-types" }, el("span", { class: "pf-label" }, "Types:"), ...typeBoxes.map((b) => b.node))
    );
    const body = el("div", {});
    const backupsWrap = el("div", {});
    outlet.append(sectionHead("Scan", "Find and optimize the real .md/.json/.yaml/.xml/.log files your coding tools read.", null), warn, controls, body, backupsWrap);

    function totals(r) {
      const pct = r.totalBefore ? Math.round((r.totalSaved / r.totalBefore) * 100) : 0;
      return el(
        "div",
        { class: "aiw-usage-nums" },
        el("div", { class: "aiw-metric" }, el("span", { class: "n" }, String(r.count)), el("span", { class: "l" }, "files")),
        el("div", { class: "aiw-metric" }, el("span", { class: "n" }, fmtTokens(r.totalBefore) + " → " + fmtTokens(r.totalAfter)), el("span", { class: "l" }, "tokens")),
        el("div", { class: "aiw-metric accent" }, el("span", { class: "n" }, "−" + fmtTokens(r.totalSaved) + " (" + pct + "%)"), el("span", { class: "l" }, "savings (est)"))
      );
    }

    async function preview(path) {
      let d;
      try {
        d = await api.command("aiw.scan.preview", { path });
      } catch (e) {
        alert(e.message || e);
        return;
      }
      openDrawer(
        path.split("/").pop(),
        el(
          "div",
          {},
          el("div", { class: "aiw-try-stats" }, el("span", {}, "Before: " + fmtTokens(d.tokensBefore)), el("span", { class: "accent" }, "After: " + fmtTokens(d.tokensAfter)), el("span", { class: "muted" }, "via " + d.optimizer)),
          el("div", { class: "aiw-scan-diff" }, el("div", {}, el("div", { class: "pf-label" }, "Before"), el("pre", { class: "aiw-try-pre" }, d.before)), el("div", {}, el("div", { class: "pf-label" }, "After"), el("pre", { class: "aiw-try-pre" }, d.after)))
        ),
        el("span", { class: "pf-label" }, path),
        "920px"
      );
    }

    function fileRow(f) {
      const cb = el("input", { type: "checkbox" });
      cb.disabled = !f.optimizable;
      cb.checked = state.selected.has(f.path);
      cb.onchange = () => { cb.checked ? state.selected.add(f.path) : state.selected.delete(f.path); updateBar(); };
      const prev = button("Preview", {});
      prev.onclick = () => preview(f.path);
      const pct = f.tokensBefore ? Math.round((f.saved / f.tokensBefore) * 100) : 0;
      const restore = f.hasBackup ? (() => { const b = button("Restore", {}); b.onclick = async () => { await api.command("aiw.scan.restore", { path: f.path }); rescan(); }; return b; })() : null;
      return el(
        "div",
        { class: "aiw-scan-file" + (f.optimizable ? "" : " dim") },
        el("span", { class: "aiw-scan-cb" }, cb),
        el("span", { class: "aiw-scan-path mono", title: f.path }, f.rel),
        badge(f.type, ""),
        el("span", { class: "aiw-scan-tok" }, fmtTokens(f.tokensBefore) + " → " + fmtTokens(f.tokensAfter)),
        el("span", { class: "aiw-scan-saved" }, f.optimizable ? "−" + pct + "%" : "—"),
        f.hasBackup ? badge("backed up", "ok") : null,
        el("span", { class: "aiw-scan-actions" }, prev, restore)
      );
    }

    const bar = el("div", { class: "aiw-scan-bar" });
    function updateBar() {
      const n = state.selected.size;
      bar.replaceChildren();
      if (!state.result) return;
      const selectAll = button("Select all optimizable", {});
      selectAll.onclick = () => { state.result.files.forEach((f) => f.optimizable && state.selected.add(f.path)); renderResult(); };
      const clear = button("Clear", {});
      clear.onclick = () => { state.selected.clear(); renderResult(); };
      const opt = button(`Optimize ${n} selected (in place)`, { tone: n ? "danger" : "" });
      opt.disabled = !n;
      opt.onclick = optimizeSelected;
      bar.append(selectAll, clear, el("span", { style: { flex: 1 } }), opt);
    }

    async function optimizeSelected() {
      const paths = [...state.selected];
      if (!paths.length) return;
      if (!confirm(`Optimize ${paths.length} file(s) in place? Originals are backed up and restorable.`)) return;
      let res;
      try {
        res = await api.command("aiw.scan.optimize", { paths });
      } catch (e) {
        alert(e.message || e);
        return;
      }
      const ok = (res.results || []).filter((r) => r.ok && !r.skipped).length;
      alert(`Optimized ${ok} file(s) in place. Originals backed up — Restore any time.`);
      state.selected.clear();
      rescan();
    }

    function renderResult() {
      const r = state.result;
      if (!r) return;
      body.replaceChildren(
        totals(r),
        bar,
        (r.notes || []).length ? el("div", { class: "notice" }, r.notes.join(" · ")) : null,
        r.files.length
          ? el("div", { class: "aiw-scan-list" }, ...r.files.map(fileRow))
          : emptyState("No optimizable files found", "Try another folder or enable more types.")
      );
      updateBar();
    }

    async function runScan(opts) {
      body.replaceChildren(el("div", { class: "notice" }, "Scanning…"));
      try {
        const r = await api.command("aiw.scan.run", opts);
        state.result = r;
        // keep only still-present selections
        state.selected = new Set([...state.selected].filter((p) => r.files.some((f) => f.path === p)));
        renderResult();
        loadBackups();
      } catch (e) {
        body.replaceChildren(el("div", { class: "notice danger" }, "Failed: " + (e.message || e)));
      }
    }
    function rescan() {
      if (state.workspaces) runScan({ workspaces: true, types: selectedTypes() });
      else if (state.roots) runScan({ roots: state.roots, types: selectedTypes() });
    }

    scanBtn.onclick = () => {
      const p = pathInput.value.trim();
      if (!p) { alert("Enter a folder path."); return; }
      state.roots = [p]; state.workspaces = false;
      runScan({ roots: state.roots, types: selectedTypes() });
    };
    wsBtn.onclick = () => { state.roots = null; state.workspaces = true; runScan({ workspaces: true, types: selectedTypes() }); };

    async function loadBackups() {
      let list = [];
      try {
        list = (await api.query("aiw.scan.backups")) || [];
      } catch {}
      if (!list.length) { backupsWrap.replaceChildren(); return; }
      const restoreAll = button("Restore all", {});
      restoreAll.onclick = async () => { if (confirm(`Restore all ${list.length} backed-up files?`)) { await api.command("aiw.scan.restore", { all: true }); rescan(); loadBackups(); } };
      backupsWrap.replaceChildren(
        card(
          "Backups (" + list.length + ")",
          el(
            "div",
            { class: "aiw-scan-list" },
            ...list.map((e) => {
              const r = button("Restore", {});
              r.onclick = async () => { await api.command("aiw.scan.restore", { path: e.path }); rescan(); loadBackups(); };
              return el("div", { class: "aiw-scan-file" }, el("span", { class: "aiw-scan-path mono", title: e.path }, e.path), badge(e.optimizer, ""), el("span", { class: "aiw-scan-tok" }, fmtTokens(e.origBytes) + " → " + fmtTokens(e.optBytes) + " B"), el("span", { class: "aiw-scan-actions" }, r));
            })
          ),
          { action: restoreAll }
        )
      );
    }

    body.replaceChildren(emptyState("Pick a folder or scan your Workspaces", "It lists every .md/.json/.yaml/.xml/.log file with its token savings — then you choose what to optimize."));
    loadBackups();
  },
};
