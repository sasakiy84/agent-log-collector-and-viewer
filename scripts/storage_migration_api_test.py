#!/usr/bin/env python3
"""Compare published v0.23 API data with the Python-migrated current binary.

This integration harness runs both servers. migrate_db.py itself uses only Python.
"""
import argparse
from contextlib import contextmanager
import importlib.util
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.parse
import urllib.request

from test_support import wait_for_ingestion

parser = argparse.ArgumentParser()
parser.add_argument('--baseline', required=True)
parser.add_argument('--binary', required=True)
args = parser.parse_args()
TOKEN = 'synthetic-storage-migration-owner'
ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('migration', ROOT / 'scripts/migrate_db.py')
migration = importlib.util.module_from_spec(spec)
spec.loader.exec_module(migration)


def call(base, path, body=None, token=TOKEN):
    headers = {'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'}
    request = urllib.request.Request(base + path, None if body is None else json.dumps(body).encode(), headers=headers)
    with urllib.request.urlopen(request, timeout=30) as response:
        return json.load(response)


@contextmanager
def server(binary, database):
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    base = f'http://127.0.0.1:{port}'
    with database.with_suffix('.log').open('w') as log:
        process = subprocess.Popen([str(Path(binary).resolve()), '--db', str(database), '--listen', f'127.0.0.1:{port}'],
                                   env={**os.environ, 'AGENTLOG_TOKEN': TOKEN}, stdout=log, stderr=log)
        try:
            deadline = time.monotonic() + 45
            while time.monotonic() < deadline:
                try:
                    state = call(base, '/api/status')
                    if state['state'] == 'failed':
                        raise RuntimeError(database.with_suffix('.log').read_text())
                    if state['state'] == 'ready' and state['pending_batches'] == 0 and call(base, '/api/skills')['ready']:
                        break
                except OSError:
                    if process.poll() is not None:
                        raise RuntimeError(database.with_suffix('.log').read_text())
                time.sleep(.02)
            else:
                raise TimeoutError('server not ready')
            yield base
        finally:
            process.terminate()
            process.wait(timeout=20)


def attributes(values):
    def value(item):
        if isinstance(item, bool):
            return {'boolValue': item}
        if isinstance(item, (dict, list)):
            return {'stringValue': json.dumps(item, ensure_ascii=False)}
        return {'stringValue': str(item)}
    return [{'key': key, 'value': value(item)} for key, item in values.items()]


