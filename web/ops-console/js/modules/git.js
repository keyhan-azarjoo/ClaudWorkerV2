// git.js — the Git page. Manages the project's REPOSITORIES (add/remove, activate/deactivate, and add
// from the project's GitHub account) and shows live Git state (worktrees). Deactivating every repo turns
// the project OFF — agents then refuse to work on it.
import { api } from "api";
import { el, card, sectionHead, badge, table, button, emptyState } from "ui";

// Discover repos from the GitHub account and let the user add the ones they want.
function discover(existingNames, onAdd) {
  const listEl = el("div", { class: "fp-list" }, el("div", { class: "sub", style: { padding: "10px 12px" } }, "Loading repos…"));
  const owner = el("input", { class: "login-input", placeholder: "org / user (blank = project default)", style: { maxWidth: "260px" } });
  const reload = button("Reload", {});
  const closeBtn = button("Close", {});
  async function go() {
    listEl.replaceChildren(el("div", { class: "sub", style: { padding: "10px 12px" } }, "Loading…"));
    try {
      const repos = (await api.query("github.repos", owner.value.trim() ? { owner: owner.value.trim() } : undefined)) || [];
      listEl.replaceChildren(
        ...repos.map((r) => {
          const added = existingNames.has(String(r.name).toLowerCase());
          const b = button(added ? "added" : "＋ add", { tone: added ? "" : "primary" });
          b.disabled = added;
          b.onclick = async () => {
            b.textContent = "…";
            b.disabled = true;
            try {
              await api.command("repos.add", { name: r.name, url: r.url });
              b.textContent = "added";
              existingNames.add(String(r.name).toLowerCase());
              onAdd && onAdd();
            } catch (e) {
              b.textContent = "＋ add";
              b.disabled = false;
            }
          };
          return el(
            "div",
            { class: "fp-row", style: { cursor: "default", display: "flex", alignItems: "center", gap: "8px" } },
            el("span", { class: "mono" }, r.name),
            r.archived ? badge("archived", "warn") : null,
            r.private ? badge("private", "info") : null,
            el("span", { style: { marginLeft: "auto" } }, b)
          );
        })
      );
      if (!repos.length) listEl.replaceChildren(el("div", { class: "notice" }, "No repos found."));
    } catch (e) {
      listEl.replaceChildren(el("div", { class: "notice danger" }, "Failed: " + (e && e.message ? e.message : e)));
    }
  }
  reload.onclick = go;
  const overlay = el(
    "div",
    { class: "drawer-overlay" },
    el(
      "div",
      { class: "drawer", style: { maxWidth: "560px", height: "72vh" } },
      el("div", { class: "drawer-head" }, el("span", { class: "drawer-title" }, "Add repos from GitHub"), closeBtn),
      el("div", { style: { padding: "12px 16px", display: "flex", gap: "8px", alignItems: "center" } }, el("span", { class: "sub" }, "owner"), owner, reload),
      el("div", { style: { padding: "0 16px 16px", overflow: "auto", flex: 1 } }, listEl)
    )
  );
  closeBtn.onclick = () => overlay.remove();
  overlay.onclick = (e) => e.target === overlay && overlay.remove();
  document.body.append(overlay);
  go();
}

export default {
  title: "Git",
  async render(outlet) {
    const reposBody = el("div", {}, el("div", { class: "notice" }, "Loading…"));
    const wtBody = el("div", {}, el("div", { class: "notice" }, "Loading…"));
    const gate = el("div");
    const addBtn = button("＋ Add from GitHub", { tone: "primary" });
    const manualBtn = button("Add manually", {});

    outlet.append(
      sectionHead("Git", "Repositories the agents may work on. Deactivate a repo to take it out of work; deactivate ALL to turn the project off.", el("span", { class: "run-control" }, manualBtn, addBtn)),
      gate,
      card("Repositories", reposBody),
      card("Active worktrees", wtBody, { sub: "live git state" })
    );

    async function loadRepos() {
      try {
        const repos = (await api.query("repos.list")) || [];
        const anyActive = repos.some((r) => r.active);
        gate.replaceChildren(
          repos.length && !anyActive
            ? el("div", { class: "notice danger", style: { marginBottom: "14px" } }, "⛔ Project deactivated — every repository is off, so agents will not work. Activate a repo to resume.")
            : el("span")
        );
        reposBody.replaceChildren(
          repos.length
            ? table(
                [
                  { key: "name", label: "Repository", mono: true },
                  { key: "url", label: "URL", render: (r) => el("span", { class: "mono sub" }, r.url || "—") },
                  { key: "active", label: "State", render: (r) => badge(r.active ? "active" : "inactive", r.active ? "ok" : "danger") },
                  {
                    key: "toggle",
                    label: "",
                    render: (r) => {
                      const b = button(r.active ? "Deactivate" : "Activate", { tone: r.active ? "" : "primary" });
                      b.onclick = async () => {
                        b.textContent = "…";
                        b.disabled = true;
                        try {
                          await api.command("repos.setActive", { name: r.name, active: !r.active });
                        } catch (e) {
                          /* shown on reload */
                        }
                        loadRepos();
                      };
                      return b;
                    },
                  },
                  {
                    key: "del",
                    label: "",
                    render: (r) => {
                      const b = button("Delete", { tone: "danger" });
                      b.onclick = async () => {
                        if (!confirm(`Remove ${r.name} from this project?`)) return;
                        await api.command("repos.remove", { name: r.name });
                        loadRepos();
                      };
                      return b;
                    },
                  },
                ],
                repos
              )
            : emptyState("No repositories", "Add one from GitHub or manually.")
        );
      } catch (e) {
        reposBody.replaceChildren(el("div", { class: "notice danger" }, "Failed: " + (e && e.message ? e.message : e)));
      }
    }

    async function loadWorktrees() {
      try {
        const wts = (await api.query("git.worktrees")) || [];
        wtBody.replaceChildren(
          wts.length
            ? table(
                [
                  { key: "branch", label: "Branch", mono: true, render: (r) => (r.branch || "").replace("refs/heads/", "") },
                  { key: "path", label: "Worktree", mono: true },
                  { key: "head", label: "HEAD", mono: true, render: (r) => (r.head || "").slice(0, 10) || "—" },
                ],
                wts
              )
            : emptyState("No active worktrees", "Worktrees appear while a task is running.")
        );
      } catch (e) {
        wtBody.replaceChildren(el("div", { class: "notice" }, "Worktrees unavailable."));
      }
    }

    manualBtn.onclick = async () => {
      const name = (prompt("Repository name:") || "").trim();
      if (!name) return;
      const url = (prompt(`Git URL for ${name} (optional):`) || "").trim();
      try {
        await api.command("repos.add", { name, url });
        loadRepos();
      } catch (e) {
        alert("Failed: " + (e && e.message ? e.message : e));
      }
    };
    addBtn.onclick = async () => {
      const repos = (await api.query("repos.list").catch(() => [])) || [];
      discover(new Set(repos.map((r) => String(r.name).toLowerCase())), loadRepos);
    };

    loadRepos();
    loadWorktrees();
  },
};
