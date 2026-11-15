#!/usr/bin/env python3
"""Real-process ACK/crash/restart and watch-checkpoint coverage. Synthetic data."""
import json
import os
from pathlib import Path
import socket
import sqlite3
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

BINARY = str(Path(os.getenv('AGENTLOG_BIN', './agentlog')).resolve())
TOKEN = 'synthetic-startup-ingestion-token'

with tempfile.TemporaryDirectory() as directory:
    root = Path(directory)
    database = root / 'events.db'
    inbox = root / 'events.db.ingest.db'
    sessions = root / 'sessions'
    sessions.mkdir()
    transcript = sessions / 'session.jsonl'
    transcript.write_text(json.dumps({'type': 'user', 'uuid': 'first', 'sessionId': 'watched',
        'timestamp': '2026-11-01T00:00:00Z', 'message': {'content': 'Durably watched message'}}) + '\n')
    config = root / 'config.json'
    config.write_text(json.dumps({'watch': [{'dir': str(sessions), 'source': 'claude'}], 'watch_interval': '50ms'}))
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    base = f'http://127.0.0.1:{port}'
    process = None
    log = (root / 'process.log').open('w+')

    def start(watch=False):
        arguments = [BINARY, '--listen', f'127.0.0.1:{port}', '--db', str(database)]
        if watch:
            arguments += ['--config', str(config)]
        return subprocess.Popen(arguments, env={**os.environ, 'AGENTLOG_TOKEN': TOKEN}, stdout=log, stderr=log)

    def api(path, data=None):
        # Deliberately no test_support waiting: measure the real async contract.
        request = urllib.request.Request(base + path, None if data is None else json.dumps(data).encode(),
            headers={'Authorization': 'Bearer ' + TOKEN, 'Content-Type': 'application/json'})
        with urllib.request.urlopen(request, timeout=2) as response:
            return json.load(response)

    def wait(check):
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            if process.poll() is not None:
                raise AssertionError('collector exited')
            try:
                if check():
                    return
            except OSError:
                pass
            time.sleep(.01)
        raise AssertionError('collector did not reach expected state')

    def settled():
        state = api('/api/status')
        assert state['state'] != 'failed', state
        return state['state'] == 'ready' and state['pending_batches'] == 0

    def payload(session):
        return {'resourceLogs': [{'resource': {'attributes': [{'key': 'service.name', 'value': {'stringValue': 'codex'}}]},
            'scopeLogs': [{'logRecords': [{'eventName': 'user_prompt', 'timeUnixNano': '1790000000000000000',
                'attributes': [{'key': 'session.id', 'value': {'stringValue': session}},
                               {'key': 'prompt', 'value': {'stringValue': 'Durable OTLP message'}}]}]}]}]}

    lock = None
    try:
        process = start()
        wait(settled)
        process.terminate()
        assert process.wait(timeout=10) == 0
        # Main-database migration cannot acquire its writer. The independent
        # inbox and watch state must still accept and commit data.
        lock = sqlite3.connect(database)
        lock.execute('BEGIN IMMEDIATE')
        process = start(watch=True)
        wait(lambda: api('/api/status')['state'] == 'migrating')
        begin = time.monotonic()
        assert api('/v1/logs', payload('queued')) == {}
        assert time.monotonic() - begin < 1
        def checkpointed():
            with sqlite3.connect(inbox) as connection:
                return connection.execute('SELECT COUNT(*) FROM watch_files').fetchone()[0] == 1
        wait(checkpointed)
        assert api('/api/status')['pending_batches'] == 2
        process.kill()
        process.wait(timeout=5)
        with sqlite3.connect(inbox) as connection:
            assert connection.execute('SELECT COUNT(*) FROM ingest_batches').fetchone()[0] == 2
            last_id = connection.execute("SELECT seq FROM sqlite_sequence WHERE name='ingest_batches'").fetchone()[0]
        lock.rollback()
        lock.close()
        lock = None
        process = start(watch=True)
        wait(settled)
        assert api('/api/stats') == {'events': 2, 'sessions': 2}
        # Unchanged watched files produce no new batch even after SIGKILL.
        time.sleep(.15)
        with sqlite3.connect(inbox) as connection:
            assert connection.execute("SELECT seq FROM sqlite_sequence WHERE name='ingest_batches'").fetchone()[0] == last_id
        # Existing read views and new durable ACKs stay available while the
        # main writer is busy after initialization as well.
        lock = sqlite3.connect(database)
        lock.execute('BEGIN IMMEDIATE')
        begin = time.monotonic()
        assert api('/v1/logs', payload('later')) == {}
        assert len(api('/api/sessions')) == 2
        assert time.monotonic() - begin < 1
        assert api('/api/status')['pending_batches'] == 1
        lock.rollback()
        lock.close()
        lock = None
        wait(settled)
        assert api('/api/stats') == {'events': 3, 'sessions': 3}
        process.terminate()
        assert process.wait(timeout=10) == 0
    except BaseException:
        log.flush()
        log.seek(0)
        print(log.read())
        raise
    finally:
        if lock is not None:
            lock.close()
        if process is not None and process.poll() is None:
            process.kill()
            process.wait(timeout=5)
        log.close()
print('PASS: OTLP/watch ACK during blocked migrations, SIGKILL recovery, persistent unchanged-file skip, concurrent browsing/ACK with a busy writer, graceful shutdown')
