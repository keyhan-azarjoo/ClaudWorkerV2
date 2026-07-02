# Operations Console (S10B)

The frontend client of the ClaudWorker V2 **Control Plane** (S10A). The Dashboard is only its landing
page; the console is a modular application.

## What it is

- **Framework-free, no build step.** Plain ES modules + Web-standard APIs + an import map — no bundler,
  no `node_modules`, no packaging. Honours the project's zero-dependency / local-first rules. (This is
  the "React-or-equivalent" component architecture: reusable components, a design system, and
  lazy-loaded modules, built on web standards.)
- **Presentation, navigation, state, visualization — and nothing else.** All business logic lives in
  the Control Plane. Every action is an API call (`js/api.js`); no module touches an engine subsystem
  directly.
- **Event-driven.** Live data arrives over the Control Plane SSE stream (`js/events.js`); the UI
  reacts to events and re-queries only when a relevant event fires — it never polls when an event
  exists. The backend stays authoritative.
- **Design:** dark-mode first, desktop-first, tablet-friendly/responsive; a small consistent design
  system (`css/app.css`); professional engineering-platform feel.

## Modules (lazy-loaded)

Dashboard · Projects · Jira · Assignments · Verification · Improvement · Policies · Resources ·
Leases · AI Runtimes · Accounts · Knowledge · Metrics · Logs · Settings.

Each is an independent ES module under `js/modules/`, imported on first navigation. Most are built from
the reusable `listModule` factory in `js/ui.js`; Dashboard, Metrics, Logs and Settings are bespoke.

## Structure

```
web/ops-console/
  index.html            # shell + import map
  css/app.css           # design system (dark-first)
  js/
    api.js              # Control Plane API client (the only backend access)
    events.js           # SSE stream (fetch-based → sends auth; auto-reconnect + Last-Event-ID)
    store.js            # reactive presentation state + event ring
    router.js           # hash router + lazy import()
    ui.js               # el(), reusable components, icons, listModule factory
    app.js              # shell boot: sidebar nav, connection indicator, mounts router
    modules/*.js        # the 15 console modules
```

## Run it

No build. Serve the folder statically and point it at a running Control Plane:

```sh
# from web/ops-console/
python3 -m http.server 5173
# open http://localhost:5173  → Settings → set API base URL + token → Save
```

If the Control Plane serves these static files itself (same origin), leave the API base URL blank.

Until the engine's `serve` loop registers queries and starts publishing events, modules show a
friendly "not yet available" state — they populate live once the Control Plane exposes data.

## Not in scope (S10B)

Production packaging (bundling, minification, Docker, CDN) is intentionally **not** implemented.
