// aiw/knowledge.js — a searchable knowledge base (notes/markdown/docs) for AI Workspace, with tags and
// collections. Separate from the engine's task-knowledge. (PDF/Word import arrives with the companion.)
import { api } from "api";
import { el, sectionHead, badge, button, emptyState } from "ui";
import { openDrawer, field } from "./shared/drawer.js";

const KINDS = ["note", "markdown", "doc"];

async function editForm(item, onDone) {
  let content = "";
  if (item) {
    try {
      const d = await api.query("aiw.knowledge.get", { id: item.id });
      content = d.content || "";
    } catch {}
  }
  const title = el("input", { class: "login-input", value: (item && item.title) || "", placeholder: "Title" });
  const kind = el("select", { class: "login-input" }, ...KINDS.map((k) => el("option", { value: k }, k)));
  kind.value = (item && item.kind) || "note";
  const collection = el("input", { class: "login-input", value: (item && item.collection) || "", placeholder: "Collection (optional)" });
  const tags = el("input", { class: "login-input", value: ((item && item.tags) || []).join(", "), placeholder: "Comma-separated tags" });
  const body = el("textarea", { class: "login-input", rows: "12", placeholder: "Content…" });
  body.value = content;
  const err = el("div", { class: "login-err" });
  const saveBtn = button(item ? "Save" : "Add", { tone: "primary" });
  const overlay = openDrawer(
    item ? "Edit knowledge" : "Add knowledge",
    el("div", {}, field("Title", title), field("Kind", kind), field("Collection", collection), field("Tags", tags), field("Content", body), err),
    saveBtn,
    "760px"
  );
  saveBtn.onclick = async () => {
    err.textContent = "";
    saveBtn.disabled = true;
    saveBtn.textContent = "Saving…";
    const tagList = tags.value.split(",").map((s) => s.trim()).filter(Boolean);
    try {
      if (item) await api.command("aiw.knowledge.update", { id: item.id, title: title.value.trim(), kind: kind.value, collection: collection.value.trim(), tags: tagList, content: body.value, contentSet: true });
      else await api.command("aiw.knowledge.add", { title: title.value.trim(), kind: kind.value, collection: collection.value.trim(), tags: tagList, content: body.value });
      overlay.remove();
      onDone && onDone();
    } catch (e) {
      saveBtn.disabled = false;
      saveBtn.textContent = item ? "Save" : "Add";
      err.textContent = e && e.message ? e.message : String(e);
    }
  };
}

function itemCard(it, reload) {
  const edit = button("Edit", {});
  edit.onclick = () => editForm(it, reload);
  const del = button("Delete", { tone: "danger" });
  del.onclick = async () => { if (confirm(`Delete "${it.title}"?`)) { await api.command("aiw.knowledge.remove", { id: it.id }); reload(); } };
  return el(
    "div",
    { class: "aiw-prov-card" },
    el("div", { class: "aiw-prov-head" }, el("span", { class: "aiw-prov-name" }, it.title), badge(it.kind, ""), it.collection ? badge(it.collection, "") : null),
    (it.tags || []).length ? el("div", { class: "aiw-opt-desc" }, (it.tags || []).map((t) => "#" + t).join("  ")) : null,
    el("div", { class: "aiw-prov-actions" }, el("span", { class: "aiw-prov-url" }, (it.bytes || 0) + " bytes"), el("span", { style: { flex: 1 } }), edit, del)
  );
}

export default {
  title: "Knowledge",
  async render(outlet) {
    let q = "";
    const search = el("input", { class: "login-input", placeholder: "Search title, tags, content…", style: { maxWidth: "280px" } });
    const addBtn = button("＋ Add", { tone: "primary" });
    const body = el("div", {}, el("div", { class: "notice" }, "Loading…"));
    outlet.append(sectionHead("Knowledge", "Searchable notes, markdown and docs with tags and collections.", el("span", { style: { display: "flex", gap: "8px" } }, search, addBtn)), body);

    let t = null;
    search.oninput = () => { clearTimeout(t); t = setTimeout(() => { q = search.value.trim(); load(); }, 250); };

    async function load() {
      try {
        const d = await api.query("aiw.knowledge.list", q ? { q } : undefined);
        const items = (d && d.items) || [];
        body.replaceChildren(items.length ? el("div", { class: "aiw-prov-list" }, ...items.map((it) => itemCard(it, load))) : emptyState(q ? "No matches" : "No knowledge yet", q ? "Try another search." : "Add notes, docs or markdown to build a searchable base."));
      } catch (e) {
        body.replaceChildren(el("div", { class: "notice danger" }, "Failed: " + (e && e.message ? e.message : e)));
      }
    }
    addBtn.onclick = () => editForm(null, load);
    load();
  },
};
