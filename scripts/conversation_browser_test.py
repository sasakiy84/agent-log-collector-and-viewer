#!/usr/bin/env python3
"""Conversation-first UI, lazy network requests and Guardian isolation."""
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.request

from playwright.sync_api import expect, sync_playwright
from test_support import wait_for_ingestion

binary = os.getenv('AGENTLOG_BIN', './agentlog')
out = Path(os.getenv('AGENTLOG_SCREENSHOT_DIR', '/tmp/agentlog-conversation-shots'))
out.mkdir(parents=True, exist_ok=True)


def attrs(values):
    return [{'key': key, 'value': {'boolValue': value} if isinstance(value, bool)
             else {'stringValue': str(value)}} for key, value in values.items()]


with tempfile.TemporaryDirectory() as directory:
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    base = f'http://127.0.0.1:{port}'
    server = subprocess.Popen([binary, '--listen', f'127.0.0.1:{port}', '--db', directory + '/test.db'],
                              env={**os.environ, 'AGENTLOG_TOKEN': ''},
                              stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        for _ in range(200):
            try:
                urllib.request.urlopen(base, timeout=1).close()
                break
            except OSError:
                assert server.poll() is None
                time.sleep(.05)
        stamp = 1794759050000000000

        def log(session, index, name, values):
            return {'eventName': name, 'timeUnixNano': str(stamp + index * 1000000000),
                    'attributes': attrs({'session.id': session, **values})}

        rows = [log('chat', 0, 'codex.user_prompt', {'prompt': '検索結果の表示を確認して、読み込み速度を改善してください。'}),
                log('chat', 1, 'gen_ai.tool.start', {'tool_name': 'exec_command', 'call_id': 'call', 'arguments': '{"cmd":"go test ./internal/app"}'}),
                log('chat', 2, 'codex.tool_result', {'tool_name': 'exec_command', 'call_id': 'call', 'output': '日本語の認証エラーを検出', 'success': True}),
                log('chat', 4, 'codex.agent_response', {'response': '検索条件とデータベースのクエリを確認しました。境界条件をテストします。'})]
        rows += [log('chat', i, 'codex.tool_result', {'tool_name': 'exec_command', 'output': f'Check {i}: PASS'}) for i in range(5, 75)]
        rows += [log('chat', 75, 'internal_failure', {'error': 'One boundary case failed; investigation is recorded below.'}),
                 log('chat', 76, 'codex.agent_response', {'response': '会話を先に読み込み、処理ログは開いたときだけ取得するよう修正しました。エラーも確認できます。'}),
                 log('no-reply', 0, 'codex.user_prompt', {'prompt': 'Run the failing check.'}),
                 log('no-reply', 1, 'codex.tool_result', {'tool_name': 'exec_command', 'error': 'Process interrupted', 'success': False}),
                 log('no-text', 0, 'codex.tool_result', {'tool_name': 'exec_command', 'output': 'Tool-only telemetry'})]
        resource = {'attributes': attrs({'service.name': 'codex', 'service.namespace': 'Agentlog', 'cwd': '/work/agentlog'})}

        def send(signal, body):
            request = urllib.request.Request(base + '/v1/' + signal, json.dumps(body).encode(), headers={'Content-Type': 'application/json'})
            urllib.request.urlopen(request).close()

        send('logs', {'resourceLogs': [{'resource': resource, 'scopeLogs': [{'logRecords': rows}]}]})
        send('traces', {'resourceSpans': [{'resource': resource, 'scopeSpans': [{'spans': [{
            'name': 'guardian_scoring_observation', 'traceId': 'a' * 32, 'spanId': 'b' * 16,
            'startTimeUnixNano': str(stamp + 3000000000), 'endTimeUnixNano': str(stamp + 3000012938),
            'attributes': attrs({'session.id': 'chat', 'tool_name': 'exec_command', 'call_id': 'call',
                                 'code.module.name': 'codex_guardian_v2::async_scorer::observation'})}, {
            'name': 'exec', 'traceId': 'a' * 32, 'spanId': 'c' * 16,
            'startTimeUnixNano': str(stamp + 2500000000), 'endTimeUnixNano': str(stamp + 3500000000),
            'attributes': attrs({'session.id': 'chat', 'tool_name': 'exec_command', 'call_id': 'call',
                                 'codex.tool.phase': 'dispatch', 'code.module.name': 'codex_core::tools::parallel'})}]}]}]})
        wait_for_ingestion(base, '')
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path='/usr/bin/chromium', headless=True, args=['--no-sandbox'])
            page = browser.new_page(viewport={'width': 1440, 'height': 1000}, locale='ja-JP', timezone_id='Asia/Tokyo')
            errors, requests = [], []
            page.on('pageerror', lambda error: (errors.append(str(error)), print('Browser error:', error, flush=True)))
            page.on('response', lambda response: print('HTTP error:', response.status, response.url, flush=True) if response.status >= 400 else None)
            page.on('request', lambda request: requests.append(request.url))
            page.goto(base + '/sessions/codex%3Achat')
            expect(page.locator('#timeline > .event')).to_have_count(3)
            expect(page.locator('.processing-group')).to_have_count(2)
            expect(page.locator('#eventfilters')).to_be_hidden()
            expect(page.locator('.processing-group').last.locator('summary')).to_contain_text('1 error')
            assert not any('/api/processing?' in request or '/api/event?' in request for request in requests), requests
            page.evaluate('document.activeElement?.blur();window.scrollTo(0,0)')
            page.screenshot(path=str(out / 'conversation-desktop.png'), full_page=True)
            first = page.locator('.processing-group').first
            expect(first.locator('.processing-count')).to_have_text(' · 2 logs')
            first.locator('summary').click()
            expect(first.locator('.event')).to_have_count(2)
            first.locator('.event.tool').first.click()
            expect(page.locator('.toolpair')).to_contain_text('日本語の認証エラー')
            page.locator('#closeinspector').click()
            first.locator('[data-processing-diagnostics]').check()
            expect(first.locator('.event')).to_have_count(4)
            expect(first.locator('.processing-count')).to_have_text(' · 4 logs')
            expect(first.locator('.event.tool')).to_have_count(2)
            timing = first.locator('.event').filter(has_text='Tool execution timing')
            timing.click()
            expect(page.locator('#inspectortitle')).to_have_text('Tool execution timing')
            expect(page.locator('#inspectdetails')).to_contain_text('dispatch')
            expect(page.locator('.toolpair')).to_have_count(0)
            assert 'Tool content not exported' not in first.inner_text()
            page.screenshot(path=str(out / 'tool-timing-inspector.png'), full_page=True)
            page.locator('#closeinspector').click()
            guardian = first.locator('.event').filter(has_text='Guardian internal observation')
            guardian.click()
            expect(page.locator('#inspectortitle')).to_have_text('Guardian internal observation')
            expect(page.locator('#inspectdetails')).to_contain_text('0.012938 ms')
            expect(page.locator('.toolpair')).to_have_count(0)
            # Details omit raw JSON; only opening JSON fetches the original.
            assert all('raw=0' in request for request in requests if '/api/event?' in request), requests
            page.locator('#jsontab').click()
            expect(page.locator('#eventjson')).to_contain_text('guardian_scoring_observation')
            assert sum('/api/event?' in request and 'raw=0' not in request for request in requests) == 1
            page.locator('#detailtab').click(); page.locator('#jsontab').click()
            assert sum('/api/event?' in request and 'raw=0' not in request for request in requests) == 1
            page.screenshot(path=str(out / 'guardian-inspector.png'), full_page=True)
            page.locator('#closeinspector').click()
            first.locator('summary').click()
            second = page.locator('.processing-group').last
            second.locator('.processing-error').click()
            expect(second.locator('.event')).to_have_count(1)
            expect(second.locator('.event')).to_contain_text('One boundary case failed')
            second.locator('[data-processing-errors]').uncheck()
            expect(second.locator('.event')).to_have_count(50)
            # Closing during a load-more request must leave the control usable.
            held = []
            page.route('**/api/processing?*cursor=*', lambda route: held.append(route))
            with page.expect_request(lambda request: '/api/processing?' in request.url and 'cursor=' in request.url):
                second.locator('.processing-more').click()
            second.locator('summary').click()
            for route in held:
                route.abort()
            page.unroute('**/api/processing?*cursor=*')
            second.locator('summary').click()
            expect(second.locator('.processing-more')).to_be_enabled()
            second.locator('.processing-more').click()
            expect(second.locator('.event')).to_have_count(71)
            expect(second.locator('.processing-more')).to_be_hidden()
            second.locator('.event').last.click()
            expect(page.locator('#inspectdetails')).to_contain_text('One boundary case failed')
            page.locator('#closeinspector').click()
            before = sum('/api/processing?' in request for request in requests)
            second.locator('summary').click()
            second.locator('summary').click()
            expect(second.locator('.event')).to_have_count(71)
            assert sum('/api/processing?' in request for request in requests) == before
            second.locator('summary').click()
            for width in [1440, 1024, 768, 390, 320]:
                page.set_viewport_size({'width': width, 'height': 900})
                page.evaluate('document.activeElement?.blur();window.scrollTo(0,0)')
                assert page.evaluate('document.documentElement.scrollWidth <= innerWidth'), width
                page.screenshot(path=str(out / f'conversation-{width}.png'), full_page=True)
            page.set_viewport_size({'width': 1440, 'height': 1000})
            page.goto(base + '/?project=name%3AAgentlog&q=' + urllib.parse.quote('認証'))
            expect(page.locator('.session')).to_have_count(1)
            page.locator('.session').click()
            expect(page.locator('.processing-match')).to_contain_text('1 log matches')
            page.locator('.processing-match').click()
            expect(page.locator('.processing-group[open] .event')).to_have_count(1)
            expect(page.locator('.processing-group[open] .event')).to_contain_text('日本語の認証エラー')
            page.goto(base + '/sessions/codex%3Ano-reply')
            expect(page.locator('.conversation-status')).to_have_text('No assistant response recorded.')
            expect(page.locator('.processing-group summary')).to_contain_text('1 error')
            page.goto(base + '/sessions/codex%3Ano-text')
            expect(page.locator('#timeline > .empty')).to_have_text('Conversation text was not recorded.')
            page.locator('.processing-group summary').click()
            expect(page.locator('.processing-events')).to_contain_text('Tool-only telemetry')
            # An import may replace a boundary's ID between reading and opening.
            page.goto(base + '/sessions/codex%3Achat')
            expect(page.locator('#timeline > .event')).to_have_count(3)
            page.route('**/api/processing?*', lambda route: route.fulfill(status=409, body='stale range'))
            page.locator('.processing-group summary').first.click()
            expect(page.locator('.processing-retry').first).to_have_text('Reload session')
            page.unroute('**/api/processing?*')
            page.locator('.processing-retry').first.click()
            expect(page.locator('.processing-group[open]')).to_have_count(0)
            page.locator('.processing-group summary').first.click()
            expect(page.locator('.processing-group').first.locator('.event')).to_have_count(2)
            assert not errors, errors
            browser.close()
    finally:
        server.terminate()
        server.wait(timeout=20)
print('PASS: conversation-only initial requests, lazy/cursor loading, diagnostics opt-in, Guardian JSON/tool pairing, search evidence, missing answers/content, five viewport widths')
