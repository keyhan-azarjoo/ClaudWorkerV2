// charts.js — tiny, dependency-free SVG charts for AI Workspace (no chart library, framework-free).
// Everything is currentColor / CSS-variable driven so it inherits the dark theme.

const NS = "http://www.w3.org/2000/svg";
function svg(tag, attrs = {}) {
  const n = document.createElementNS(NS, tag);
  for (const [k, v] of Object.entries(attrs)) n.setAttribute(k, v);
  return n;
}

// sparkline(values, {w,h,tone}) — a smooth-ish area+line for a small series (e.g. 14-day tokens).
export function sparkline(values, { w = 220, h = 44, tone = "accent" } = {}) {
  const s = svg("svg", { viewBox: `0 0 ${w} ${h}`, class: "aiw-spark " + tone, preserveAspectRatio: "none" });
  const vals = (values && values.length ? values : [0]).map((v) => (Number.isFinite(v) ? v : 0));
  const max = Math.max(1, ...vals);
  const n = vals.length;
  const x = (i) => (n <= 1 ? 0 : (i / (n - 1)) * w);
  const y = (v) => h - 3 - (v / max) * (h - 6);
  const pts = vals.map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`);
  const line = svg("polyline", { points: pts.join(" "), class: "aiw-spark-line", fill: "none" });
  const area = svg("polygon", { points: `0,${h} ${pts.join(" ")} ${w},${h}`, class: "aiw-spark-area" });
  s.append(area, line);
  return s;
}

// donut(pct, {size,label}) — a ring gauge for a 0–100 percentage (compression, cache hit…).
export function donut(pct, { size = 92, label = "" } = {}) {
  pct = Math.max(0, Math.min(100, Math.round(pct || 0)));
  const r = size / 2 - 8;
  const c = 2 * Math.PI * r;
  const s = svg("svg", { viewBox: `0 0 ${size} ${size}`, class: "aiw-donut", width: size, height: size });
  const cx = size / 2;
  s.append(svg("circle", { cx, cy: cx, r, class: "aiw-donut-track", fill: "none" }));
  const arc = svg("circle", {
    cx, cy: cx, r, fill: "none", class: "aiw-donut-arc",
    "stroke-dasharray": `${((pct / 100) * c).toFixed(1)} ${c.toFixed(1)}`,
    transform: `rotate(-90 ${cx} ${cx})`,
  });
  s.append(arc);
  const t = svg("text", { x: cx, y: cx, class: "aiw-donut-text", "text-anchor": "middle", "dominant-baseline": "central" });
  t.textContent = pct + "%";
  s.append(t);
  const wrap = document.createElement("div");
  wrap.className = "aiw-donut-wrap";
  wrap.append(s);
  if (label) {
    const l = document.createElement("div");
    l.className = "aiw-donut-label";
    l.textContent = label;
    wrap.append(l);
  }
  return wrap;
}

// fmtTokens — compact human token counts (1.2k, 3.4M).
export function fmtTokens(n) {
  n = Number(n) || 0;
  if (n >= 1e6) return (n / 1e6).toFixed(1).replace(/\.0$/, "") + "M";
  if (n >= 1e3) return (n / 1e3).toFixed(1).replace(/\.0$/, "") + "k";
  return String(n);
}

// fmtBytes — compact human byte sizes.
export function fmtBytes(n) {
  n = Number(n) || 0;
  if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + " MB";
  if (n >= 1 << 10) return (n / (1 << 10)).toFixed(1) + " KB";
  return n + " B";
}

// hbars(rows) — a labelled horizontal bar list. rows = [{label, value, fmt?}]. Pure DOM (no SVG).
export function hbars(rows, { emptyText = "No data yet" } = {}) {
  const wrap = document.createElement("div");
  wrap.className = "aiw-hbars";
  if (!rows || !rows.length) {
    const e = document.createElement("div");
    e.className = "aiw-hbars-empty";
    e.textContent = emptyText;
    wrap.append(e);
    return wrap;
  }
  const max = Math.max(1, ...rows.map((r) => r.value || 0));
  for (const r of rows) {
    const row = document.createElement("div");
    row.className = "aiw-hbar";
    const label = document.createElement("span");
    label.className = "aiw-hbar-label";
    label.textContent = r.label;
    const track = document.createElement("span");
    track.className = "aiw-hbar-track";
    const fill = document.createElement("span");
    fill.className = "aiw-hbar-fill";
    fill.style.width = Math.round(((r.value || 0) / max) * 100) + "%";
    track.append(fill);
    const val = document.createElement("span");
    val.className = "aiw-hbar-val";
    val.textContent = r.fmt ? r.fmt(r.value) : fmtTokens(r.value);
    row.append(label, track, val);
    wrap.append(row);
  }
  return wrap;
}
