#!/usr/bin/env bash
set -euo pipefail
version="${1:?Usage: scripts/release.sh v0.1.0}"
mkdir -p dist
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  platform="${target%/*}"; arch="${target#*/}"; name="agentlog_${version}_${platform}_${arch}"
  stage="$(mktemp -d)"; binary=agentlog
  if [ "$platform" = windows ]; then binary=agentlog.exe; fi
  CGO_ENABLED=0 GOOS="$platform" GOARCH="$arch" go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$stage/$binary" ./cmd/agentlog
  cp README.md LICENSE THIRD_PARTY_NOTICES.txt "$stage/"
  cp -r docs "$stage/"
  if [ "$platform" = windows ]; then (cd "$stage" && zip -qr "$OLDPWD/dist/$name.zip" "$binary" README.md LICENSE THIRD_PARTY_NOTICES.txt docs); else tar -czf "dist/$name.tar.gz" -C "$stage" "$binary" README.md LICENSE THIRD_PARTY_NOTICES.txt docs; fi
  rm -rf "$stage"
done
(cd dist && sha256sum *.tar.gz *.zip > checksums.txt)
