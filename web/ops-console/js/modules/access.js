// access.js — Access Grants. Folders the worker agents may read/use beyond their single-repo worktree,
// so cross-repo tasks (whose plan or sibling repos live elsewhere) don't fail as "outside the sandbox".
// Grant "Always" (kept, applied to every future task — never asked again) or "Just this task"
// (temporary). Granted paths are injected into every worker's prompt.
import { api } from "api";
import { el, card, sectionHead, badge, button, emptyState } from "ui";

// grantDialog is the Always / Just this task / Cancel popup.
function grantDialog(path, onDone) {
  const err = el("div", { class: "login-err" });
  const always = button("Always", { tone: "primary" });
  const once = button("Just this task", {});
  const cancel = button("Cancel", {});
  const overlay = el(
    "div",
    { class: "drawer-overlay" },
    el(
      "div",
      { class: "drawer", style: { maxWidth: "520px" } },
      el("div", { class: "drawer-head" }, el("span", { class: "drawer-title" }, "Grant access"), cancel),
      el(
        "div",
        { style: { padding: "16px" } },
        el("div", { class: "pf-label" }, "Let worker agents read and use this folder in future tasks:"),
        el("div", { class: "mono", style: { margin: "8px 0", wordBreak: "break-all", color: "var(--accent)" } }, path),
        el("div", { class: "pf-label" }, "“Always” is kept and applied to every future task (you won’t be asked again). “Just this task” is temporary."),
        err
      ),
      el("div", { class: "drawer-foot" }, once, always)
    )
  );
  async function grant(scope) {
    err.textContent = "";
    always.disabled = once.disabled = true;
    try {
      await api.command("access.add", { path, scope });
      overlay.remove();
      onDone && onDone();
    } catch (e) {
      always.disabled = once.disabled = false;
      err.textContent = e && e.message ? e.message : String(e);
    }
  }
  always.onclick = () => grant("always");
  once.onclick = () => grant("once");
  cancel.onclick = () => overlay.remove();
  overlay.onclick = (e) => e.target === overlay && overlay.remove();
  document.body.append(overlay);
}

export default {
  title: "Access",
  async render(outlet) {
    const input = el("input", { class: "login-input", placeholder: "/absolute/path/to/a/folder (e.g. your whole project folder)", style: { minWidth: "340px", flex: "1" } });
    const grantBtn = button("Grant access", { tone: "primary" });
    const body = el("div", {}, el("div", { class: "notice" }, "Loading…"));
    outlet.append(
      sectionHead("Access", "Folders worker agents may read/use beyond their worktree — so cross-repo tasks don’t fail as “outside the sandbox”.", null),
      el("div", { class: "notice" }, "Grant a folder (e.g. your whole project folder) so agents can read its files next time. “Always” grants are injected into every future task’s prompt; “Just this task” grants expire automatically."),
      el("div", { style: { display: "flex", gap: "8px", margin: "12px 0", flexWrap: "wrap" } }, input, grantBtn),
      card("Granted folders", body)
    );

    grantBtn.onclick = () => {
      const p = input.value.trim();
      if (!p) { input.focus(); return; }
      grantDialog(p, () => { input.value = ""; load(); });
    };
    input.addEventListener("keydown", (e) => { if (e.key === "Enter") grantBtn.onclick(); });

    async function load() {
      try {
        const grants = (await api.query("access.list")) || [];
        body.replaceChildren(
          grants.length
            ? el(
                "div",
                { class: "aiw-scan-list" },
                ...grants.map((g) => {
                  const del = button("Remove", { tone: "danger" });
                  del.onclick = async () => { await api.command("access.remove", { path: g.path }); load(); };
                  return el(
                    "div",
                    { class: "aiw-scan-file" },
                    el("span", { class: "aiw-scan-path mono", title: g.path }, g.path),
                    badge(g.scope === "once" ? "just this task" : "always", g.scope === "once" ? "warn" : "ok"),
                    el("span", { class: "aiw-scan-actions" }, del)
                  );
                })
              )
            : emptyState("No folders granted", "Agents only see their own worktree. Grant a folder above to widen access.")
        );
      } catch (e) {
        body.replaceChildren(el("div", { class: "notice danger" }, "Failed: " + (e && e.message ? e.message : e)));
      }
    }
    load();
  },
};
