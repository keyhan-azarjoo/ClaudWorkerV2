#!/usr/bin/env bash
# ClaudWorker V1 → V2 production cutover. Safe, ordered, non-destructive.
#   1 backup V1  2 verify V2  3 import V1 config  4 start V2  5 observe  6 archive V1 read-only (never delete)
#
# Usage: deploy/cutover.sh <V1 dir> <V2 cwv2.yaml> [--go]
#   Without --go it is a DRY RUN (prints the plan, backs up + imports, does NOT start V2 or archive V1).
set -euo pipefail
V1="${1:?usage: cutover.sh <V1 dir> <V2 cwv2.yaml> [--go]}"
CFG="${2:?need V2 cwv2.yaml}"
GO="${3:-}"
CWV2="${CWV2:-cwv2}"
STAMP="$(date +%Y%m%d-%H%M%S 2>/dev/null || echo now)"
OUT="cutover-${STAMP}"
mkdir -p "$OUT"

echo "== 1. Backup V1 (read-only copy; V1 is never modified) =="
tar -czf "$OUT/v1-backup.tgz" -C "$(dirname "$V1")" "$(basename "$V1")"
echo "   → $OUT/v1-backup.tgz"

echo "== 2. Verify V2 =="
"$CWV2" validate --config "$CFG"
echo "   → V2 config valid"

echo "== 3. Import V1 configuration (read-only against V1) =="
"$CWV2" migrate --from "$V1" --to "$OUT/imported"
echo "   → $OUT/imported/{resources.json,migrated.yaml,migration-matrix.md}"
echo "   Review migrated.yaml + resources.json and merge into your V2 config before going live."

if [ "$GO" != "--go" ]; then
  echo "== DRY RUN complete. Re-run with --go to start V2 + archive V1. =="
  exit 0
fi

echo "== 4. Start V2 (live) =="
"$CWV2" serve --config "$CFG" --mode live --bind :8080 &
echo "   → V2 serving (pid $!). Observe the Operations Console + /v1/status."

echo "== 5. Observe =="
sleep 5
curl -fsS http://localhost:8080/v1/status | head -c 300; echo
echo "   Watch for healthy autonomous processing before archiving V1."

echo "== 6. Archive V1 read-only (NEVER delete) =="
read -r -p "V2 healthy? Archive V1 as read-only? [y/N] " ans
if [ "${ans:-N}" = "y" ]; then
  chmod -R a-w "$V1" 2>/dev/null || true
  echo "   → V1 set read-only at $V1 (retained, not deleted)."
else
  echo "   → V1 left writable; archive later once confident."
fi
