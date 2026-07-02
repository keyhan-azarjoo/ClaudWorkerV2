// router.js — hash-based router with lazy module loading. Each module is fetched with dynamic
// import() only when first navigated to (code-splitting without a bundler), satisfying "modules
// lazy-loaded where appropriate".

const loaders = new Map();
let current = null; // { dispose }

export function register(path, importer) {
  loaders.set(path, importer);
}

export function navigate(path) {
  if (location.hash !== "#" + path) location.hash = "#" + path;
  else resolve();
}

export function currentPath() {
  const h = location.hash.replace(/^#/, "");
  return h || "/dashboard";
}

let outlet = null;
let onChange = null;

export function start({ mount, onRouteChange }) {
  outlet = mount;
  onChange = onRouteChange;
  window.addEventListener("hashchange", resolve);
  resolve();
}

async function resolve() {
  const path = currentPath();
  const importer = loaders.get(path) || loaders.get("/dashboard");
  if (onChange) onChange(path);

  // tear down previous module
  if (current && typeof current.dispose === "function") {
    try {
      current.dispose();
    } catch (e) {
      console.error("dispose error", e);
    }
  }
  current = null;
  outlet.replaceChildren();

  try {
    const mod = await importer();
    const view = mod.default;
    const dispose = await view.render(outlet, ctx());
    current = { dispose };
  } catch (e) {
    console.error("route error", e);
    outlet.innerHTML = `<div class="empty"><div class="big">Failed to load module</div><div class="hint">${escapeHtml(
      e.message
    )}</div></div>`;
  }
}

// ctx is the shared context handed to every module (its live dependencies).
let sharedCtx = {};
export function setContext(c) {
  sharedCtx = c;
}
function ctx() {
  return sharedCtx;
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
}
