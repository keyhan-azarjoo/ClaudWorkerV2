// rules.js — the Rules page. Standing rules every agent MUST read and obey before making any change
// (they are injected into the main agent's prompt). View, add, edit, activate/deactivate and delete.
import { api } from "api";
import { el, card, sectionHead, badge, button, emptyState } from "ui";

function editForm(rule, onDone) {
  const title = el("input", { class: "login-input", value: (rule && rule.title) || "", placeholder: "Short title" });
  const text = el("textarea", { class: "login-input", rows: "6", placeholder: "The rule the agents must follow…" });
  text.value = (rule && rule.text) || "";
  const err = el("div", { class: "login-err" });
  const saveBtn = button(rule ? "Save" : "Add rule", { tone: "primary" });
  const closeBtn = button("Cancel", {});
  saveBtn.onclick = async () => {
    err.textContent = "";
    saveBtn.textContent = "Saving…";
    saveBtn.disabled = true;
    try {
      if (rule) await api.command("rules.update", { id: rule.id, title: title.value.trim(), text: text.value.trim(), active: rule.active !== false });
      else await api.command("rules.add", { title: title.value.trim(), text: text.value.trim() });
      overlay.remove();
      onDone && onDone();
    } catch (e) {
      saveBtn.textContent = rule ? "Save" : "Add rule";
      saveBtn.disabled = false;
      err.textContent = e && e.message ? e.message : String(e);
    }
  };
  const overlay = el(
    "div",
    { class: "drawer-overlay" },
    el(
      "div",
      { class: "drawer", style: { maxWidth: "560px" } },
      el("div", { class: "drawer-head" }, el("span", { class: "drawer-title" }, rule ? "Edit rule" : "Add a rule"), closeBtn),
      el("div", { style: { padding: "16px", overflow: "auto" } }, el("label", { class: "pf-field" }, el("span", { class: "pf-label" }, "Title"), title), el("label", { class: "pf-field" }, el("span", { class: "pf-label" }, "Rule"), text), err),
      el("div", { class: "drawer-foot" }, saveBtn)
    )
  );
  closeBtn.onclick = () => overlay.remove();
  overlay.onclick = (e) => e.target === overlay && overlay.remove();
  document.body.append(overlay);
  setTimeout(() => title.focus(), 50);
}

export default {
  title: "Rules",
  async render(outlet) {
    const body = el("div", {}, el("div", { class: "notice" }, "Loading…"));
    const addBtn = button("＋ Add rule", { tone: "primary" });
    outlet.append(
      sectionHead("Rules", "Standing rules injected into EVERY agent's prompt — the main agent reads them before any change. Add, edit, activate or delete.", addBtn),
      card("Rules", body)
    );

    async function load() {
      try {
        const rules = (await api.query("rules.list")) || [];
        body.replaceChildren(
          rules.length
            ? el(
                "div",
                { class: "rules-list" },
                ...rules.map((r) => {
                  const toggle = button(r.active ? "Deactivate" : "Activate", { tone: r.active ? "" : "primary" });
                  toggle.onclick = async () => {
                    toggle.disabled = true;
                    try {
                      await api.command("rules.setActive", { id: r.id, active: !r.active });
                    } catch (e) {}
                    load();
                  };
                  const editB = button("Edit", {});
                  editB.onclick = () => editForm(r, load);
                  const delB = button("Delete", { tone: "danger" });
                  delB.onclick = async () => {
                    if (!confirm("Delete this rule?")) return;
                    await api.command("rules.remove", { id: r.id });
                    load();
                  };
                  return el(
                    "div",
                    { class: "rule-card" + (r.active ? "" : " inactive") },
                    el(
                      "div",
                      { class: "rule-head" },
                      el("span", { class: "rule-title" }, r.title || "(untitled)"),
                      badge(r.active ? "active" : "inactive", r.active ? "ok" : "danger"),
                      el("span", { style: { marginLeft: "auto" } }, el("span", { class: "run-control" }, toggle, editB, delB))
                    ),
                    el("div", { class: "rule-text" }, r.text || "")
                  );
                })
              )
            : emptyState("No rules yet", "Add a rule that every agent must follow.")
        );
      } catch (e) {
        body.replaceChildren(el("div", { class: "notice danger" }, "Failed: " + (e && e.message ? e.message : e)));
      }
    }

    addBtn.onclick = () => editForm(null, load);
    load();
  },
};
