// store.js — minimal reactive state management (presentation state only; the backend is
// authoritative). It keeps connection status, a rolling event log for the Logs/Dashboard modules,
// and live per-type/subsystem counters derived purely from the event stream.

const MAX_EVENTS = 500;

function createStore() {
  const state = {
    connected: false,
    events: [], // most recent last
    counts: {}, // event type -> count (session)
    subsystems: {}, // subsystem -> count
    lastSeq: 0,
  };
  const subs = new Set();

  function notify() {
    for (const fn of subs) fn(state);
  }

  return {
    get: () => state,
    subscribe(fn) {
      subs.add(fn);
      fn(state);
      return () => subs.delete(fn);
    },
    setConnected(v) {
      state.connected = v;
      notify();
    },
    pushEvent(ev) {
      state.events.push(ev);
      if (state.events.length > MAX_EVENTS) state.events.shift();
      state.counts[ev.type] = (state.counts[ev.type] || 0) + 1;
      if (ev.subsystem) state.subsystems[ev.subsystem] = (state.subsystems[ev.subsystem] || 0) + 1;
      state.lastSeq = ev.seq || state.lastSeq;
      notify();
    },
  };
}

export const store = createStore();
