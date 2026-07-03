// projects.js — the projects page. Each project is a FULLY ISOLATED backend (own repo, Jira, creds,
// tasks + data) served on the SAME url at /p/<slug>/. Lists projects, opens them, and adds a new one
// from the console (scaffolds + starts it) with a server-side folder picker for the repo.
import { api } from "api";
import { el, card, sectionHead, button, badge } from "ui";

// folderPicker opens a read-only server-side directory browser; on pick it sets `input.value`.
function folderPicker(input) {
  const crumb = el("div", { class: "mono sub", style: { marginBottom: "8px", wordBreak: "break-all" } }, "…");
  const listEl = el("div", { class: "fp-list" });
  const useBtn = button("Use this folder", { tone: "primary" });
  const closeBtn = button("Cancel", {});
  let cur = input.value || "";

  async function go(path) {
    try {
      const r = await api.query("fs.dirs", path ? { path } : undefined);
      cur = r.path;
      crumb.textContent = r.path;
      const rows = [];
      rows.push(rowEl("⬆ ..", () => go(r.parent)));
      (r.dirs || []).forEach((d) => rows.push(rowEl("📁 " + d, () => go(r.path.replace(/\/$/, "") + "/" + d))));
      listEl.replaceChildren(...(rows.length ? rows : [el("div", { class: "sub" }, "(no sub-folders)")]));
    } catch (e) {
      listEl.replaceChildren(el("div", { class: "notice danger" }, "Cannot read: " + (e.message || e)));
    }
  }
  function rowEl(label, onClick) {
    const d = el("div", { class: "fp-row" }, label);
    d.onclick = onClick;
    return d;
  }
  const overlay = el(
    "div",
    { class: "drawer-overlay" },
    el(
      "div",
      { class: "drawer", style: { maxWidth: "560px", height: "70vh" } },
      el("div", { class: "drawer-head" }, el("span", { class: "drawer-title" }, "Select the project folder"), closeBtn),
      el("div", { style: { padding: "14px 16px", overflow: "auto", flex: 1 } }, crumb, listEl),
      el("div", { class: "drawer-foot" }, useBtn)
    )
  );
  const close = () => overlay.remove();
  closeBtn.onclick = close;
  useBtn.onclick = () => {
    input.value = cur;
    input.dispatchEvent(new Event("input"));
    close();
  };
  overlay.onclick = (e) => e.target === overlay && close();
  document.body.append(overlay);
  go(cur);
}

function field(label, input) {
  return el("label", { class: "pf-field" }, el("span", { class: "pf-label" }, label), input);
}

function openAddForm(onDone) {
  const name = el("input", { class: "login-input", placeholder: "e.g. Chida" });
  const port = el("input", { class: "login-input", type: "number", value: "8788", placeholder: "8788" });
  const repo = el("input", { class: "login-input", placeholder: "git URL or local folder path" });
  const browse = button("📁 Browse…", {});
  browse.onclick = () => folderPicker(repo);
  const branch = el("input", { class: "login-input", value: "development" });
  const jiraUrl = el("input", { class: "login-input", placeholder: "https://your.atlassian.net" });
  const jiraEmail = el("input", { class: "login-input", placeholder: "jira email (optional)" });
  const jiraTok = el("input", { class: "login-input", type: "password", placeholder: "jira API token (optional)" });
  const ghTok = el("input", { class: "login-input", type: "password", placeholder: "github token (optional)" });
  const err = el("div", { class: "login-err" });
  const createBtn = button("Create project", { tone: "primary" });
  const closeBtn = button("Cancel", {});

  createBtn.onclick = async () => {
    err.textContent = "";
    createBtn.textContent = "Creating…";
    createBtn.disabled = true;
    try {
      const res = await api.command("projects.create", {
        name: name.value.trim(),
        port: parseInt(port.value, 10) || 0,
        repo: repo.value.trim(),
        dev_branch: branch.value.trim(),
        jira_url: jiraUrl.value.trim(),
        jira_email: jiraEmail.value.trim(),
        jira_token: jiraTok.value.trim(),
        github_token: ghTok.value.trim(),
      });
      overlay.remove();
      alert(`Created “${res.name}” at ${res.base}/\n\n${res.note || ""}`);
      onDone && onDone();
    } catch (e) {
      createBtn.textContent = "Create project";
      createBtn.disabled = false;
      err.textContent = e && e.message ? e.message : String(e);
    }
  };

  const overlay = el(
    "div",
    { class: "drawer-overlay" },
    el(
      "div",
      { class: "drawer", style: { maxWidth: "540px" } },
      el("div", { class: "drawer-head" }, el("span", { class: "drawer-title" }, "Add a project (separate & isolated)"), closeBtn),
      el(
        "div",
        { style: { padding: "16px", overflow: "auto" } },
        field("Name", name),
        field("Port (unique)", port),
        field("Repo", el("div", { class: "run-control" }, repo, browse)),
        field("Dev branch", branch),
        field("Jira URL", jiraUrl),
        field("Jira email", jiraEmail),
        field("Jira token", jiraTok),
        field("GitHub token", ghTok),
        err
      ),
      el("div", { class: "drawer-foot" }, createBtn)
    )
  );
  closeBtn.onclick = () => overlay.remove();
  overlay.onclick = (e) => e.target === overlay && overlay.remove();
  document.body.append(overlay);
  setTimeout(() => name.focus(), 50);
}

export default {
  title: "Projects",
  async render(outlet) {
    const body = el("div", {}, el("div", { class: "notice" }, "Loading…"));
    const addBtn = button("＋ Add project", { tone: "primary" });
    outlet.append(
      sectionHead("Projects", "Each project is a fully isolated backend on the SAME url (/p/<name>/) — own repo, Jira, credentials, tasks and data. Nothing shared.", addBtn),
      card("Projects", body)
    );

    async function load() {
      try {
        const list = (await api.query("projects.list")) || [];
        body.replaceChildren(
          el(
            "div",
            { class: "feed" },
            ...list.map((p) => {
              const open = button("Open →", {});
              open.onclick = () => (location.href = (p.base || "") + "/");
              return el(
                "div",
                { class: "feed-row" },
                el("span", { class: "sys" }, p.name),
                el("span", { class: "mono sub" }, (p.base || "/") + " "),
                badge(p.base ? "sub-project" : "default", p.base ? "info" : "ok"),
                el("span", { style: { marginLeft: "auto" } }, open)
              );
            })
          )
        );
      } catch (e) {
        body.replaceChildren(el("div", { class: "notice danger" }, "Failed: " + (e && e.message ? e.message : e)));
      }
    }

    addBtn.onclick = () => openAddForm(load);
    load();
  },
};
