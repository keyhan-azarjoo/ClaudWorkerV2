#!/usr/bin/env bash
# ClaudWorker V2 installer — builds the binary, installs it, and (optionally) the service unit.
set -euo pipefail
PREFIX="${PREFIX:-/usr/local}"
echo "building cwv2…"; CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o cwv2 ./cmd/cwv2
install -d "$PREFIX/bin" "$PREFIX/share/cwv2"
install -m755 cwv2 "$PREFIX/bin/cwv2"
cp -R web/ops-console "$PREFIX/share/cwv2/"
echo "installed cwv2 to $PREFIX/bin/cwv2"
echo "next: create your config, run 'cwv2 validate --config <cfg>', then install deploy/systemd or deploy/launchd."
