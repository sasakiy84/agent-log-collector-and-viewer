#!/usr/bin/env python3
"""Exercise local config and HTTP watch commands against a real agentlog process."""
from test_support import settled_urlopen
import json
import os
import pathlib
import socket
import subprocess
import tempfile
import time
import urllib.request

binary = str(pathlib.Path(os.getenv('AGENTLOG_BIN', './agentlog')).resolve())
token = 'watch-integration-token-at-least-16'
env = {**os.environ, 'AGENTLOG_TOKEN': token}


def wait_for(check):
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        try:
            if check():
                return
        except OSError:
            pass
        time.sleep(.05)
    raise AssertionError('Timed out waiting for watch ingestion')


def row(session, ident, text):
    return json.dumps({'type': 'user', 'uuid': ident, 'sessionId': session,
                       'cwd': '/work/shop', 'timestamp': '2026-11-01T00:00:00Z',
                       'message': {'content': text}}) + '\n'


with tempfile.TemporaryDirectory() as tmp:
    root = pathlib.Path(tmp)
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    base = f'http://127.0.0.1:{port}'

    def fetch(path):
        req = urllib.request.Request(base + path, headers={'Authorization': 'Bearer ' + token})
        with settled_urlopen(req, timeout=1) as response:
            return json.load(response)

    # Resolve this path relative to the config, independently of process cwd.
    local = root / 'local'
    local.mkdir()
    (local / 'session.jsonl').write_text(row('local-watch', 'initial', 'Local initial'))
    config = root / 'agentlog.json'
    config.write_text(json.dumps({'watch': [{'dir': 'local', 'project': 'shop', 'tags': ['backend']}],
                                  'watch_interval': '50ms'}))
    remote = root / 'remote'
    remote.mkdir()
    (remote / 'session.jsonl').write_text(row('remote-watch', 'initial', 'HTTP initial'))
    processes = []
    with (root / 'process.log').open('w+') as logs:
        def start(args):
            proc = subprocess.Popen([binary, *args], env=env, stdout=logs, stderr=logs)
            processes.append(proc)
            return proc

        def stop(proc):
            proc.terminate()
            assert proc.wait(timeout=10) == 0, 'process did not exit cleanly'
            processes.remove(proc)

        server_args = ['--listen', f'127.0.0.1:{port}', '--db', str(root / 'events.db'), '--config', str(config)]
        try:
            # Initially offline: the unchanged remote file must retry delivery.
            watcher = start(['import', '--watch', '--watch-state', str(root / 'watch.db'), '--dir', str(remote), '--interval', '50ms', '--endpoint', base])
            time.sleep(.2)
            server = start(server_args)
            wait_for(lambda: fetch('/api/stats')['events'] == 2)
            assert 'backend' in json.dumps(fetch('/api/session?id=claude:local-watch'))
            # Both watcher modes accept append and new nested sessions.
            with (local / 'session.jsonl').open('a') as f:
                f.write(row('local-watch', 'second', 'Local append'))
            nested = remote / 'new' / 'nested'
            nested.mkdir(parents=True)
            (nested / 'session.jsonl').write_text(row('remote-watch', 'second', 'Remote nested'))
            wait_for(lambda: fetch('/api/stats')['events'] == 4)
            stop(watcher)
            stop(server)
            server = start(server_args)
            wait_for(lambda: fetch('/api/stats')['events'] == 4)
            time.sleep(.15)
            assert fetch('/api/stats')['events'] == 4, 'restart duplicated records'
            stop(server)
        except BaseException:
            for proc in processes:
                proc.terminate()
            for proc in processes:
                proc.wait(timeout=10)
            logs.flush()
            logs.seek(0)
            print(logs.read())
            raise
print('PASS: local config, relative paths, HTTP offline retry, append, nested folders, restart deduplication and graceful shutdown.')
