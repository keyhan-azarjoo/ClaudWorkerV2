// api.js — the ONLY way the Operations Console talks to the backend. Every action is a Control Plane
// API call; the frontend holds no business logic and never touches a subsystem directly.

const KEY_BASE = "oc.base";
const KEY_TOKEN = "oc.token";

export function config() {
  return {
    base: localStorage.getItem(KEY_BASE) || "", // "" = same origin as the console
    token: localStorage.getItem(KEY_TOKEN) || "",
  };
}

export function setConfig({ base, token }) {
  if (base !== undefined) localStorage.setItem(KEY_BASE, base);
  if (token !== undefined) localStorage.setItem(KEY_TOKEN, token);
}

export function authHeaders() {
  const { token } = config();
  return token ? { Authorization: `Bearer ${token}` } : {};
}

// NotWired signals that a named query/command has no data source registered on the Control Plane yet
// (HTTP 404). Modules render a friendly "not yet available" state instead of an error.
export class NotWired extends Error {}

async function req(path, opts = {}) {
  const { base } = config();
  const res = await fetch(base + path, {
    ...opts,
    headers: { ...authHeaders(), ...(opts.headers || {}) },
  });
  let body = {};
  try {
    body = await res.json();
  } catch {
    /* non-JSON */
  }
  if (res.status === 404) throw new NotWired(body.error || "not registered");
  if (!res.ok || body.ok === false) throw new Error(body.error || `HTTP ${res.status}`);
  return body.data;
}

export const api = {
  query: (name, params) => req(`/v1/query/${encodeURIComponent(name)}${params ? "?" + new URLSearchParams(params) : ""}`),
  command: (name, body) =>
    req(`/v1/command/${encodeURIComponent(name)}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body || {}),
    }),
  status: () => req("/v1/status"),
  metrics: () => req("/v1/metrics"),
  queries: () => req("/v1/queries"),
  commands: () => req("/v1/commands"),
  health: () => req("/v1/healthz"),
  eventsUrl: (lastSeq) => `${config().base}/v1/events${lastSeq ? `?last_event_id=${lastSeq}` : ""}`,
};
