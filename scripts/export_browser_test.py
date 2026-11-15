#!/usr/bin/env python3
"""Project-scoped search and native ZIP downloads, using synthetic data only."""
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.parse
import urllib.request
import zipfile

from playwright.sync_api import sync_playwright, expect
from test_support import settled_urlopen

BINARY = os.getenv('AGENTLOG_BIN', './agentlog')
TOKEN = 'synthetic-filtered-export-browser-token'


def attributes(values):
    return [{'key': key, 'value': {'boolValue': value} if isinstance(value, bool) else {'stringValue': value}}
            for key, value in values.items()]


def resource(project, source, records):
    return {'resource': {'attributes': attributes({'service.name': source, 'service.namespace': project})},
            'scopeLogs': [{'logRecords': [{'eventName': name, 'timeUnixNano': str(1794744249000000000 + i),
                'attributes': attributes({'session.id': session, **values})}
                for i, (session, name, values) in enumerate(records)]}]}


def downloaded_zip(page, sessions, records):
    with page.expect_download() as pending:
        page.locator('#downloadexport').click()
    download = pending.value
    assert download.suggested_filename == 'agentlog-export.zip'
    with zipfile.ZipFile(download.path()) as archive:
        manifest = json.loads(archive.read('manifest.json'))
        events = [json.loads(row) for row in archive.read('events.jsonl').splitlines()]
    assert manifest['sessions'] == sessions and manifest['records'] == records and len(events) == records
    return manifest, events


