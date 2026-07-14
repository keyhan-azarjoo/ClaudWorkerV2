// aiw/providers.js — manage AI providers. Multiple providers, each with multiple accounts (keys/orgs),
// a default, priority, enable/disable, and a free "Test connection" (model-list only, never a paid
// call). API keys are entered here but stored in the OS keychain by the backend — the UI only ever shows
// a masked ••••1234 hint, never the raw key.
import { api } from "api";
import { el, card, sectionHead, badge, button, emptyState } from "ui";

function field(label, input) {
  return el("label", { class: "pf-field" }, el("span", { class: "pf-label" }, label), input);
}

function drawer(titleText, bodyNode, footNode, maxW = "560px") {
  const closeBtn = button("Cancel", {});
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

// addProviderForm — pick a kind (from the backend catalog), name + base URL prefilled from the kind.
async function addProviderForm(onDone) {
  let kinds = [];
  try {
    kinds = (await api.query("aiw.provider.kinds")) || [];
  } catch {}
  const kindSel = el("select", { class: "login-input" }, ...kinds.map((k) => el("option", { value: k.kind }, k.label + (k.local ? "  (free)" : ""))));
  const name = el("input", { class: "login-input", placeholder: "Friendly name" });
  const baseURL = el("input", { class: "login-input", placeholder: "Base URL" });
  const hint = el("div", { class: "pf-label" });
  const err = el("div", { class: "login-err" });
  function applyKind() {
    const k = kinds.find((x) => x.kind === kindSel.value);
    if (!k) return;
    if (!name.value) name.value = k.label;
    baseURL.value = k.defaultBaseURL || "";
    hint.textContent = k.local ? "Local & free — no API key or money required." : (k.canTest ? "Cloud provider — add an API key after creating it." : "Custom endpoint.");
  }
  kindSel.onchange = () => { name.value = ""; applyKind(); };
  applyKind();

  const saveBtn = button("Add provider", { tone: "primary" });
  const overlay = drawer(
    "Add a provider",
    el("div", {}, field("Provider", kindSel), field("Name", name), field("Base URL", baseURL), hint, err),
    saveBtn
  );
  saveBtn.onclick = async () => {
    err.textContent = "";
    saveBtn.disabled = true;
    saveBtn.textContent = "Adding…";
    try {
      await api.command("aiw.provider.add", { kind: kindSel.value, name: name.value.trim(), baseURL: baseURL.value.trim() });
      overlay.remove();
      onDone && onDone();
    } catch (e) {
      saveBtn.disabled = false;
      saveBtn.textContent = "Add provider";
      err.textContent = e && e.message ? e.message : String(e);
    }
  };
}

// accountForm — add or edit one account (key goes to the keychain; leave key blank when editing to keep).
function accountForm(providerId, acct, onDone) {
  const label = el("input", { class: "login-input", value: (acct && acct.label) || "", placeholder: "e.g. work, personal" });
  const org = el("input", { class: "login-input", value: (acct && acct.org) || "", placeholder: "Organization (optional)" });
  const model = el("input", { class: "login-input", value: (acct && acct.defaultModel) || "", placeholder: "Default model (optional)" });
  const key = el("input", { class: "login-input", type: "password", placeholder: acct && acct.hasKey ? "•••• (leave blank to keep current)" : "API key", autocomplete: "off" });
  const err = el("div", { class: "login-err" });
  const saveBtn = button(acct ? "Save" : "Add account", { tone: "primary" });
  const overlay = drawer(
    acct ? "Edit account" : "Add account",
    el("div", {}, field("Label", label), field("Organization", org), field("Default model", model), field("API key", key), el("div", { class: "pf-label" }, "Keys are stored in your OS keychain — never in a file, never shown again."), err),
    saveBtn
  );
  saveBtn.onclick = async () => {
    err.textContent = "";
    saveBtn.disabled = true;
    saveBtn.textContent = "Saving…";
    try {
      if (acct) await api.command("aiw.account.update", { providerId, accountId: acct.id, label: label.value.trim(), org: org.value.trim(), key: key.value, model: model.value.trim() });
      else await api.command("aiw.account.add", { providerId, label: label.value.trim(), org: org.value.trim(), key: key.value, model: model.value.trim() });
      overlay.remove();
      onDone && onDone();
    } catch (e) {
      saveBtn.disabled = false;
      saveBtn.textContent = acct ? "Save" : "Add account";
      err.textContent = e && e.message ? e.message : String(e);
    }
  };
}

function providerCard(p, reload) {
  const kindBadge = badge(p.kind, "");
  const head = el(
    "div",
    { class: "aiw-prov-head" },
    el("span", { class: "aiw-prov-name" }, p.name),
    kindBadge,
    p.isDefault ? badge("default", "ok") : null,
    p.enabled ? null : badge("disabled", "danger"),
    el("span", { style: { marginLeft: "auto" }, class: "aiw-prov-url" }, p.baseURL || "")
  );

  // Accounts (masked keys)
  const accts = (p.accounts || []).length
    ? el(
        "div",
        { class: "aiw-accts" },
        ...p.accounts.map((a) => {
          const edit = button("Edit", {});
          edit.onclick = () => accountForm(p.id, a, reload);
          const del = button("Remove", { tone: "danger" });
          del.onclick = async () => {
            if (!confirm(`Remove account "${a.label}"? Its key is deleted from the keychain.`)) return;
            await api.command("aiw.account.remove", { providerId: p.id, accountId: a.id });
            reload();
          };
          return el(
            "div",
            { class: "aiw-acct" },
            el("span", { class: "aiw-acct-label" }, a.label || "account"),
            a.org ? badge(a.org, "") : null,
            el("span", { class: "aiw-acct-key mono" }, a.hasKey ? "••••" + (a.keyHint || "") : "no key"),
            a.defaultModel ? el("span", { class: "aiw-acct-model" }, a.defaultModel) : null,
            el("span", { style: { marginLeft: "auto" }, class: "run-control" }, edit, del)
          );
        })
      )
    : el("div", { class: "aiw-accts-empty" }, "No accounts yet.");

  // Actions
  const addAcct = button("＋ Account", {});
  addAcct.onclick = () => accountForm(p.id, null, reload);
  const testBtn = button("Test", {});
  const testMsg = el("span", { class: "aiw-test-msg" }, p.lastTestAt ? (p.lastTestOK ? "✓ " : "✕ ") + (p.lastTestMsg || "") : "");
  testMsg.classList.toggle("ok", !!p.lastTestOK);
  testMsg.classList.toggle("bad", p.lastTestAt && !p.lastTestOK);
  testBtn.onclick = async () => {
    testBtn.disabled = true;
    testMsg.textContent = "Testing…";
    testMsg.className = "aiw-test-msg";
    try {
      const r = await api.command("aiw.provider.test", { id: p.id });
      testMsg.textContent = (r.ok ? "✓ " : "✕ ") + r.message;
      testMsg.classList.add(r.ok ? "ok" : "bad");
    } catch (e) {
      testMsg.textContent = "✕ " + (e.message || e);
      testMsg.classList.add("bad");
    }
    testBtn.disabled = false;
    setTimeout(reload, 800);
  };
  const defBtn = button("Set default", {});
  defBtn.disabled = !!p.isDefault;
  defBtn.onclick = async () => { await api.command("aiw.provider.setDefault", { id: p.id }); reload(); };
  const enBtn = button(p.enabled ? "Disable" : "Enable", { tone: p.enabled ? "" : "primary" });
  enBtn.onclick = async () => { await api.command("aiw.provider.enable", { id: p.id, enabled: !p.enabled }); reload(); };
  const delBtn = button("Delete", { tone: "danger" });
  delBtn.onclick = async () => {
    if (!confirm(`Delete provider "${p.name}"? All its account keys are removed from the keychain.`)) return;
    await api.command("aiw.provider.remove", { id: p.id });
    reload();
  };

  const actions = el("div", { class: "aiw-prov-actions" }, addAcct, testBtn, testMsg, el("span", { class: "spacer", style: { flex: 1 } }), defBtn, enBtn, delBtn);
  return el("div", { class: "aiw-prov-card" + (p.enabled ? "" : " off") }, head, accts, actions);
}

export default {
  title: "Providers",
  async render(outlet) {
    const body = el("div", {}, el("div", { class: "notice" }, "Loading…"));
    const addBtn = button("＋ Add provider", { tone: "primary" });
    outlet.append(
      sectionHead("Providers", "Connect one or more AI providers. Keys are stored in your OS keychain and shown only as ••••. The local Ollama option is free.", addBtn),
      body
    );
    async function load() {
      try {
        const ps = (await api.query("aiw.providers.list")) || [];
        body.replaceChildren(
          ps.length ? el("div", { class: "aiw-prov-list" }, ...ps.map((p) => providerCard(p, load))) : emptyState("No providers yet", "Add a provider to start — try Ollama (local, free) or your Anthropic/OpenAI key.")
        );
      } catch (e) {
        body.replaceChildren(el("div", { class: "notice danger" }, "Failed: " + (e && e.message ? e.message : e)));
      }
    }
    addBtn.onclick = () => addProviderForm(load);
    load();
  },
};
