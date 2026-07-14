// drawer.js — a shared slide-over drawer for AI Workspace forms (matches the console's drawer styling).
import { el, button } from "ui";

export function openDrawer(titleText, bodyNode, footNode, maxW = "620px") {
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

// field(label, inputNode, help?) — a labelled form field.
export function field(label, input, help) {
  return el("label", { class: "pf-field" }, el("span", { class: "pf-label" }, label), input, help ? el("span", { class: "pf-label" }, help) : null);
}

// linesInput/toLines — textareas that edit a string[] one-per-line.
export function toLines(text) {
  return String(text || "")
    .split("\n")
    .map((s) => s.trim())
    .filter(Boolean);
}

// optimizerChecks(list, selectedIds) — checkbox group for choosing optimizer ids; returns {node, get()}.
export function optimizerChecks(list, selected) {
  const sel = new Set(selected || []);
  const boxes = (list || []).map((o) => {
    const cb = el("input", { type: "checkbox" });
    cb.checked = sel.has(o.meta.id);
    return { id: o.meta.id, cb, node: el("label", { class: "aiw-cfg-row" }, cb, el("span", {}, o.meta.name)) };
  });
  return {
    node: el("div", { class: "aiw-opt-checks" }, ...boxes.map((b) => b.node)),
    get: () => boxes.filter((b) => b.cb.checked).map((b) => b.id),
  };
}