with tempfile.TemporaryDirectory() as directory:
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    base = f'http://127.0.0.1:{port}'
    server = subprocess.Popen([BINARY, '--db', directory + '/test.db', '--listen', f'127.0.0.1:{port}'],
        env={**os.environ, 'AGENTLOG_TOKEN': TOKEN}, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        for _ in range(500):
            try:
                urllib.request.urlopen(base, timeout=1).close()
                break
            except OSError:
                time.sleep(.02)
        fixture = {'resourceLogs': [
            resource('決済', 'codex', [
                ('root', 'user_prompt', {'prompt': '認証を修正'}),
                ('root', 'gen_ai.assistant.message', {'response': 'completed response'}),
                ('child', 'user_prompt', {'prompt': 'review', 'parent.session.id': 'root'}),
                ('auto', 'user_prompt', {'prompt': 'generated title', 'session_init.ephemeral': True,
                                       'rpc.request_id': 'temporary-structured-test'}),
            ]),
            resource('決済', 'claude', [('claude', 'user_prompt', {'prompt': 'ユーザー設定'})]),
            resource('Other', 'codex', [('other', 'user_prompt', {'prompt': '認証を修正'})]),
            resource('Large', 'codex', [(f'large-{i:03d}', 'user_prompt', {'prompt': f'hello {i}'}) for i in range(205)]),
        ]}
        request = urllib.request.Request(base + '/v1/logs', json.dumps(fixture).encode(),
            headers={'Authorization': 'Bearer ' + TOKEN, 'Content-Type': 'application/json'})
        with settled_urlopen(request) as response:
            assert response.status == 200
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path='/usr/bin/chromium', headless=True, args=['--no-sandbox'])
            page = browser.new_page(viewport={'width': 1440, 'height': 1000})
            errors = []
            page.on('pageerror', lambda error: errors.append(str(error)))
            page.goto(base)
            page.locator('#token').fill(TOKEN)
            page.locator('#loginform button').click()
            assert page.locator('.topbar nav a').all_text_contents() == ['Sessions', 'Skills']
            assert page.locator('#exportcontrols,#exportmatches,#archivepage').count() == 0
            requests=[]
            page.on('request',lambda r:requests.append(r.url))
            page.locator('[data-project="name:決済"]').click()
            expect(page.locator('.session')).to_have_count(2)
            assert page.locator('#sessionpagination button:visible,#sessionpagination input:visible').count()==0
            page.locator('#search').fill('未登録')
            expect(page.locator('.session')).to_have_count(0)
            expect(page.locator('#downloadexport')).to_be_disabled()
            # Entire-project export includes children/internal records, without
            # replacing the current search or navigating to another page.
            page.locator('#dataoptions summary').click()
            with page.expect_download() as pending:page.locator('#projectexport').click()
            with zipfile.ZipFile(pending.value.path()) as archive:
                manifest=json.loads(archive.read('manifest.json'))
                assert manifest['sessions']==4 and manifest['records']==5 and manifest['include_internal']
            assert urllib.parse.urlsplit(page.url).path=='/'
            expect(page.locator('#search')).to_have_value('未登録')
            expect(page.locator('.session')).to_have_count(0)
            page.locator('#search').fill('認証')
            expect(page.locator('#downloadexport')).to_be_disabled()
            expect(page.locator('.session')).to_have_count(1)
            expect(page.locator('#downloadexport')).to_be_enabled()
            manifest,events=downloaded_zip(page,1,2)
            assert not manifest['include_internal'] and manifest['filters']['include_children']=='0'
            assert all(e['session']=='codex:root' for e in events)
            assert any('completed response' in json.dumps(e) for e in events)
            page.reload();expect(page.locator('.session')).to_have_count(1)
            expect(page.locator('#search')).to_have_value('認証')
            page.locator('[name=searchsource][value=claude]').check()
            expect(page.locator('.session')).to_have_count(0)
            expect(page.locator('#downloadexport')).to_be_disabled()
            page.locator('#search').fill('');expect(page.locator('.session')).to_have_count(1)
            downloaded_zip(page,1,1)
            page.reload();expect(page.locator('[name=searchsource][value=claude]')).to_be_checked()
            page.locator('[name=searchsource][value=claude]').uncheck()
            expect(page.locator('.session')).to_have_count(2)
            page.locator('#showdiagnostics').check();expect(page.locator('.session')).to_have_count(3)
            downloaded_zip(page,3,4)
            page.locator('#showdiagnostics').uncheck();expect(page.locator('.session')).to_have_count(2)
            page.locator('#searchoptions summary').click()
            page.locator('#identifierfield').fill('session.id');page.locator('#identifiervalue').fill('root')
            page.locator('#searchoptionsform button[type=submit]').click();expect(page.locator('.session')).to_have_count(1)
            downloaded_zip(page,1,2)
            page.locator('.session').click();expect(page.locator('#detailpage')).to_be_visible()
            page.locator('#detailpage > .back').click();expect(page.locator('.session')).to_have_count(1)
            assert urllib.parse.urlsplit(page.url).path=='/'
            page.locator('#searchoptions summary').click();page.locator('#clearidsearch').click()
            page.keyboard.press('Escape')
            page.locator('[data-project="name:Large"]').click();expect(page.locator('.session')).to_have_count(200)
            page.locator('#sessionpagination [aria-label="Next page"]').click();expect(page.locator('.session')).to_have_count(5)
            downloaded_zip(page,205,205)
            assert not any('/api/export/sessions' in u or '/api/archive' in u for u in requests),requests
            if os.getenv('AGENTLOG_SCREENSHOT'):
                page.evaluate('document.activeElement?.blur();window.scrollTo(0,0)')
                page.screenshot(path=os.environ['AGENTLOG_SCREENSHOT'],full_page=True)
            page.set_viewport_size({'width':390,'height':844});assert page.evaluate('document.documentElement.scrollWidth<=innerWidth')
            # Old bookmarks redirect to the shared list and retain filters.
            page.goto(base+'/export?project=name:決済&q=認証');expect(page.locator('.session')).to_have_count(1)
            assert urllib.parse.urlsplit(page.url).path=='/'
            assert not errors,errors
            browser.close()
    finally:
        server.terminate()
        server.wait(timeout=20)
print('PASS: shared list, direct and entire-project ZIP, all internal/child data, Japanese/ID/agent filters, native ZIP download, full sessions, pagination, reload/back, empty state and mobile')
