#!/usr/bin/env python3
"""First human request titles, including an existing database with context titles."""
from test_support import settled_urlopen
import hashlib
import json
import os
import socket
import sqlite3
import subprocess
import tempfile
import time
import urllib.request
from pathlib import Path

from playwright.sync_api import expect, sync_playwright

BINARY = os.getenv("AGENTLOG_BIN", "./agentlog")
TOKEN = "synthetic-first-user-title-token"


def start(binary, database):
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    base = f"http://127.0.0.1:{port}"
    proc = subprocess.Popen([binary, "--listen", f"127.0.0.1:{port}", "--db", database],
                            env={**os.environ, "AGENTLOG_TOKEN": TOKEN},
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    for _ in range(300):
        try:
            settled_urlopen(base, timeout=1).close()
            return proc, base
        except OSError:
            if proc.poll() is not None:
                raise AssertionError("Server exited during startup")
            time.sleep(.05)
    proc.terminate()
    proc.wait(timeout=20)
    raise AssertionError("Server startup timed out")


def fingerprint(database):
    with sqlite3.connect(database) as connection:
        rows = connection.execute("SELECT id,hex(raw) FROM records ORDER BY id").fetchall()
    return hashlib.sha256(json.dumps(rows).encode()).hexdigest()


def message(session, order, text):
    attributes = {"session.id": session, "cwd": "/work/title-preview", "prompt": text}
    return {"eventName": "user_prompt", "timeUnixNano": str(1780000000000000000 + order * 1000000000),
            "attributes": [{"key": key, "value": {"stringValue": value}} for key, value in attributes.items()]}


with tempfile.TemporaryDirectory() as folder:
    database = folder + "/history.db"
    proc, base = start(BINARY, database)
    try:
        context = "<recommended_plugins>\n" + "Shared plugin context\n" * 1000 + "</recommended_plugins>"
        rows = [message("first", 0, context), message("first", 1, "一覧のタイトルを修正して\n追加の説明"),
                message("first", 4, "後からの依頼"), message("second", 0, context + "\n<environment_context>metadata</environment_context>\nログ検索を改善して"),
                message("second", 3, "こちらも後からの依頼")]
        payload = {"resourceLogs": [{"resource": {"attributes": [{"key": "service.name", "value": {"stringValue": "codex"}}]}, "scopeLogs": [{"logRecords": rows}]}]}
        req = urllib.request.Request(base + "/v1/logs", json.dumps(payload).encode(), headers={"Content-Type": "application/json", "Authorization": "Bearer " + TOKEN})
        settled_urlopen(req).close()
        original = fingerprint(database)
    finally:
        proc.terminate()
        proc.wait(timeout=20)
    proc, base = start(BINARY, database)
    try:
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path="/usr/bin/chromium", headless=True, args=["--no-sandbox"])
            page = browser.new_page(viewport={"width": 1440, "height": 1000})
            errors = []
            page.on("pageerror", lambda error: errors.append(str(error)))
            page.goto(base + "/?folder=%2Fwork%2Ftitle-preview")
            page.locator("#token").fill(TOKEN)
            page.locator("#loginform button").click()
            expect(page.locator(".session h2")).to_have_text(["一覧のタイトルを修正して", "ログ検索を改善して"])
            page.reload()
            expect(page.locator(".session h2")).to_have_text(["一覧のタイトルを修正して", "ログ検索を改善して"])
            if os.getenv("AGENTLOG_SCREENSHOT_DIR"):
                output = Path(os.environ["AGENTLOG_SCREENSHOT_DIR"])
                output.mkdir(parents=True, exist_ok=True)
                page.screenshot(path=str(output / "ui-session-titles.png"))
            page.locator(".session").first.click()
            expect(page.locator("#sessiontitle")).to_have_text("一覧のタイトルを修正して")
            page.locator(".event").first.click()
            page.locator("#jsontab").click()
            expect(page.locator("#eventjson")).to_contain_text("recommended_plugins")
            page.goto(base + "/?folder=%2Fwork%2Ftitle-preview")
            page.set_viewport_size({"width": 390, "height": 844})
            expect(page.locator(".session h2")).to_have_text(["一覧のタイトルを修正して", "ログ検索を改善して"])
            assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
            assert not errors, errors
            browser.close()
        assert fingerprint(database) == original, "Title update altered original log records"
    finally:
        proc.terminate()
        proc.wait(timeout=20)
print("PASS: existing/context-prefixed sessions show distinct first user requests; later requests, reload, detail title, original JSON and mobile verified")
