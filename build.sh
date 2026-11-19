#!/bin/sh
# Cross-compiles Islet.exe for 64-bit Windows from Linux or macOS. Needs Go 1.24+.
set -e
cd "$(dirname "$0")"
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-H windowsgui -s -w" -o Islet.exe .
echo "Built Islet.exe"
