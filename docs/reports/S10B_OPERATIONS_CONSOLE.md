# S10B — Operations Console (frontend)

Implements docs/21 S10B, **renamed Dashboard → Operations Console** (the Dashboard is only its landing
page). Location: `web/ops-console/`. A modular frontend that is purely a **client** of the Control
Plane API (S10A).

## Architecture — framework-free, no build

Built as a modern component architecture on web standards: **ES modules + Web APIs + an import map** —
**no bundler, no `node_modules`, no build step, no packaging**. This honours the project's
zero-dependency / local-first rules while delivering the "React-or-equivalent" goals: reusable
components, a consistent design system, and lazy-loaded modules. (Production packaging is intentionally
out of scope.)

```
web/ops-console/
  index.html            # shell + import map
  css/app.css           # design system (dark-first, desktop-first, responsive)
  js/api.js             # Control Plane client — the ONLY backend access
  js/events.js          # SSE stream (fetch-based → sends auth; reconnect + Last-Event-ID resume)
  js/store.js           # reactive presentation state + event ring
  js/router.js          # hash router + lazy import()
  js/ui.js              # el(), reusable components, icons, listModule factory
  js/app.js             # shell boot: grouped sidebar nav, live connection indicator, router
  js/modules/*.js       # 15 lazy-loaded modules
```

## Frontend rules — enforced

- **Presentation, navigation, state, visualization only.** No business logic anywhere in the frontend.
- **Every action calls the API** (`js/api.js`: `query` / `command` / `status` / `metrics` /
  `queries` / `commands` / `events`). No module imports or reaches a subsystem directly — the only
  backend surface is the Control Plane.
- **No duplicated logic.** Modules render whatever the Control Plane returns; tone/label mapping is
  presentation only.

## Live updates — event-driven, no needless polling

- All live data arrives via the Control Plane **SSE** stream. `events.js` uses a fetch-based reader so
  the `Authorization` header is sent, auto-reconnects, and resumes from the last sequence
  (`?last_event_id`) so nothing is missed within the server ring.
- The UI **reacts to events**: `listModule` re-queries only when a relevant event type fires; the
  Dashboard/Logs update straight from the event stream. No timer polling where an event exists (Metrics
  offers a manual refresh + refresh-on-event, since it has no dedicated event).
- The **backend stays authoritative** — events only trigger a re-query or a local render.

## Modules (lazy-loaded, independent)

Dashboard · Projects · Jira · Assignments · Verification · Improvement · Policies · Resources ·
Leases · AI Runtimes · Accounts · Knowledge · Metrics · Logs · Settings.

Each is a separate ES module imported on first navigation (`import()` code-splitting without a
bundler). Eleven are built from the reusable `listModule` factory (query → table → event-driven
refresh, with graceful *not-yet-wired* and *empty* states); Dashboard, Metrics, Logs and Settings are
bespoke.

- **Dashboard** (landing page): live KPI tiles (assignments, verifications, leases, events), a live
  activity feed, current work, and aggregated system status — the whole system understood in seconds.
- **Logs**: live event log from the stream, free-text filter, newest first.
- **Metrics**: aggregated Control Plane metrics, grouped per provider.
- **Settings**: Control Plane base URL + token (saved locally), connection test, theme toggle, and API
  discovery (`/v1/queries`, `/v1/commands`).

## Design

Dark-mode first (light theme toggle), desktop-first with a responsive/tablet layout (collapsing
sidebar), a small token-based design system, status dots, KPI tiles, dense data tables, live feeds —
a professional engineering-platform feel rather than an admin dashboard.

## Clients

This console is one of the equal API clients (Web / Flutter desktop / Flutter mobile / CLI). It holds
no unique business logic; every feature it shows is available through the same Control Plane API.

## Validation

- **All 21 JS files pass `node --check`** (syntax-clean); 15 modules present.
- `go build ./...` and `go test ./...` remain green — the frontend is static assets, untouched by Go
  and not part of the module.
- Runtime: served statically (e.g. `python3 -m http.server`) and pointed at a Control Plane; until the
  engine's serve loop registers queries/events, modules render the friendly *not-yet-available* state.

## Deferrals (honest)

- **Production packaging** (bundling, minification, Docker, CDN) — intentionally not implemented.
- **Live data** depends on the future `cwv2 serve` loop registering Control Plane queries and
  publishing events; the console is ready and degrades gracefully until then.
- Charts/visualisation are currently tables + KPIs + feeds; richer graphs can be added as reusable
  components without touching the API contract.
