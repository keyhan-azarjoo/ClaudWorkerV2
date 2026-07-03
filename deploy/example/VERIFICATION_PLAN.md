# Example — Verification Plan (per repo)

Concrete build/verify commands, reconstructed from the actual repositories (not guessed). Pass them to
`cwv2 serve` via `--build-cmd`, `--api-url`, `--web-url` (one repo piloted at a time).

| Repo | Remote | Build (`--build-cmd`) | Verify | API/Web target |
|---|---|---|---|---|
| **backend (.NET)** — PILOT | `keyhan-azarjoo/DotNet-IoT-MainWebApi` (`WebApi.sln`) | `dotnet build` | `dotnet test` | `--api-url https://api.example.com/health` |
| mobile-app (Flutter) | `keyhan-azarjoo/Flutter-IoT-MobileApp` (`example/pubspec.yaml`) | `flutter build apk --debug` | `flutter analyze` + `flutter test` | — |
| website (Next.js) | `keyhan-azarjoo/NextJs-Myotgo-Website` (scripts: build/lint/test) | `npm ci && npm run build` | `npm run lint` + `npm test` | `--web-url https://example.com` |

## Pilot launch (backend)

```sh
cwv2 serve --config deploy/example/cwv2.yaml --mode live \
  --build-cmd "dotnet build" --api-url https://api.example.com/health --bind 127.0.0.1:8080
```

Notes:
- `serve` applies `--build-cmd`/`--api-url`/`--web-url` to the **first** repo in `repos[]`. Pilot one
  repo at a time; switch repos + flags per pilot. (Per-repo verification config is a documented
  future ACP; the CLI flags are sufficient for staged piloting.)
- Toolchains required on the runner: **.NET SDK** (backend), **Flutter SDK** (mobile), **Node ≥20 +
  npm** (website). Verify with `cwv2 doctor`.
- Verification outcomes are Pass/Fail/Blocked/Inconclusive; a missing toolchain surfaces as
  Blocked/Inconclusive (never a false Fail).
