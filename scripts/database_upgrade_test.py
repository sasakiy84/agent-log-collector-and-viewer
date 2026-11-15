#!/usr/bin/env python3
"""Compare a released v0.9.0 database/API with an upgraded and restarted binary.

Synthetic records only. Set AGENTLOG_BASELINE_BIN and AGENTLOG_BIN.
"""
import hashlib
import json
import os
from pathlib import Path
import socket
import sqlite3
import subprocess
import tempfile
import time
import urllib.parse
import urllib.request

TOKEN = "synthetic-database-migration-token"
BASELINE = os.environ["AGENTLOG_BASELINE_BIN"]
CANDIDATE = os.getenv("AGENTLOG_BIN", "./agentlog")


def start(binary, database):
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    base = f"http://127.0.0.1:{port}"
    process = subprocess.Popen([binary, "--listen", f"127.0.0.1:{port}", "--db", database],
                               env={**os.environ, "AGENTLOG_TOKEN": TOKEN},
                               stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        try:
            call(base, "/api/stats")
            return process, base
        except OSError:
            if process.poll() is not None:
                raise AssertionError("Server exited during database preparation")
            time.sleep(.05)
    process.terminate()
    process.wait(timeout=20)
    raise AssertionError("Server startup timed out")


def call(base, path, data=None, binary=False):
    body = None if data is None else json.dumps(data).encode()
    request = urllib.request.Request(base + path, body, headers={
        "Authorization": "Bearer " + TOKEN, "Content-Type": "application/json"})
    with urllib.request.urlopen(request, timeout=30) as response:
        return response.read() if binary else json.load(response)


def fingerprint(database):
    digest = hashlib.sha256()
    with sqlite3.connect("file:" + database + "?mode=ro", uri=True) as connection:
        for row in connection.execute("SELECT id,session,raw FROM events ORDER BY id"):
            for item in row:
                digest.update(item if isinstance(item, bytes) else item.encode())
    return digest.hexdigest()


def snapshot(base):
    result = {path: call(base, path) for path in (
        "/api/stats", "/api/folders", "/api/sessions?diagnostics=1", "/api/skills")}
    for session in result["/api/sessions?diagnostics=1"]:
        key = urllib.parse.quote(session["id"], safe="")
        page = call(base, "/api/session?id=" + key)
        result["session:" + key] = page
        for event in page["events"]:
            result["event:" + event["id"]] = call(base, "/api/event?session=" + key + "&id=" + event["id"])
    return result


def presentation_normalized(value):
    # New optional presentation metadata is derived from existing attributes;
    # compare all established fields and original record payloads unchanged.
    if isinstance(value, list):
        return [presentation_normalized(item) for item in value]
    if isinstance(value, dict):
        return {key: presentation_normalized(item) for key, item in value.items()
                if not (key in ('diagnostic', 'turn') and 'kind' in value and 'blocks' in value)}
    return value


def records(source):
    def record(i, name, values):
        attributes = {"session.id": "upgrade-" + source, "cwd": "/work/移行テスト", **values}
        return {"eventName": name, "timeUnixNano": str(1735689600000000000 + i * 1000000000),
                "attributes": [{"key": key, "value": {"stringValue": value}} for key, value in attributes.items()]}
    rows = [record(0, "gen_ai.user.message", {"prompt": "日本語の検索と移行"}),
            record(1, "gen_ai.tool.start", {"tool_name": "exec_command", "call_id": "read", "arguments": '{"cmd":"cat /work/skills/review/SKILL.md"}'}),
            record(2, "gen_ai.tool.result", {"tool_name": "exec_command", "call_id": "read", "output": "Synthetic instructions"}),
            record(3, "gen_ai.client.operation", {"gen_ai.operation.name": "chat", "gen_ai.usage.input_tokens": "123", "gen_ai.usage.output_tokens": "45"})]
    if source == "codex":
        rows.append(record(4, "codex.skill_invocation", {"skill.name": "review", "turn.id": "one", "skill.scope": "repo"}))
    return {"resourceLogs": [{"resource": {"attributes": [{"key": "service.name", "value": {"stringValue": source}}]},
                              "scopeLogs": [{"logRecords": rows}]}]}


with tempfile.TemporaryDirectory() as folder:
    database = folder + "/history.db"
    process, base = start(BASELINE, database)
    try:
        for source in ("codex", "claude", "copilot"):
            call(base, "/v1/logs", records(source))
        # Preserve archive deletion/suppression metadata through the migration.
        archived = records("other")
        call(base, "/v1/logs", archived)
        job = call(base, "/api/archives", {"sessions": ["other:upgrade-other"], "cutoff": "2026-01-01T00:00:00Z"})
        def wait(status):
            for _ in range(400):
                current = call(base, "/api/archives/" + job["id"])
                if current["status"] == status:
                    return current
                assert current["status"] != "failed", current
                time.sleep(.025)
            raise AssertionError("Archive did not finish")
        ready = wait("ready")
        payload = call(base, "/api/archives/" + job["id"] + "/download", binary=True)
        assert hashlib.sha256(payload).hexdigest() == ready["sha256"]
        call(base, "/api/archives/" + job["id"] + "/delete", {"confirmation": "DOWNLOADED"})
        wait("deleted")
        before, original = snapshot(base), fingerprint(database)
    finally:
        process.terminate()
        process.wait(timeout=20)
    checkpoints = None
    for stage in ("migration", "restart"):
        process, base = start(CANDIDATE, database)
        try:
            after = snapshot(base)
            assert presentation_normalized(after) == presentation_normalized(before), "API changed after " + stage + ": " + str([key for key in before if before[key] != after.get(key)])
            assert fingerprint(database) == original, "Original records changed"
            call(base, "/v1/logs", archived)
            assert call(base, "/api/stats") == before["/api/stats"], "Archived replay was reimported"
            with sqlite3.connect("file:" + database + "?mode=ro", uri=True) as connection:
                assert connection.execute("PRAGMA foreign_key_check").fetchall() == []
                current = connection.execute("SELECT version,name,checksum,applied_at FROM schema_migrations ORDER BY version").fetchall()
                assert len(current) == len(list((Path(__file__).resolve().parents[1] / 'internal/app/migrations').glob('*.sql')))
                current += connection.execute("SELECT name,cursor,complete,processed,updated_at FROM maintenance_jobs ORDER BY name").fetchall()
                if checkpoints is not None:
                    assert current == checkpoints, "Completed migrations/jobs ran again"
                checkpoints = current
        finally:
            process.terminate()
            process.wait(timeout=20)
print("PASS: v0.9.0 API equality, original payloads/identities, archive replay suppression, foreign keys, and no repeat migration/job on restart")
