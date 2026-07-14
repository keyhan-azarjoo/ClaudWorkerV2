// aiw/settings.js — AI Workspace settings: the local companion connection, appearance, and info. The
// companion is an optional localhost daemon that does heavy work (indexing, embeddings, vector DB,
// proxy); until it exists this connects to its contract and degrades gracefully when absent.
import { api } from "api";
import { el, card, sectionHead, badge, button } from "ui";

function companionSection() {
  const body = el("div", {}, el("div", { class: "notice" }, "Loading…"));
  const wrap = card("Local companion", body, { sub: "optional localhost daemon" });

  async function load() {
    let st;
    try {
      st = await api.query("aiw.companion.status");
    } catch (e) {
      body.replaceChildren(el("div", { class: "notice danger" }, "Failed: " + (e.message || e)));
      return;
    }
    const url = el("input", { class: "login-input", placeholder: "http://127.0.0.1:8765", value: st.url || "", style: { maxWidth: "320px" } });
    const err = el("div", { class: "login-err" });
    const connect = button("Connect", { tone: "primary" });
    const disconnect = button("Disconnect", {});
    connect.onclick = async () => {
      err.textContent = "";
      connect.disabled = true;
      connect.textContent = "Connecting…";
      try {
        await api.command("aiw.companion.connect", { url: url.value.trim() });
      } catch (e) {
        err.textContent = e.message || String(e);
      }
      connect.disabled = false;
      connect.textContent = "Connect";
      load();
    };
    disconnect.onclick = async () => { await api.command("aiw.companion.disconnect"); load(); };

    const statusRow = st.present
      ? el("div", { class: "aiw-set-status" }, badge("connected", "ok"), el("span", {}, st.url), (st.capabilities || []).length ? el("span", { class: "muted" }, "caps: " + st.capabilities.join(", ")) : el("span", { class: "muted" }, "no capabilities reported"))
      : el("div", { class: "aiw-set-status" }, badge(st.configured ? "unreachable" : "not connected", st.configured ? "warn" : ""), st.error ? el("span", { class: "muted" }, st.error) : el("span", { class: "muted" }, "No companion daemon is running — companion-only features are disabled."));

    body.replaceChildren(
      el("div", { class: "aiw-opt-desc" }, "Point AI Workspace at a local companion daemon to enable repository indexing, local embeddings, a vector DB and the optimizing proxy. Must be a localhost address."),
      statusRow,
      el("div", { class: "aiw-prov-actions" }, url, connect, st.configured ? disconnect : null),
      err
    );
  }
  load();
  return wrap;
}

function appearanceSection() {
  const current = document.documentElement.getAttribute("data-theme") || "dark";
  const sel = el("select", { class: "login-input", style: { maxWidth: "200px" } }, el("option", { value: "dark" }, "Dark"), el("option", { value: "light" }, "Light"));
  sel.value = current;
  sel.onchange = () => {
    document.documentElement.setAttribute("data-theme", sel.value);
    localStorage.setItem("oc.theme", sel.value);
  };
  return card("Appearance", el("label", { class: "pf-field" }, el("span", { class: "pf-label" }, "Theme"), sel));
}

function infoSection() {
  return card(
    "About",
    el(
      "div",
      { class: "aiw-opt-desc" },
      "AI Workspace is local-first: providers' API keys live in your OS keychain, usage and caches are per-project JSON stores, and no data leaves this machine unless you connect a cloud provider. Heavy/at-scale work is delegated to the optional local companion."
    )
  );
}

export default {
  title: "Settings",
  async render(outlet) {
    outlet.append(
      sectionHead("Settings", "Companion connection, appearance and info.", null),
      el("div", { class: "aiw-set-grid" }, companionSection(), appearanceSection(), infoSection())
    );
  },
};
