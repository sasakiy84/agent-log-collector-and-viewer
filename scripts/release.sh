#!/usr/bin/env bash
set -euo pipefail
version="${1:?Usage: scripts/release.sh vX.Y.Z}"
source_version="$(sed -n 's/^const version = "\([^"]*\)"$/\1/p' cmd/agentlog/version.go)"
if [[ "$version" != "$source_version" ]]; then
  printf 'Release tag %s must match the version in cmd/agentlog/version.go (%s).\n' "$version" "$source_version" >&2
  exit 1
fi
mkdir -p dist
stage=''
trap 'if [ -n "$stage" ]; then rm -rf "$stage"; fi' EXIT
rm -f dist/agentlog_"$version"_*.tar.gz dist/agentlog_"$version"_*.zip dist/checksums.txt
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  platform="${target%/*}"; arch="${target#*/}"; name="agentlog_${version}_${platform}_${arch}"
  stage="$(mktemp -d)"; binary=agentlog
  if [ "$platform" = windows ]; then binary=agentlog.exe; fi
  CGO_ENABLED=0 GOOS="$platform" GOARCH="$arch" go build -trimpath -ldflags "-s -w" -o "$stage/$binary" ./cmd/agentlog
  cp README.md LICENSE THIRD_PARTY_NOTICES.txt Dockerfile .dockerignore compose.yaml .env.example go.mod go.sum "$stage/"
  cp -r docs cmd internal scripts "$stage/"
  if [ "$platform" = windows ]; then (cd "$stage" && zip -qr "$OLDPWD/dist/$name.zip" "$binary" README.md LICENSE THIRD_PARTY_NOTICES.txt Dockerfile .dockerignore compose.yaml .env.example go.mod go.sum docs cmd internal scripts); else tar -czf "dist/$name.tar.gz" -C "$stage" "$binary" README.md LICENSE THIRD_PARTY_NOTICES.txt Dockerfile .dockerignore compose.yaml .env.example go.mod go.sum docs cmd internal scripts; fi
  rm -rf "$stage"
  stage=''
done
(cd dist && sha256sum agentlog_"$version"_*.tar.gz agentlog_"$version"_*.zip > checksums.txt)
