#!/usr/bin/env bash
# ClaudWorker V2 — automated LIVE acceptance. One command runs the complete validation:
#   validate → doctor → start (live) → discover → claim → develop → verify → improve → merge →
#   update Jira → release → observe. Exits non-zero if the loop never completes an assignment.
#
# Usage: deploy/live-acceptance.sh <cwv2.yaml> [--build-cmd "..."] [--api-url URL] [--web-url URL] [--bind :8080]
# Requires: credentials in place (see docs/guides/LIVE_CONFIG_CHECKLIST.md). Safe to re-run.
set -euo pipefail
CFG="${1:?usage: live-acceptance.sh <cwv2.yaml> [flags...]}"; shift || true
BIND=":8088"; EXTRA=()
while [ $# -gt 0 ]; do case "$1" in --bind) BIND="$2"; shift 2;; *) EXTRA+=("$1"); shift;; esac; done
CWV2="${CWV2:-cwv2}"

echo "[1/6] validate config…";     "$CWV2" validate --config "$CFG"
echo "[2/6] doctor (env/secrets)…"; "$CWV2" doctor   --config "$CFG" || true

echo "[3/6] start (live)…"
"$CWV2" serve --config "$CFG" --mode live --bind "$BIND" "${EXTRA[@]}" &
SRV=$!; trap 'kill $SRV 2>/dev/null || true' EXIT
# wait for health
for i in $(seq 1 30); do curl -fsS "http://localhost${BIND}/v1/healthz" >/dev/null 2>&1 && break; sleep 1; done

echo "[4/6] observe discovery + status…"
curl -fsS "http://localhost${BIND}/v1/query/resources.snapshot" | head -c 400; echo
curl -fsS "http://localhost${BIND}/v1/status" | head -c 300; echo

echo "[5/6] drive the loop until an assignment completes (max 20 ticks)…"
DONE=0
for i in $(seq 1 20); do
  curl -fsS -X POST "http://localhost${BIND}/v1/command/orchestrator.tick" >/dev/null 2>&1 || true
  if curl -fsS "http://localhost${BIND}/v1/query/assignments.list" 2>/dev/null | grep -q '"state":"done"'; then DONE=1; break; fi
  sleep 3
done

echo "[6/6] result…"
curl -fsS "http://localhost${BIND}/v1/query/assignments.list" | head -c 600; echo
curl -fsS "http://localhost${BIND}/v1/metrics" | head -c 400; echo
if [ "$DONE" = 1 ]; then echo "ACCEPTANCE: PASS — an assignment reached done (claim→…→merge→Jira Done)"; exit 0; fi
echo "ACCEPTANCE: INCOMPLETE — no assignment completed. Check the queue (work_jql), accounts, and logs."; exit 1
