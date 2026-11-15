#!/usr/bin/env bash
set -euo pipefail
test -z "$(gofmt -l cmd internal)"
for script in internal/app/static/*.js; do
  node --check "$script"
done
