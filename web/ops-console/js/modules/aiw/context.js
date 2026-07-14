// aiw/context.js — build reusable, pre-optimized context packs from files/folders or pasted text with a
// chain of optimizers applied. Pin, view, rebuild or delete. Shows real token savings.
import { api } from "api";
import { el, card, sectionHead, badge, button, emptyState } from "ui";
import { fmtTokens } from "./shared/charts.js";
import { openDrawer, field, toLines, optimizerChecks } from "./shared/drawer.js";

async function buildForm(pack, onDone) {
  let opts = [];
  try {
    opts = (await api.query("aiw.optimizers.list")) || [];
  } catch {}
  const name = el("input", { class: "login-input", value: (pack && pack.name) || "", placeholder: "Pack name" });
  const sources = el("textarea", { class: "login-input", rows: "3", placeholder: "One file or folder path per line (optional)" });
  sources.value = ((pack && pack.sources) || []).join("\n");
  const inline = el("textarea", { class: "login-input", rows: "6", placeholder: "…or paste text/markdown here" });
  const optChecks = optimizerChecks(opts, (pack && pack.optimizers) || ["compress", "dedup"]);
  const err = el("div", { class: "login-err" });
  const saveBtn = button(pack ? "Rebuild" : "Build", { tone: "primary" });
  const overlay = openDrawer(
    pack ? "Rebuild pack" : "Build context pack",
    el("div", {}, field("Name", name), field("Sources", sources, "Files or folders on this machine — large/ignored dirs are skipped."), field("Inline text", inline), el("div", { class: "pf-label" }, "Optimizers to apply (in order)"), optChecks.node, err),
    saveBtn,
    "720px"
  );
  saveBtn.onclick = async () => {
    err.textContent = "";
    saveBtn.disabled = true;
    saveBtn.textContent = "Building…";
    try {
      await api.command("aiw.context.build", { id: (pack && pack.id) || "", name: name.value.trim(), sources: toLines(sources.value), inline: inline.value, optimizers: optChecks.get() });
      overlay.remove();
      onDone && onDone();
    } catch (e) {
      saveBtn.disabled = false;
      saveBtn.textContent = pack ? "Rebuild" : "Build";
      err.textContent = e && e.message ? e.message : String(e);
    }
  };
}

async function viewPack(id) {
  let data;
  try {
    data = await api.query("aiw.context.get", { id });
  } catch (e) {
    return;
  }
  openDrawer(
    data.pack.name,
    el("div", {}, el("div", { class: "aiw-try-notes" }, (data.pack.notes || []).join(" · ")), el("pre", { class: "aiw-try-pre" }, data.content || "")),
    el("span", { class: "pf-label" }, fmtTokens(data.pack.tokensAfter) + " tokens"),
    "820px"
  );
}

function packCard(p, reload) {
  const pct = p.tokensBefore ? Math.round(((p.tokensBefore - p.tokensAfter) / p.tokensBefore) * 100) : 0;
  const view = button("View", {});
  view.onclick = () => viewPack(p.id);
  const rebuild = button("Rebuild", {});
  rebuild.onclick = () => buildForm(p, reload);
  const pin = button(p.pinned ? "Unpin" : "Pin", {});
  pin.onclick = async () => { await api.command("aiw.context.pin", { id: p.id, pinned: !p.pinned }); reload(); };
  const del = button("Delete", { tone: "danger" });
  del.onclick = async () => { if (confirm(`Delete pack "${p.name}"?`)) { await api.command("aiw.context.remove", { id: p.id }); reload(); } };
  return el(
    "div",
    { class: "aiw-prov-card" },
    el("div", { class: "aiw-prov-head" }, el("span", { class: "aiw-prov-name" }, p.name), p.pinned ? badge("pinned", "ok") : null, el("span", { class: "aiw-prov-url", style: { marginLeft: "auto" } }, p.files + " files")),
    el("div", { class: "aiw-try-stats" }, el("span", {}, fmtTokens(p.tokensBefore) + " → " + fmtTokens(p.tokensAfter) + " tok"), el("span", { class: "accent" }, "−" + pct + "%"), el("span", { class: "muted" }, (p.optimizers || []).join(", ") || "no optimizers")),
    el("div", { class: "aiw-prov-actions" }, view, rebuild, el("span", { style: { flex: 1 } }), pin, del)
  );
}

export default {
  title: "Context",
  async render(outlet) {
    const body = el("div", {}, el("div", { class: "notice" }, "Loading…"));
    const addBtn = button("＋ Build pack", { tone: "primary" });
    outlet.append(sectionHead("Context Packs", "Assemble sources into a compact, optimizer-processed context you can reuse.", addBtn), body);
    async function load() {
      try {
        const packs = (await api.query("aiw.context.list")) || [];
        body.replaceChildren(packs.length ? el("div", { class: "aiw-prov-list" }, ...packs.map((p) => packCard(p, load))) : emptyState("No context packs", "Build one from files/folders or pasted text."));
      } catch (e) {
        body.replaceChildren(el("div", { class: "notice danger" }, "Failed: " + (e && e.message ? e.message : e)));
      }
    }
    addBtn.onclick = () => buildForm(null, load);
    load();
  },
};
