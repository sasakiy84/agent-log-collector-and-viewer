#!/usr/bin/env bash
set -euo pipefail
if [ -n "${AGENTLOG_BASELINE_BIN:-}" ]; then
  exec python3 scripts/database_upgrade_test.py
fi
baseline_dir="$(mktemp -d)"
trap 'rm -rf "$baseline_dir"' EXIT
archive=agentlog_v0.9.0_linux_amd64.tar.gz
gh release download v0.9.0 --repo sasakiy84/agent-log-collector-and-viewer --pattern "$archive" --dir "$baseline_dir"
printf '%s  %s\n' cb3672fc77296562c348a787a4cca8b904efa9184b826a470b1b3fe785faa17c "$baseline_dir/$archive" | sha256sum --check --status
tar -xzf "$baseline_dir/$archive" -C "$baseline_dir" agentlog
AGENTLOG_BASELINE_BIN="$baseline_dir/agentlog" python3 scripts/database_upgrade_test.py
