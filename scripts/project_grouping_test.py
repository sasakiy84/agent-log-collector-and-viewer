#!/usr/bin/env python3
"""Merge a real old DB's 41 named + 6 directory sessions, then test the UI."""
import hashlib
import json
import os
import socket
import sqlite3
import subprocess
import tempfile
import time
import urllib.parse
import urllib.request
import zipfile

from playwright.sync_api import sync_playwright, expect
from test_support import settled_urlopen, wait_for_ingestion

TOKEN = 'synthetic-project-name-grouping-token'
BINARY = os.getenv('AGENTLOG_BIN', './agentlog')
BASELINE = os.getenv('AGENTLOG_BASELINE_BIN')


def attrs(values):
    return [{'key': key, 'value': {'stringValue': value}} for key, value in values.items()]


with tempfile.TemporaryDirectory() as directory:
    database = directory + '/test.db'
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    base = f'http://127.0.0.1:{port}'

    def start(binary):
        process = subprocess.Popen([binary, '--db', database, '--listen', f'127.0.0.1:{port}'],
            env={**os.environ, 'AGENTLOG_TOKEN': TOKEN}, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        for _ in range(1000):
            try:
                urllib.request.urlopen(base, timeout=1).close()
                wait_for_ingestion(base, TOKEN)
                return process
            except OSError:
                assert process.poll() is None, 'Server exited'
                time.sleep(.02)
        raise RuntimeError('Server did not start')

    def api(path, body=None):
        req = urllib.request.Request(base + path, json.dumps(body).encode() if body is not None else None,
            headers={'Authorization': 'Bearer ' + TOKEN, 'Content-Type': 'application/json'})
        with settled_urlopen(req, timeout=30) as response:
            return json.load(response)

    def fingerprint():
        digest = hashlib.sha256()
        with sqlite3.connect('file:' + database + '?mode=ro', uri=True) as db:
            for row in db.execute('SELECT id,session,raw FROM records ORDER BY id'):
                for value in row:
                    digest.update(value if isinstance(value, bytes) else value.encode())
        return digest.hexdigest()

    old = start(BASELINE or BINARY)
    try:
        resources = []
        for kind, count, metadata in [('named', 41, {'service.namespace': 'shop'}), ('directory', 6, {'cwd': '/workspaces/shop'})]:
            records = []
            for i in range(count):
                records.append({'eventName': 'codex.user_prompt', 'timeUnixNano': str(1794744249000000000 + i),
                    'attributes': attrs({'session.id': f'{kind}-{i}', 'prompt': 'hello'})})
            records.append({'eventName': 'codex.skill_invocation', 'timeUnixNano': '1794744249000000100',
                'attributes': attrs({'session.id': kind + '-0', 'skill.name': 'review', 'turn.id': 'one'})})
            resources.append({'resource': {'attributes': attrs({'service.name': 'codex', **metadata})},
                              'scopeLogs': [{'logRecords': records}]})
        api('/v1/logs', {'resourceLogs': resources})
        if BASELINE:
            assert api('/api/projects')['total'] == 2
        original = fingerprint()
    finally:
        old.terminate()
        old.wait(timeout=20)
    current = start(BINARY)
    try:
        groups = api('/api/projects')
        assert groups['total'] == 1 and groups['sessions'] == 47
        assert groups['projects'][0]['id'] == 'name:shop' and groups['projects'][0]['count'] == 47
        assert len(api('/api/sessions?project=name:shop')) == 47
        assert len(api('/api/sessions?project=path:/workspaces/shop')) == 47
        assert api('/api/skills?project=name:shop')['uses'] == 2
        assert api('/api/skills?project=path:/workspaces/shop&q=review')['uses'] == 2
        assert fingerprint() == original
        with sqlite3.connect('file:' + database + '?mode=ro', uri=True) as db:
            ledger = db.execute('SELECT version,checksum,applied_at FROM schema_migrations ORDER BY version').fetchall()
            assert db.execute('PRAGMA foreign_key_check').fetchall() == []
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path='/usr/bin/chromium', headless=True, args=['--no-sandbox'])
            page = browser.new_page(viewport={'width': 1440, 'height': 900})
            errors = []
            page.on('pageerror', lambda e: errors.append(str(e)))
            page.goto(base + '/?' + urllib.parse.urlencode({'project': 'path:/workspaces/shop'}))
            page.locator('#token').fill(TOKEN)
            page.locator('#loginform button').click()
            expect(page.locator('.session')).to_have_count(47)
            expect(page.locator('#projectlist button')).to_have_count(1)
            expect(page.locator('#projectlist button small')).to_have_text('47')
            expect(page.locator('[data-project="name:shop"]')).to_have_attribute('aria-current', 'true')
            assert urllib.parse.parse_qs(urllib.parse.urlsplit(page.url).query)['project'] == ['name:shop']
            page.locator('#dataoptions summary').click()
            expect(page.locator('.session')).to_have_count(47)
            with page.expect_download() as pending:
                page.locator('#projectexport').click()
            with zipfile.ZipFile(pending.value.path()) as archive:
                manifest = json.loads(archive.read('manifest.json'))
                events = archive.read('events.jsonl').splitlines()
            assert manifest['sessions'] == 47 and manifest['records'] == len(events) == 49
            assert manifest['filters']['project'] == 'name:shop'
            if os.getenv('AGENTLOG_SCREENSHOT'):
                page.screenshot(path=os.environ['AGENTLOG_SCREENSHOT'], full_page=True)
            page.set_viewport_size({'width': 390, 'height': 844})
            assert page.evaluate('document.documentElement.scrollWidth <= innerWidth')
            assert not errors, errors
            browser.close()
    finally:
        current.terminate()
        current.wait(timeout=20)
    restarted = start(BINARY)
    try:
        assert api('/api/projects')['projects'][0]['count'] == 47
        assert fingerprint() == original
        with sqlite3.connect('file:' + database + '?mode=ro', uri=True) as db:
            assert db.execute('SELECT version,checksum,applied_at FROM schema_migrations ORDER BY version').fetchall() == ledger
    finally:
        restarted.terminate()
        restarted.wait(timeout=20)
print('PASS: 41 named + 6 directory sessions merge into one project; old DB/URLs, skills, ZIP export, original records, mobile and restart verified')
