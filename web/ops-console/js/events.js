// events.js — live updates come through the Control Plane SSE stream. A fetch-based reader is used
// (not EewSource) so the Authorization header can be sent. It reconnects automatically and resumes
// from the last seen sequence via ?last_event_id, so nothing is missed within the server's ring.
//
// The UI reacts to events; it does NOT poll when an event already exists. The backend stays
// authoritative — events only trigger a re-query or a local render.

import { api, authHeaders } from "api";

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

export class EventStream {
  constructor() {
    this.handlers = new Set();
    this.stateHandlers = new Set();
    this.last = 0;
    this.stopped = false;
    this.connected = false;
  }

  // on(fn) subscribes to events; returns an unsubscribe function.
  on(fn) {
    this.handlers.add(fn);
    return () => this.handlers.delete(fn);
  }

  // onState(fn) subscribes to connection-state changes (true/false).
  onState(fn) {
    this.stateHandlers.add(fn);
    fn(this.connected);
    return () => this.stateHandlers.delete(fn);
  }

  _emit(ev) {
    for (const h of this.handlers) {
      try {
        h(ev);
      } catch (e) {
        console.error("event handler error", e);
      }
    }
  }

  _setConnected(v) {
    if (this.connected === v) return;
    this.connected = v;
    for (const h of this.stateHandlers) h(v);
  }

  async start() {
    this.stopped = false;
    while (!this.stopped) {
      try {
        await this._connect();
      } catch (e) {
        this._setConnected(false);
      }
      if (this.stopped) break;
      await sleep(2000); // backoff before reconnect
    }
  }

  stop() {
    this.stopped = true;
    this._setConnected(false);
    if (this.ctrl) this.ctrl.abort();
  }

  async _connect() {
    this.ctrl = new AbortController();
    const res = await fetch(api.eventsUrl(this.last), {
      headers: { Accept: "text/event-stream", ...authHeaders() },
      signal: this.ctrl.signal,
    });
    if (!res.ok || !res.body) throw new Error(`events HTTP ${res.status}`);
    this._setConnected(true);
    const reader = res.body.getReader();
    const dec = new TextDecoder();
    let buf = "";
    while (!this.stopped) {
      const { done, value } = await reader.read();
      if (done) break;
      buf += dec.decode(value, { stream: true });
      let idx;
      while ((idx = buf.indexOf("\n\n")) >= 0) {
        const frame = buf.slice(0, idx);
        buf = buf.slice(idx + 2);
        const ev = parseFrame(frame);
        if (ev) {
          if (ev.seq) this.last = ev.seq;
          this._emit(ev);
        }
      }
    }
    this._setConnected(false);
  }
}

function parseFrame(frame) {
  let data = null;
  for (const line of frame.split("\n")) {
    if (line.startsWith("data:")) data = line.slice(5).trim();
  }
  if (!data) return null;
  try {
    return JSON.parse(data);
  } catch {
    return null;
  }
}
