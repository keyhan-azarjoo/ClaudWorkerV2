// aiw/workspaces.js — group repos/folders and per-workspace optimizer choices. The "current" workspace
// shows on the Dashboard. (Named "Workspaces" to avoid colliding with cwv2's top-level Projects.)
import { api } from "api";
import { el, sectionHead, badge, button, emptyState } from "ui";
import { openDrawer, field, toLines, optimizerChecks } from "./shared/drawer.js";

async function editForm(ws, onDone) {
  let opts = [];
  try {
    opts = (await api.query("aiw.optimizers.list")) || [];
  } catch {}
  const name = el("input", { class: "login-input", value: (ws && ws.name) || "", placeholder: "Workspace name" });
  const repos = el("textarea", { class: "login-input", rows: "3", placeholder: "One repo per line" });
  repos.value = ((ws && ws.repos) || []).join("\n");
  const folders = el("textarea", { class: "login-input", rows: "3", placeholder: "One folder path per line" });
  folders.value = ((ws && ws.folders) || []).join("\n");
  const notes = el("textarea", { class: "login-input", rows: "2", placeholder: "Notes (optional)" });
  notes.value = (ws && ws.notes) || "";
  const optChecks = optimizerChecks(opts, (ws && ws.optimizers) || []);
  const err = el("div", { class: "login-err" });
  const saveBtn = button(ws ? "Save" : "Create", { tone: "primary" });
  const overlay = openDrawer(
    ws ? "Edit workspace" : "New workspace",
    el("div", {}, field("Name", name), field("Repos", repos), field("Folders", folders), field("Notes", notes), el("div", { class: "pf-label" }, "Optimizers for this workspace"), optChecks.node, err),
    saveBtn
  );
  saveBtn.onclick = async () => {
    err.textContent = "";
    saveBtn.disabled = true;
    saveBtn.textContent = "Saving…";
    try {
      if (ws) {
        await api.command("aiw.workspace.update", { id: ws.id, name: name.value.trim(), repos: toLines(repos.value), folders: toLines(folders.value), notes: notes.value.trim(), optimizers: optChecks.get() });
      } else {
        const r = await api.command("aiw.workspace.add", { name: name.value.trim() });
        // Immediately apply the rest of the form to the freshly created workspace.
        await api.command("aiw.workspace.update", { id: r.id, name: name.value.trim(), repos: toLines(repos.value), folders: toLines(folders.value), notes: notes.value.trim(), optimizers: optChecks.get() });
      }
      overlay.remove();
      onDone && onDone();
    } catch (e) {
      saveBtn.disabled = false;
      saveBtn.textContent = ws ? "Save" : "Create";
      err.textContent = e && e.message ? e.message : String(e);
    }
  };
}

function wsCard(w, reload) {
  const setCur = button("Set current", {});
  setCur.disabled = !!w.current;
  setCur.onclick = async () => { await api.command("aiw.workspace.setCurrent", { id: w.id }); reload(); };
  const edit = button("Edit", {});
  edit.onclick = () => editForm(w, reload);
  const del = button("Delete", { tone: "danger" });
  del.onclick = async () => { if (confirm(`Delete workspace "${w.name}"?`)) { await api.command("aiw.workspace.remove", { id: w.id }); reload(); } };
  return el(
    "div",
    { class: "aiw-prov-card" },
    el("div", { class: "aiw-prov-head" }, el("span", { class: "aiw-prov-name" }, w.name), w.current ? badge("current", "ok") : null, el("span", { class: "aiw-prov-url", style: { marginLeft: "auto" } }, (w.repos || []).length + " repos · " + (w.folders || []).length + " folders")),
    (w.optimizers || []).length ? el("div", { class: "aiw-opt-stats" }, el("span", {}, "Optimizers: ", el("b", {}, w.optimizers.join(", ")))) : el("div", { class: "aiw-accts-empty" }, "No workspace optimizers set."),
    w.notes ? el("div", { class: "aiw-opt-desc" }, w.notes) : null,
    el("div", { class: "aiw-prov-actions" }, setCur, el("span", { style: { flex: 1 } }), edit, del)
  );
}

export default {
  title: "Workspaces",
  async render(outlet) {
    const body = el("div", {}, el("div", { class: "notice" }, "Loading…"));
    const addBtn = button("＋ New workspace", { tone: "primary" });
    outlet.append(sectionHead("Workspaces", "Group repos, folders and optimizer choices. The current workspace shows on the Dashboard.", addBtn), body);
    async function load() {
      try {
        const ws = (await api.query("aiw.workspaces.list")) || [];
        body.replaceChildren(ws.length ? el("div", { class: "aiw-prov-list" }, ...ws.map((w) => wsCard(w, load))) : emptyState("No workspaces yet", "Create one to group repos and pick its optimizers."));
      } catch (e) {
        body.replaceChildren(el("div", { class: "notice danger" }, "Failed: " + (e && e.message ? e.message : e)));
      }
    }
    addBtn.onclick = () => editForm(null, load);
    load();
  },
};
