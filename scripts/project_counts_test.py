#!/usr/bin/env python3
"""Visible sidebar counts, including upgrade from the old 41-vs-2 mismatch."""
import hashlib
import json
import os
import socket
import sqlite3
import subprocess
import tempfile
import time
import urllib.request
import zipfile

from playwright.sync_api import sync_playwright, expect
from test_support import settled_urlopen, wait_for_ingestion

TOKEN = 'synthetic-visible-project-count-token'
BINARY = os.getenv('AGENTLOG_BIN', './agentlog')
BASELINE = os.getenv('AGENTLOG_BASELINE_BIN')


def attrs(values):
    return [{'key': key, 'value': {'boolValue': value} if isinstance(value, bool) else {'stringValue': value}}
            for key, value in values.items()]


with tempfile.TemporaryDirectory() as root:
    database = root + '/test.db'
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    base = f'http://127.0.0.1:{port}'

    def start(binary):
        p = subprocess.Popen([binary, '--db', database, '--listen', f'127.0.0.1:{port}'],
            env={**os.environ, 'AGENTLOG_TOKEN': TOKEN}, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        for _ in range(1000):
            try:
                urllib.request.urlopen(base, timeout=1).close()
                wait_for_ingestion(base, TOKEN)
                return p
            except OSError:
                assert p.poll() is None, 'Server exited'
                time.sleep(.02)
        raise RuntimeError('Server did not start')

    def api(path, body=None):
        request = urllib.request.Request(base + path, json.dumps(body).encode() if body is not None else None,
            headers={'Authorization': 'Bearer ' + TOKEN, 'Content-Type': 'application/json'})
        with settled_urlopen(request, timeout=30) as response:
            return json.load(response)

    def fingerprint():
        h = hashlib.sha256()
        with sqlite3.connect('file:' + database + '?mode=ro', uri=True) as db:
            for row in db.execute('SELECT id,session,raw FROM records ORDER BY id'):
                for value in row:
                    h.update(value if isinstance(value, bytes) else value.encode())
        return h.hexdigest()

    old = start(BASELINE or BINARY)
    try:
        records = []
        for i in range(41):
            values = {'session.id': f'root-{i}' if i < 2 else f'child-{i}', 'prompt': 'hello'}
            if i >= 2:
                values['parent.session.id'] = 'root-0'
            records.append({'eventName': 'codex.user_prompt', 'timeUnixNano': str(1794744249000000000 + i), 'attributes': attrs(values)})
        records.append({'eventName': 'codex.user_prompt', 'timeUnixNano': '1794744250000000000',
            'attributes': attrs({'session.id': 'internal', 'prompt': 'generated title',
                                 'session_init.ephemeral': True, 'rpc.request_id': 'temporary-structured-test'})})
        api('/v1/logs', {'resourceLogs': [{'resource': {'attributes': attrs({'service.name': 'codex', 'service.namespace': 'shop'})},
            'scopeLogs': [{'logRecords': records}]}]})
        assert len(api('/api/sessions?project=name:shop')) == 2
        if BASELINE:
            assert api('/api/projects')['projects'][0]['count'] == 41
        original = fingerprint()
    finally:
        old.terminate()
        old.wait(timeout=20)
    current = start(BINARY)
    try:
        projects = api('/api/projects')
        sessions = api('/api/sessions?project=name:shop')
        assert projects['sessions'] == projects['projects'][0]['count'] == len(sessions) == 2
        assert projects['projects'][0]['end'] == max(s['end'] for s in sessions)
        assert api('/api/projects?diagnostics=1')['sessions'] == 3
        assert api('/api/projects?include_children=1&diagnostics=1')['sessions'] == 42
        assert fingerprint() == original
        with sqlite3.connect('file:' + database + '?mode=ro', uri=True) as db:
            ledger = db.execute('SELECT version,checksum,applied_at FROM schema_migrations ORDER BY version').fetchall()
            assert db.execute('PRAGMA foreign_key_check').fetchall() == []
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path='/usr/bin/chromium', headless=True, args=['--no-sandbox'])
            page = browser.new_page()
            page.goto(base + '/?project=name:shop')
            page.locator('#token').fill(TOKEN)
            page.locator('#loginform button').click()
            expect(page.locator('.session')).to_have_count(2)
            expect(page.locator('[data-project="name:shop"] small')).to_have_text('2')
            page.locator('#showdiagnostics').check()
            expect(page.locator('.session')).to_have_count(3)
            expect(page.locator('[data-project="name:shop"] small')).to_have_text('3')
            page.locator('#showdiagnostics').uncheck()
            expect(page.locator('.session')).to_have_count(2)
            expect(page.locator('[data-project="name:shop"] small')).to_have_text('2')
            page.locator('#dataoptions summary').click()
            expect(page.locator('.session')).to_have_count(2)
            expect(page.locator('[data-project="name:shop"] small')).to_have_text('2')
            with page.expect_download() as pending:
                page.locator('#projectexport').click()
            with zipfile.ZipFile(pending.value.path()) as archive:
                manifest = json.loads(archive.read('manifest.json'))
                assert manifest['sessions'] == manifest['records'] == len(archive.read('events.jsonl').splitlines()) == 42
            browser.close()
    finally:
        current.terminate()
        current.wait(timeout=20)
    restarted = start(BINARY)
    try:
        assert api('/api/projects')['sessions'] == 2
        assert fingerprint() == original
        with sqlite3.connect('file:' + database + '?mode=ro', uri=True) as db:
            assert db.execute('SELECT version,checksum,applied_at FROM schema_migrations ORDER BY version').fetchall() == ledger
    finally:
        restarted.terminate()
        restarted.wait(timeout=20)
print('PASS: old 41-vs-2 mismatch repaired, internal/child exclusion, visible recency, browser toggles, full export, original data and migration restart verified')