def seed(base):
    counter = 0
    def log(source, session, name, values, imported=False, parent=''):
        nonlocal counter
        counter += 1
        attrs = {'session.id': session, 'event.name': name, **values}
        if parent:
            attrs['parent.session.id'] = parent
        if imported:
            attrs.update({'agentlog.origin': 'import', 'agentlog.import.id': str(counter), 'agentlog.import.sequence': counter})
        body = {'resourceLogs': [{'resource': {'attributes': attributes({'service.name': source, 'cwd': '/work/demo', 'service.namespace': 'demo'})},
                'scopeLogs': [{'scope': {'name': 'agentlog.import' if imported else 'fixture'}, 'logRecords': [{
                'timeUnixNano': str(1790000000000000000 + counter * 1000000000), 'eventName': name, 'attributes': attributes(attrs)}]}]}]}
        call(base, '/v1/logs', body)
        wait_for_ingestion(base, TOKEN)
    for source in ('codex', 'claude', 'copilot'):
        log(source, source + '-conversation', 'gen_ai.user.message', {'gen_ai.message.id': 'msg-user', 'prompt': '日本語の検索を高速化して'}, True)
        log(source, source + '-conversation', 'gen_ai.user.message', {'message_id': 'msg-user', 'prompt': '日本語の検索を高速化して'})
        log(source, source + '-conversation', 'gen_ai.tool.start', {'gen_ai.tool.call.id': 'call-read', 'gen_ai.tool.name': 'exec_command', 'gen_ai.tool.call.arguments': {'cmd': 'cat /work/skills/review/SKILL.md'}}, True)
        log(source, source + '-conversation', 'gen_ai.tool.result', {'gen_ai.tool.call.id': 'call-read', 'gen_ai.tool.name': 'exec_command', 'gen_ai.tool.call.result': 'Process exited with code 0\nOutput:\nreview instructions', 'success': True})
        log(source, source + '-conversation', 'gen_ai.assistant.message', {'gen_ai.message.id': 'msg-answer', 'response': '検索を改善しました。', 'model': 'synthetic-model'}, True)
    log('codex', 'codex-conversation', 'codex.skill_invocation', {'skill.name': 'review', 'skill.scope': 'repo', 'turn.id': 'one'})
    log('codex', 'child', 'gen_ai.user.message', {'prompt': 'review the change'}, parent='codex-conversation')
    log('codex', 'automatic', 'codex.user_prompt', {'prompt': 'This is an app-generated request for a suggested next user message. The user did not write this message.'})
    # A completed model call and an overlapping delta-metric series exercise
    # model facts, usage selection and the interval R-tree after conversion.
    start = 1790000000000000000
    call(base, '/v1/traces', {'resourceSpans': [{'resource': {'attributes': attributes({'service.name': 'codex', 'service.namespace': 'demo'})}, 'scopeSpans': [{'spans': [{
        'name': 'gen_ai.client.operation', 'traceId': 'a' * 32, 'spanId': 'b' * 16,
        'startTimeUnixNano': str(start), 'endTimeUnixNano': str(start + 1000000000), 'attributes': attributes({
        'session.id': 'codex-conversation', 'turn.id': 'one', 'gen_ai.operation.name': 'chat', 'gen_ai.request.model': 'synthetic-model', 'gen_ai.usage.input_tokens': '9007199254740993', 'gen_ai.usage.output_tokens': '78'})}]}]}]})
    for shift in (0, 1, 3):
        call(base, '/v1/metrics', {'resourceMetrics': [{'resource': {'attributes': attributes({'service.name': 'codex'})}, 'scopeMetrics': [{'metrics': [{
            'name': 'gen_ai.client.token.usage', 'sum': {'aggregationTemporality': 1, 'isMonotonic': True, 'dataPoints': [{
            'startTimeUnixNano': str(start + shift * 1000000000), 'timeUnixNano': str(start + (shift + 2) * 1000000000), 'asInt': '10', 'attributes': attributes({'session.id': 'metric-session', 'gen_ai.token.type': 'input'})}]}}]}]}]})
    wait_for_ingestion(base, TOKEN)
    return call(base, '/api/settings/api-keys', {'name': 'agent-readonly'})


def snapshot(base):
    paths = ['/api/sessions', '/api/projects', '/api/folders', '/api/skills',
             '/api/sessions?q=' + urllib.parse.quote('日本語'), '/api/sessions?project=name%3Ademo',
             '/api/sessions?diagnostics=1', '/api/search/summary']
    result = {path: call(base, path) for path in paths}
    sessions = call(base, '/api/sessions?diagnostics=1')
    for session in sessions:
        query = urllib.parse.urlencode({'id': session['id']})
        for origin in ('', 'import', 'otel'):
            for view in ('conversation', 'activity', 'diagnostics'):
                path = f'/api/session?{query}&origin={origin}&view={view}&position=latest'
                result[path] = call(base, path)
        path = '/api/export?' + urllib.parse.urlencode({'session': session['id']})
        result[path] = call(base, path)
    result['/api/settings/api-keys'] = call(base, '/api/settings/api-keys')
    metrics = call(base, '/api/metrics')
    result['metrics'] = {key: metrics[key] for key in ('events', 'sessions', 'logical_events', 'raw_bytes', 'counts_ready')}
    return result


with tempfile.TemporaryDirectory() as temporary:
    directory = Path(temporary)
    source = directory / 'old' / 'agentlog.db'
    source.parent.mkdir()
    with server(args.baseline, source) as base:
        key = seed(base)
        before = snapshot(base)
    migration.run(source, directory / 'new')
    with server(args.binary, directory / 'new' / 'agentlog.db') as base:
        after = snapshot(base)
        for path in before:
            assert before[path] == after[path], (path, before[path], after[path])
        assert call(base, '/api/projects', token=key['key']) == after['/api/projects']
    print(f'PASS: {len(before)} API snapshots match across offline migration; original bytes, Japanese search, origins, usage, skills, children, API keys and restart retained.')
