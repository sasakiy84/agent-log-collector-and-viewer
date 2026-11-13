#!/usr/bin/env bash
set -euo pipefail
version="${1:?Usage: scripts/release.sh vX.Y.Z}"
mkdir -p dist
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  platform="${target%/*}"; arch="${target#*/}"; name="agentlog_${version}_${platform}_${arch}"
  stage="$(mktemp -d)"; binary=agentlog
  if [ "$platform" = windows ]; then binary=agentlog.exe; fi
  CGO_ENABLED=0 GOOS="$platform" GOARCH="$arch" go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$stage/$binary" ./cmd/agentlog
  cp README.md LICENSE THIRD_PARTY_NOTICES.txt Dockerfile .dockerignore compose.yaml compose.https.yaml Caddyfile .env.example go.mod go.sum "$stage/"
  cp -r docs cmd internal scripts "$stage/"
  if [ "$platform" = windows ]; then (cd "$stage" && zip -qr "$OLDPWD/dist/$name.zip" "$binary" README.md LICENSE THIRD_PARTY_NOTICES.txt Dockerfile .dockerignore compose.yaml compose.https.yaml Caddyfile .env.example go.mod go.sum docs cmd internal scripts); else tar -czf "dist/$name.tar.gz" -C "$stage" "$binary" README.md LICENSE THIRD_PARTY_NOTICES.txt Dockerfile .dockerignore compose.yaml compose.https.yaml Caddyfile .env.example go.mod go.sum docs cmd internal scripts; fi
  rm -rf "$stage"
done
(cd dist && sha256sum agentlog_"$version"_*.tar.gz agentlog_"$version"_*.zip > checksums.txt)
