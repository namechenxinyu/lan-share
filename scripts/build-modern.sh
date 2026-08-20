#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"
mkdir -p dist

LDFLAGS='-s -w'
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="$LDFLAGS" -o dist/lan-share-win11-x64.exe ./cmd/lan-share
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags="$LDFLAGS" -o dist/lan-share-deepin-linux-amd64 ./cmd/lan-share
CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -trimpath -ldflags="$LDFLAGS" -o dist/lan-share-linux-arm64 ./cmd/lan-share
CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -trimpath -ldflags="$LDFLAGS" -o dist/lan-share-macos-intel ./cmd/lan-share
CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags="$LDFLAGS" -o dist/lan-share-macos-apple-silicon ./cmd/lan-share

echo "Modern builds written to $ROOT/dist"
