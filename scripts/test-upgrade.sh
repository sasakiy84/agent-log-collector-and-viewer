#!/usr/bin/env bash
set -euo pipefail
uv run scripts/migrate_db_test.py
if [ -n "${AGENTLOG_BASELINE_BIN:-}" ]; then
  uv run --python '>=3.14' scripts/storage_migration_api_test.py \
    --baseline "$AGENTLOG_BASELINE_BIN" --binary "${AGENTLOG_BIN:-./agentlog}"
fi
