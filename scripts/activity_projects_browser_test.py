#!/usr/bin/env python3
"""Persistent project selection and server-filtered activity. Synthetic data only."""
import json
import os
import socket
import subprocess
import tempfile
import time
import urllib.request
from pathlib import Path

from playwright.sync_api import expect, sync_playwright

BINARY = os.getenv('AGENTLOG_BIN', './agentlog')
TOKEN = 'synthetic-activity-projects-test-token'
OUT = Path(__file__).resolve().parents[1] / 'docs'


def attrs(values):
    return [{'key': key, 'value': {'stringValue': str(value)}} for key, value in values.items()]


with tempfile.TemporaryDirectory() as directory:
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    base = f'http://127.0.0.1:{port}'
    proc = subprocess.Popen([BINARY, '--listen', f'127.0.0.1:{port}', '--db', directory + '/test.db'],
                            env={**os.environ, 'AGENTLOG_TOKEN': TOKEN},
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        for _ in range(300):
            try:
                urllib.request.urlopen(base, timeout=1).close()
                break
            except OSError:
                assert proc.poll() is None, 'Server exited'
                time.sleep(.05)

        def api(path, body=None):
            req = urllib.request.Request(base + path, json.dumps(body).encode() if body is not None else None,
                                         headers={'Content-Type': 'application/json', 'Authorization': 'Bearer ' + TOKEN})
            return json.load(urllib.request.urlopen(req))

        stamp = 1794736800000000000

        def log(session, index, name, values):
            return {'eventName': name, 'timeUnixNano': str(stamp + index * 1000000000),
                    'attributes': attrs({'session.id': session, **values})}

        rows = [log('search', 0, 'codex.user_prompt', {'service.namespace': 'Agentlog', 'prompt': '日本語の検索結果を確認して、絞り込みの問題を直して。', 'turn.id': 'one'}),
                log('search', 2, 'codex.tool_result', {'tool_name': 'exec_command', 'arguments': '{"cmd":"go test ./internal/app"}', 'output': 'PASS', 'success': 'true'}),
                log('search', 4, 'codex.agent_response', {'response': 'プロジェクトを選択した状態で検索条件を適用し、表示件数とページ分割を修正しました。', 'turn.id': 'one'})]
        rows += [log('search', 10 + i, 'prepare_tools', {'service.namespace': 'Agentlog'}) for i in range(410)]
        for session, name, path, prompt in [
                ('api', 'Agentlog', '/work/api', 'Reduce session list loading time.'),
                ('web', 'Website', '/work/website', 'Review the responsive navigation.'),
                ('docs', 'Documentation', '/work/docs', 'Update the setup guide.'),
                ('one', '', '/work/client-a/app', 'Check the client A build.'),
                ('two', '', '/work/client-b/app', 'Check the client B build.'),
                ('unknown', '', '', 'Hello!')]:
            values = {'prompt': prompt}
            if name:
                values['service.namespace'] = name
            if path:
                values['cwd'] = path
            rows.append(log(session, -100, 'codex.user_prompt', values))
        resource = {'attributes': attrs({'service.name': 'codex'})}
        api('/v1/logs', {'resourceLogs': [{'resource': resource, 'scopeLogs': [{'logRecords': rows}]}]})
        api('/v1/traces', {'resourceSpans': [{'resource': resource, 'scopeSpans': [{'spans': [
            {'name': 'run_sampling_request', 'traceId': 'a' * 32, 'spanId': 'a' * 16,
             'startTimeUnixNano': str(stamp + 1000000000), 'endTimeUnixNano': str(stamp + 3000000000),
             'attributes': attrs({'session.id': 'search', 'turn.id': 'one', 'model': 'example-model',
                                  'gen_ai.usage.input_tokens': '4200', 'gen_ai.usage.output_tokens': '518',
                                  'gen_ai.usage.cache_read.input_tokens': '2048'})}]}]}]})

        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path='/usr/bin/chromium', headless=True, args=['--no-sandbox'])
            page = browser.new_page(viewport={'width': 1440, 'height': 1000})
            errors = []
            page.on('pageerror', lambda error: errors.append(str(error)))
            page.goto(base)
            page.locator('#token').fill(TOKEN)
            page.locator('#loginform button').click()
            expect(page.locator('.session')).to_have_count(7)
            expect(page.locator('#projectsidebar')).to_be_visible()
            page.locator('[data-project="name:Agentlog"]').click()
            expect(page.locator('.session')).to_have_count(2)
            page.locator('#search').fill('日本語')
            expect(page.locator('.session')).to_have_count(1)
            expect(page.locator('[data-project="name:Website"]')).to_be_visible()
            page.locator('#search').fill('')
            expect(page.locator('.session')).to_have_count(2)
            page.screenshot(path=str(OUT / 'ui-projects.png'), full_page=True)
            page.locator('[data-session="codex:search"]').click()
            expect(page.locator('.event')).to_have_count(3)
            expect(page.locator('.event.assistant .model-calls')).to_contain_text('4.2K in')
            expect(page.locator('.event.assistant .prose')).to_contain_text('プロジェクトを選択')
            expect(page.locator('[data-view="activity"]')).to_have_attribute('aria-pressed', 'true')
            page.locator('.model-calls button').click()
            expect(page.locator('#inspector')).to_be_visible()
            expect(page.locator('#inspectdetails .model-calls')).to_contain_text('518 out')
            page.locator('#jsontab').click()
            expect(page.locator('#eventjson')).to_contain_text('run_sampling_request')
            page.locator('#closeinspector').click()
            page.screenshot(path=str(OUT / 'ui-activity.png'), full_page=True)
            page.locator('[data-view="diagnostics"]').click()
            expect(page.locator('#pageinfo')).to_contain_text('401–414 / 414')
            page.locator('button[data-kind="conversation"]').click()
            expect(page.locator('.event')).to_have_count(2)
            expect(page.locator('#pageinfo')).to_contain_text('1–2 / 2')
            page.reload()
            expect(page.locator('.event')).to_have_count(2)
            expect(page.locator('button[data-kind="conversation"]')).to_have_class('active')
            page.locator('#detailpage > .back').click()
            expect(page.locator('[data-project="name:Agentlog"]')).to_have_attribute('aria-current', 'true')
            page.locator('[data-project=""]').click()
            expect(page.locator('.session')).to_have_count(1)
            expect(page.locator('.session h2')).to_have_text('Hello!')
            page.locator('#allprojects').click()
            expect(page.locator('.session')).to_have_count(7)
            page.locator('#projectsearch').fill('client-b')
            expect(page.locator('#projectlist button')).to_have_count(1)
            page.locator('#projectlist button').click()
            expect(page.locator('.session')).to_have_count(1)
            expect(page.locator('.session h2')).to_contain_text('client B')
            page.locator('#projectsearch').fill('')
            expect(page.locator('#projectlist button')).to_have_count(6)
            page.set_viewport_size({'width': 390, 'height': 844})
            expect(page.locator('#projectsidebar')).to_be_visible()
            assert page.evaluate('document.documentElement.scrollWidth <= innerWidth')
            page.screenshot(path=str(OUT / 'ui-projects-mobile.png'), full_page=True)
            assert not errors, errors
            browser.close()
    finally:
        proc.terminate()
        proc.wait(timeout=20)
print('PASS: persistent projects, selection, search, unassigned and distinct paths, URL return/reload, server filtering, call inspector/JSON, mobile; synthetic screenshots.')
