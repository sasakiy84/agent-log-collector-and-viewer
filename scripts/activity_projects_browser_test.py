#!/usr/bin/env python3
"""Persistent project selection and server-filtered activity. Synthetic data only."""
from test_support import settled_urlopen
import json
import os
import socket
import subprocess
import tempfile
import time
import urllib.request
import urllib.parse
from pathlib import Path

from playwright.sync_api import expect, sync_playwright

BINARY = os.getenv('AGENTLOG_BIN', './agentlog')
TOKEN = 'synthetic-activity-projects-test-token'
OUT = Path(os.getenv('AGENTLOG_SCREENSHOT_DIR', Path(__file__).resolve().parents[1] / 'docs'))
OUT.mkdir(parents=True, exist_ok=True)


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
                settled_urlopen(base, timeout=1).close()
                break
            except OSError:
                assert proc.poll() is None, 'Server exited'
                time.sleep(.05)

        def api(path, body=None):
            req = urllib.request.Request(base + path, json.dumps(body).encode() if body is not None else None,
                                         headers={'Content-Type': 'application/json', 'Authorization': 'Bearer ' + TOKEN})
            return json.load(settled_urlopen(req))

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
            # Replacing a sidebar request must not cancel the parallel session list.
            held = []
            def hold_first_scope(route):
                query = urllib.parse.parse_qs(urllib.parse.urlsplit(route.request.url).query)
                if query.get('session_q') == ['setup'] and not query.get('q'):
                    held.append(route)
                else:
                    route.continue_()
            page.route('**/api/projects?*', hold_first_scope)
            with page.expect_request(lambda request: '/api/projects?' in request.url and 'session_q=setup' in request.url):
                page.locator('#search').fill('setup')
            # Results must render while the independent project query is held.
            expect(page.locator('.session')).to_have_count(1)
            expect(page.locator('.session')).to_contain_text('Update the setup guide.')
            expect(page.locator('#workspace')).not_to_have_attribute('aria-busy', 'true')
            page.locator('#projectsearch').fill('Documentation')
            expect(page.locator('.session')).to_have_count(1)
            expect(page.locator('.session')).to_contain_text('Update the setup guide.')
            expect(page.locator('[data-project="name:Documentation"]')).to_be_visible()
            for route in held:
                route.abort()
            page.unroute('**/api/projects?*', hold_first_scope)
            def fail_scope(route):
                route.fulfill(status=500, body='Synthetic sidebar failure')
            page.route('**/api/projects?*', fail_scope)
            page.locator('#search').fill('guide')
            expect(page.locator('.session')).to_have_count(1)
            expect(page.locator('.session')).to_contain_text('Update the setup guide.')
            expect(page.locator('#error')).to_be_empty()
            expect(page.locator('#projectstatus')).to_contain_text('HTTP 500')
            page.unroute('**/api/projects?*', fail_scope)
            page.goto(base)
            expect(page.locator('.session')).to_have_count(7)
            # A successful sidebar for a rejected query must not survive when
            # the main list restores its previous successful search scope.
            rejected_lists = []
            def hold_rejected_list(route):
                query = urllib.parse.parse_qs(urllib.parse.urlsplit(route.request.url).query)
                if query.get('q') == ['setup']:
                    rejected_lists.append(route)
                else:
                    route.continue_()
            page.route('**/api/sessions?*', hold_rejected_list)
            page.locator('#search').fill('setup')
            expect(page.locator('#projectlist button')).to_have_count(1)
            expect(page.locator('[data-project="name:Documentation"]')).to_be_visible()
            assert rejected_lists, 'Main list request was not held'
            rejected_lists.pop().fulfill(status=500, body='Synthetic list failure')
            expect(page.locator('#search')).to_have_value('')
            expect(page.locator('.session')).to_have_count(7)
            expect(page.locator('#error')).to_contain_text('HTTP 500')
            expect(page.locator('#projectlist button')).to_have_count(5)
            expect(page.locator('[data-project="name:Agentlog"] small')).to_have_text('2')
            page.unroute('**/api/sessions?*', hold_rejected_list)
            page.locator('[data-project="name:Agentlog"]').click()
            expect(page.locator('.session')).to_have_count(2)
            page.locator('#search').fill('日本語')
            expect(page.locator('.session')).to_have_count(1)
            expect(page.locator('[data-project="name:Website"]')).to_have_count(0)
            page.locator('#search').fill('')
            expect(page.locator('.session')).to_have_count(2)
            expect(page.locator('[data-project="name:Website"]')).to_be_visible()
            page.evaluate('document.activeElement?.blur();window.scrollTo(0,0)')
            page.screenshot(path=str(OUT / 'ui-projects.png'), full_page=True)
            page.locator('[data-session="codex:search"]').click()
            expect(page.locator('#timeline > .event')).to_have_count(2)
            expect(page.locator('[data-view="conversation"]')).to_have_attribute('aria-pressed', 'true')
            page.locator('[data-view="activity"]').click()
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
            # Browsers may restore form controls after deferred scripts run.
            # A restored checkbox must not silently change the URL's filters.
            page.add_init_script('''document.addEventListener('DOMContentLoaded', () => {
                if (location.pathname === '/' && !new URL(location.href).searchParams.has('diagnostics'))
                    document.querySelector('#showdiagnostics').checked = true;
            });''')
            page.locator('#detailpage > .back').click()
            expect(page.locator('[data-project="name:Agentlog"]')).to_have_attribute('aria-current', 'true')
            expect(page.locator('.session')).to_have_count(2)
            expect(page.locator('#showdiagnostics')).not_to_be_checked()
            assert 'diagnostics=' not in page.url, page.url
            page.go_back()
            expect(page.locator('#detailpage')).to_be_visible()
            page.go_forward()
            expect(page.locator('.session')).to_have_count(2)
            expect(page.locator('#showdiagnostics')).not_to_be_checked()
            assert 'diagnostics=' not in page.url, page.url
            # Explicit inclusion remains enabled when returning from details.
            page.locator('#showdiagnostics').check()
            expect(page).to_have_url(base + '/?project=name%3AAgentlog&diagnostics=1')
            page.locator('[data-session="codex:search"]').click()
            expect(page.locator('#detailpage')).to_be_visible()
            page.locator('[data-view="diagnostics"]').click()
            page.locator('#detailpage > .back').click()
            expect(page.locator('.session')).to_have_count(2)
            expect(page.locator('#showdiagnostics')).to_be_checked()
            assert 'diagnostics=1' in page.url, page.url
            page.locator('#showdiagnostics').uncheck()
            expect(page).to_have_url(base + '/?project=name%3AAgentlog')
            page.locator('[data-project=""]').click()
            expect(page.locator('.session')).to_have_count(1)
            expect(page.locator('.session h2')).to_have_text('Hello!')
            page.locator('#allprojects').click()
            expect(page.locator('.session')).to_have_count(7)
            page.locator('#projectsearch').fill('app')
            expect(page.locator('#projectlist button')).to_have_count(1)
            page.locator('#projectlist button').click()
            expect(page.locator('.session')).to_have_count(2)
            expect(page.locator('#sessions')).to_contain_text('client A')
            expect(page.locator('#sessions')).to_contain_text('client B')
            page.locator('#projectsearch').fill('')
            expect(page.locator('#projectlist button')).to_have_count(5)
            page.set_viewport_size({'width': 390, 'height': 844})
            expect(page.locator('#projectsidebar')).to_be_visible()
            assert page.evaluate('document.documentElement.scrollWidth <= innerWidth')
            page.mouse.move(0,0);page.evaluate('document.activeElement?.blur()')
            page.screenshot(path=str(OUT / 'ui-projects-mobile.png'), full_page=True)
            # The same independent navigation contract applies to Skills.
            skill_projects = []
            def hold_skill_projects(route):
                skill_projects.append(route)
            page.route('**/api/projects?*', hold_skill_projects)
            page.goto(base + '/skills')
            expect(page.locator('#skillrows .empty')).to_have_text('No skills found')
            expect(page.locator('#workspace')).not_to_have_attribute('aria-busy', 'true')
            assert skill_projects, 'Sidebar request was not exercised'
            for route in skill_projects:
                route.abort()
            page.unroute('**/api/projects?*', hold_skill_projects)
            page.route('**/api/projects?*', fail_scope)
            page.reload()
            expect(page.locator('#skillrows .empty')).to_have_text('No skills found')
            expect(page.locator('#error')).to_be_empty()
            expect(page.locator('#projectstatus')).to_contain_text('HTTP 500')
            page.unroute('**/api/projects?*', fail_scope)
            page.reload()
            expect(page.locator('#projectlist button')).to_have_count(5)
            rejected_skills = []
            def hold_rejected_skills(route):
                query = urllib.parse.parse_qs(urllib.parse.urlsplit(route.request.url).query)
                if query.get('source') == ['claude']:
                    rejected_skills.append(route)
                else:
                    route.continue_()
            page.route('**/api/skills?*', hold_rejected_skills)
            page.set_viewport_size({'width': 1440, 'height': 1000})
            page.locator('[name="searchsource"][value="claude"]').check()
            expect(page.locator('#projectlist button')).to_have_count(0)
            assert rejected_skills, 'Skills request was not held'
            rejected_skills.pop().fulfill(status=500, body='Synthetic skills failure')
            expect(page.locator('[name="searchsource"][value="claude"]')).not_to_be_checked()
            expect(page.locator('#projectlist button')).to_have_count(5)
            expect(page.locator('#error')).to_contain_text('HTTP 500')
            page.unroute('**/api/skills?*', hold_rejected_skills)
            assert not errors, errors
            browser.close()
    finally:
        proc.terminate()
        proc.wait(timeout=20)
print('PASS: persistent projects, selection, search, unassigned and merged same-name directories, URL return/reload, server filtering, call inspector/JSON, mobile; synthetic screenshots.')
