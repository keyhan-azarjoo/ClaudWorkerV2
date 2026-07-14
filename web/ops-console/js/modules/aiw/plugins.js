// aiw/plugins.js — the plugin registry. Every optimizer is a plugin; this lists them grouped by
// category and flags which run in the local companion. External plugin installation is a companion
// capability (shown honestly, not faked).
import { api } from "api";
import { el, card, sectionHead, badge, button } from "ui";

const CAT_ORDER = ["content", "context", "repo", "cache", "filter"];

export default {
  title: "Plugins",
  async render(outlet) {
    const body = el("div", {}, el("div", { class: "notice" }, "Loading…"));
    outlet.append(sectionHead("Plugins", "Every optimizer is a plugin. New plugins register with one file; external plugins need the companion.", null), body);

    let companion = { present: false };
    try {
      companion = await api.query("aiw.companion.status");
    } catch {}

    async function load() {
      let list;
      try {
        list = (await api.query("aiw.optimizers.list")) || [];
      } catch (e) {
        body.replaceChildren(el("div", { class: "notice danger" }, "Failed: " + (e.message || e)));
        return;
      }
      const byCat = {};
      for (const o of list) (byCat[o.meta.category] = byCat[o.meta.category] || []).push(o);
      const cats = Object.keys(byCat).sort((a, b) => CAT_ORDER.indexOf(a) - CAT_ORDER.indexOf(b));

      const installNote = el(
        "div",
        { class: "notice" },
        companion.present ? "Local companion connected — external plugins can be installed." : "Installing external plugins requires a local companion (Settings → Local companion)."
      );

      body.replaceChildren(
        installNote,
        ...cats.map((cat) =>
          card(
            cat + " (" + byCat[cat].length + ")",
            el(
              "div",
              { class: "aiw-plugin-list" },
              ...byCat[cat].map((o) =>
                el(
                  "div",
                  { class: "aiw-plugin" },
                  el("span", { class: "aiw-plugin-name" }, o.meta.name),
                  badge("v" + (o.meta.version || "1"), ""),
                  o.meta.requiresCompanion ? badge("needs companion", "warn") : badge("built-in", "ok"),
                  o.enabled === false ? badge("disabled", "danger") : null,
                  el("span", { class: "aiw-plugin-desc" }, o.meta.description)
                )
              )
            ),
            { flush: false }
          )
        )
      );
    }
    load();
  },
};
