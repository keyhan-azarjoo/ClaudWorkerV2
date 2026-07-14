// aiw/cache.js — the intelligent cache. Real content-addressed cache fed by optimizer runs (identical
// optimizer+config+content is served from cache), so the hit ratio is live. View stats, per-kind
// rollups and entries; pin, expire (delete one), or clear.
import { api } from "api";
import { el, card, sectionHead, badge, button, table, emptyState } from "ui";
import { donut, fmtTokens, fmtBytes } from "./shared/charts.js";

function stat(label, value) {
  return el("div", { class: "aiw-tile" }, el("div", { class: "aiw-tile-label" }, label), el("div", { class: "aiw-tile-value" }, value));
}

export default {
  title: "Cache",
  async render(outlet) {
    const clearBtn = button("Clear cache", { tone: "danger" });
    const head = sectionHead("Cache", "Content-addressed cache. Optimizer results are cached — identical inputs are served instantly.", clearBtn);
    const statsWrap = el("div", {});
    const listWrap = el("div", {});
    outlet.append(head, statsWrap, el("div", { style: { height: "16px" } }), listWrap);

    clearBtn.onclick = async () => {
      if (!confirm("Clear all unpinned cache entries?")) return;
      const r = await api.command("aiw.cache.clear", {});
      alert("Removed " + (r.removed || 0) + " entries.");
      load();
    };

    async function load() {
      let stats, list;
      try {
        [stats, list] = await Promise.all([api.query("aiw.cache.stats"), api.query("aiw.cache.list", { limit: 100 })]);
      } catch (e) {
        statsWrap.replaceChildren(el("div", { class: "notice danger" }, "Failed: " + (e && e.message ? e.message : e)));
        return;
      }
      const kinds = stats.byKind || [];
      statsWrap.replaceChildren(
        el(
          "div",
          { class: "aiw-cache-stats" },
          el("div", { class: "aiw-donut-hold" }, donut(stats.hitRatio || 0, { label: "Hit ratio" })),
          el(
            "div",
            { class: "aiw-tiles", style: { flex: "1" } },
            stat("Entries", String(stats.entries || 0)),
            stat("Saved (est)", fmtTokens(stats.savedTokens || 0)),
            stat("Hits / Misses", (stats.hits || 0) + " / " + (stats.misses || 0)),
            stat("Size", fmtBytes(stats.bytes || 0))
          )
        ),
        kinds.length
          ? card("By kind", table(
              [
                { key: "kind", label: "Kind" },
                { key: "count", label: "Entries" },
                { key: "hits", label: "Hits" },
                { key: "tokens", label: "Tokens", render: (r) => fmtTokens(r.tokens) },
                { key: "bytes", label: "Size", render: (r) => fmtBytes(r.bytes) },
              ],
              kinds
            ), { flush: true })
          : null
      );

      const rows = (list || []).map((e) => ({
        ...e,
        _pin: (() => {
          const b = button(e.pinned ? "Unpin" : "Pin", {});
          b.onclick = async () => { await api.command("aiw.cache.pin", { key: e.key, pinned: !e.pinned }); load(); };
          return b;
        })(),
        _del: (() => {
          const b = button("Expire", { tone: "danger" });
          b.onclick = async () => { await api.command("aiw.cache.expire", { key: e.key }); load(); };
          return b;
        })(),
      }));
      listWrap.replaceChildren(
        rows.length
          ? card("Entries", table(
              [
                { key: "label", label: "Label" },
                { key: "kind", label: "Kind", render: (r) => badge(r.kind, "") },
                { key: "hits", label: "Hits" },
                { key: "tokens", label: "Tokens", render: (r) => fmtTokens(r.tokens) },
                { key: "bytes", label: "Size", render: (r) => fmtBytes(r.bytes) },
                { key: "pinned", label: "", render: (r) => (r.pinned ? badge("pinned", "ok") : "") },
                { key: "_pin", label: "", render: (r) => r._pin },
                { key: "_del", label: "", render: (r) => r._del },
              ],
              rows
            ), { flush: true })
          : card(null, emptyState("Cache is empty", "Run an optimizer twice on the same input — the second run is a cache hit."), { flush: true })
      );
    }
    load();
  },
};
