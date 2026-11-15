#!/usr/bin/env python3
import json, os, socket, sqlite3, statistics, subprocess, tempfile, time, urllib.request
import argparse
parser = argparse.ArgumentParser(description='Compare ready-database startup and unrelated ingestion with retained internal traces. Synthetic data only.')
parser.add_argument('--baseline', required=True)
parser.add_argument('--binary', default=os.getenv('AGENTLOG_BIN', './agentlog'))
parser.add_argument('--pending-traces', type=int, default=10000)
args = parser.parse_args()
if args.pending_traces < 1:
    parser.error('--pending-traces must be positive')
OLD = str(__import__('pathlib').Path(args.baseline).resolve())
NEW = str(__import__('pathlib').Path(args.binary).resolve())
TOKEN = 'synthetic-lifecycle-benchmark-token'

def start(binary, db):
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        port = s.getsockname()[1]
    base = f'http://127.0.0.1:{port}'
    begin = time.perf_counter()
    proc = subprocess.Popen([binary, '--listen', f'127.0.0.1:{port}', '--db', db], env={**os.environ, 'AGENTLOG_TOKEN': TOKEN}, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    for _ in range(2000):
        try:
            call(base, '/api/stats')
            return (proc, base, time.perf_counter() - begin)
        except OSError:
            if proc.poll() is not None:
                raise RuntimeError('server exited')
            time.sleep(0.002)
    proc.terminate()
    proc.wait(timeout=20)
    raise RuntimeError('startup timed out')

def call(base, path, body=None):
    req = urllib.request.Request(base + path, None if body is None else json.dumps(body).encode(), headers={'Authorization': 'Bearer ' + TOKEN, 'Content-Type': 'application/json'})
    with urllib.request.urlopen(req, timeout=60) as response:
        return json.load(response)

def logbody(i):
    return {'resourceLogs': [{'resource': {'attributes': [{'key': 'service.name', 'value': {'stringValue': 'codex'}}]}, 'scopeLogs': [{'logRecords': [{'eventName': 'gen_ai.user.message', 'timeUnixNano': str(1780000000000000000 + i * 1000000000), 'attributes': [{'key': 'session.id', 'value': {'stringValue': 'normal'}}, {'key': 'cwd', 'value': {'stringValue': '/work/benchmark'}}, {'key': 'prompt', 'value': {'stringValue': f'Synthetic append {i}'}}]}]}]}]}
with tempfile.TemporaryDirectory() as folder:
    fixture = folder + '/fixture.db'
    proc, base, _ = start(OLD, fixture)
    try:
        for batch in range(0, args.pending_traces, 1000):
            spans = [{'name': 'fs.read_file', 'traceId': f'{i + 1:032x}', 'spanId': f'{i + 1:016x}', 'startTimeUnixNano': '1780000000000000000', 'endTimeUnixNano': '1780000000000000100'} for i in range(batch, min(batch + 1000, args.pending_traces))]
            call(base, '/v1/traces', {'resourceSpans': [{'resource': {'attributes': [{'key': 'service.name', 'value': {'stringValue': 'codex'}}]}, 'scopeSpans': [{'spans': spans}]}]})
        call(base, '/v1/logs', logbody(0))
    finally:
        proc.terminate()
        proc.wait(timeout=20)
    for label, binary in [('baseline', OLD), ('current', NEW)]:
        db = folder + '/' + label + '.db'
        with sqlite3.connect(fixture) as src, sqlite3.connect(db) as dst:
            src.backup(dst)
        first = None
        samples = []
        for repeat in range(4):
            proc, base, seconds = start(binary, db)
            try:
                if first is None:
                    first = seconds
                else:
                    samples.append(seconds)
                if repeat == 3:
                    append = []
                    for i in range(1, 41):
                        began = time.perf_counter()
                        call(base, '/v1/logs', logbody(i))
                        append.append((time.perf_counter() - began) * 1000)
                    stats = call(base, '/api/stats')
                    assert stats == {'events': args.pending_traces + 41, 'sessions': args.pending_traces + 1}, stats
                    print(label, 'first_upgrade_ms', round(first * 1000, 2), 'warm_start_median_ms', round(statistics.median(samples) * 1000, 2), 'unrelated_append_median_ms', round(statistics.median(append), 2), flush=True)
            finally:
                proc.terminate()
                proc.wait(timeout=20)
