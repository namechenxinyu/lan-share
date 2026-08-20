#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"
GO120="${GO120:-go}"
mkdir -p dist

LDFLAGS='-s -w'
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 "$GO120" build -trimpath -ldflags="$LDFLAGS" -o dist/lan-share-win7-x64.exe ./cmd/lan-share
CGO_ENABLED=0 GOOS=windows GOARCH=386   "$GO120" build -trimpath -ldflags="$LDFLAGS" -o dist/lan-share-win7-x86.exe ./cmd/lan-share

echo "Win7 builds written to $ROOT/dist"
