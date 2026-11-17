#!/usr/bin/env python3
"""Compare large, already-indexed session reads; never uses a user's database."""
import argparse
from contextlib import contextmanager
import json
import os
from pathlib import Path
import socket
import sqlite3
import statistics
import subprocess
import tempfile
import time
import urllib.parse
import urllib.request

SESSION = 'codex:large-session'


def api(base, path):
    with urllib.request.urlopen(base + path, timeout=30) as response:
        return json.load(response)


@contextmanager
def server(binary, database):
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    base = f'http://127.0.0.1:{port}'
    with database.with_suffix('.log').open('w+') as log:
        process = subprocess.Popen([str(Path(binary).resolve()), '--db', str(database),
                                    '--listen', f'127.0.0.1:{port}'],
                                   env={**os.environ, 'AGENTLOG_TOKEN': ''}, stdout=log, stderr=log)
        try:
            deadline = time.monotonic() + 60
            while time.monotonic() < deadline:
                if process.poll() is not None:
                    raise RuntimeError('Collector exited; inspect ' + str(log.name))
                try:
                    state = api(base, '/api/status')
                    if state['state'] == 'failed':
                        raise RuntimeError('Database preparation failed; inspect ' + str(log.name))
                    if state['state'] == 'ready' and state['pending_batches'] == 0:
                        break
                except OSError:
                    pass
                time.sleep(.02)
            else:
                raise TimeoutError('Collector preparation exceeded one minute')
            yield base
        finally:
            process.terminate()
            process.wait(timeout=15)


def fixture(binary, database, records, skewed=False):
    with server(binary, database) as base:
        while not api(base, '/api/skills')['ready']:
            time.sleep(.01)
    # Seed consistent prepared rows, not parser/backfill work. This benchmark
    # measures the read path independently of historical import throughput.
    with sqlite3.connect(database) as db:
        triggers = db.execute("SELECT name,sql FROM sqlite_master WHERE type='trigger'").fetchall()
        for name, _ in triggers:
            db.execute('DROP TRIGGER "' + name + '"')
        values = {'records': records, 'session': SESSION}
        # A long-lived real session can have most logs between its last few
        # messages. Uniform placement alone hides the cost of collapsed ranges.
        if skewed:
            counts = [15,10,52,21,27,30,18,45,30,15,31,15,12,12,12,12,15,28,48,19,
                      27,21,23,15,24,15,39,28,15,131,199,101,1498,465,371,132,1471,
                      115,2653,12,2030,263,1804,9,203,24,374,57,133,350,9,
                      153046,3307,2708,35572,39606,67162,104122]
            counts = [round(n * records / 650962) for n in counts]
            prefix = records - sum(counts) - len(counts)
            positions = [i * (prefix - 1) // 799 for i in range(800)]
            cursor = prefix
            for count in counts:
                cursor += count
                positions.append(cursor)
                cursor += 1
            db.execute('CREATE TEMP TABLE conversation_positions(position INTEGER PRIMARY KEY,number INTEGER)')
            db.executemany('INSERT INTO conversation_positions VALUES(?,?)', ((position, i) for i, position in enumerate(positions)))
            conversation = 'p.position IS NOT NULL'
            kind = "CASE WHEN p.position IS NULL THEN 'event' WHEN p.number%2=0 THEN 'user' ELSE 'assistant' END"
            seconds = f'CASE WHEN i<{prefix} THEN i*518400.0/{prefix} ELSE 518400.0+(i-{prefix})*61200.0/{records-prefix} END'
            join = 'LEFT JOIN conversation_positions p ON p.position=n.i'
        else:
            conversation = 'i%1000 IN(0,999) OR i=:records-1'
            kind = "CASE WHEN i%1000=0 THEN 'user' WHEN i%1000=999 OR i=:records-1 THEN 'assistant' ELSE 'event' END"
            seconds, join = 'i/1000.0', ''
        base_date = "2026-11-10" if skewed else "2026-11-17"
        db.execute(f"""CREATE TEMP TABLE fixture AS
 WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM n WHERE i+1<:records)
 SELECT CASE WHEN i%3=0 THEN 'import:' ELSE '' END||printf('%064x',i) AS id,
 strftime('%Y-%m-%dT%H:%M:%f','{base_date}',printf('+%f seconds',{seconds}))||'000000Z' AS time,
 i AS sequence,CASE WHEN i%3=0 THEN 'import' ELSE 'otel' END AS origin,
 {conversation} AS conversation, {kind} AS kind,
 i%31<>0 AS activity,i%17003=0 AND NOT ({conversation}) AS is_error FROM n {join}""", values)
        db.execute("""INSERT INTO events(id,session,parent,source,signal,time,name,trace,span,parent_span,raw,sequence,origin,kind,tool_name)
 SELECT id,:session,'','codex','logs',time,kind,'','','',
 CAST(json_object('resource',json_object('attributes',json_array(
 json_object('key','service.name','value',json_object('stringValue','codex')),
 json_object('key','agentlog.origin','value',json_object('stringValue',origin)))),
 'record',json_object('eventName',kind,'body',json_object('stringValue','Synthetic event'))) AS BLOB),
 sequence,origin,kind,'' FROM fixture""", values)
        metadata = {'name': 'receiving', 'kind': 'event', 'title': 'Runtime metadata',
                    'blocks': [{'label': 'Details', 'text': 'Synthetic runtime metadata ' + 'x' * 400}],
                    'fields': [], 'attributes': {}, 'cwd': '/work/large'}
        values['metadata'] = json.dumps(metadata)
        db.execute("""INSERT INTO event_cards(id,session,origin,source,parent,time,sequence,is_user,signal,trace,span,parent_span,
 detail,prompt_title,activity,kind,is_error,turn,presentation_ready,conversation,has_skill)
 SELECT id,:session,origin,'codex','',time,sequence,kind='user','logs','','','',
 CAST(CASE WHEN conversation=0 THEN :metadata ELSE json_object('name',kind,'kind',kind,'title',kind,
 'blocks',json_array(json_object('label',CASE WHEN kind='user' THEN 'Request' ELSE 'Response' END,
 'text','Synthetic conversation '||sequence)), 'fields',json('[]'),'attributes',json('{}'),'cwd','/work/large') END AS BLOB),
 '',activity,kind,is_error,'',1,conversation,0 FROM fixture""", values)
        db.execute("""INSERT INTO timeline_entries(id,session,source,parent,time,end,sequence,is_user,import_id,otel_id,
 activity,kind,is_error,conversation,has_skill)
 SELECT id,:session,'codex','',time,time,sequence,kind='user',CASE WHEN origin='import' THEN id ELSE '' END,
 CASE WHEN origin='otel' THEN id ELSE '' END,activity,kind,is_error,conversation,0 FROM fixture""", values)
        db.execute('INSERT INTO timeline_counts SELECT :session,COUNT(*) FROM fixture', values)
        db.execute("INSERT INTO card_counts SELECT :session,origin,'codex','',COUNT(*) FROM fixture GROUP BY origin", values)
        db.execute("""INSERT INTO session_catalog(session,source,parent,count,start,end,import_count,import_start,import_end,
 otel_count,otel_start,otel_end,preview)
 SELECT :session,'codex','',COUNT(*),MIN(time),MAX(time),SUM(origin='import'),MIN(time),MAX(time),
 SUM(origin='otel'),MIN(time),MAX(time),:metadata FROM fixture""", values)
        db.execute('INSERT INTO folder_dirty VALUES(?)', (SESSION,))
        if db.execute("SELECT 1 FROM sqlite_master WHERE name='processing_buckets'").fetchone():
            db.execute('DELETE FROM processing_buckets')
            db.execute("UPDATE maintenance_jobs SET cursor='',complete=0,processed=0 WHERE name IN('processing.cards.v1','processing.timeline.v1')")
        for _, sql in triggers:
            db.execute(sql)


def browser_check(base):
    from playwright.sync_api import sync_playwright
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(executable_path='/usr/bin/chromium', headless=True,
                                             args=['--no-sandbox'])
        try:
            page = browser.new_page(viewport={'width': 1440, 'height': 1000})
            requests = []
            errors = []
            page.on('request', lambda request: requests.append(request.url))
            page.on('pageerror', lambda error: errors.append(str(error)))
            started = time.perf_counter()
            page.goto(base + '/sessions/' + urllib.parse.quote(SESSION, safe=''))
            page.wait_for_selector('#timeline .event')
            page.wait_for_function("document.querySelector('#workspace').getAttribute('aria-busy') !== 'true'")
            print('browser_initial_ms', round((time.perf_counter() - started) * 1000, 2), flush=True)
            assert len(page.locator('#timeline .event').all()) <= 200
            assert not any('/api/processing?' in request for request in requests)
            assert not any('/api/event?' in request for request in requests)
            assert page.evaluate('scrollY > 0'), 'latest conversation was not revealed'
            assert not errors, errors
        finally:
            browser.close()


def measure(binary, source, directory, label, browser):
    database = directory / (label + '.db')
    with sqlite3.connect(source) as src, sqlite3.connect(database) as dst:
        src.backup(dst)
    signatures = {}
    with server(binary, database) as base:
        queries = ['view=conversation&position=latest', 'view=conversation&offset=0',
                   'view=conversation&position=latest&origin=otel',
                   'view=conversation&position=latest&origin=import',
                   'view=diagnostics&position=latest']
        for query in queries:
            samples = []
            for _ in range(6):
                started = time.perf_counter()
                result = api(base, '/api/session?id=' + SESSION + '&' + query)
                samples.append((time.perf_counter() - started) * 1000)
            signatures[query] = result
            assert len(result['events']) <= 201
            assert all(not record.get('raw') for event in result['events'] for record in event['records'])
            print(label, query, 'first_ms', round(samples[0], 2),
                  'median_ms', round(statistics.median(samples[1:]), 2), flush=True)
        group = next(group for group in signatures[queries[0]]['processing'] if group['count'] > 50)
        params = urllib.parse.urlencode({'session': SESSION, 'after': group['after'],
                                        'before': group['before'], 'diagnostics': '1'})
        signatures['processing'] = api(base, '/api/processing?' + params)
        assert len(signatures['processing']['events']) == 50
        if browser:
            browser_check(base)
    return signatures


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', default=os.getenv('AGENTLOG_BIN', './agentlog'))
    parser.add_argument('--baseline')
    parser.add_argument('--records', type=int, default=277022)
    parser.add_argument('--browser', action='store_true')
    args = parser.parse_args()
    if args.records < 2000:
        parser.error('--records must be at least 2000')
    with tempfile.TemporaryDirectory(prefix='agentlog-session-perf-') as temporary:
        directory = Path(temporary)
        source = directory / 'fixture.db'
        fixture(args.baseline or args.binary, source, args.records)
        previous = measure(args.baseline, source, directory, 'baseline', args.browser) if args.baseline else None
        current = measure(args.binary, source, directory, 'current', args.browser)
        if previous is not None:
            assert previous == current, 'Events, groups, counts, origins or ordering changed'
        print('PASS: large session reads and lazy processing; complete responses match baseline'
              if previous is not None else 'PASS: large session reads and lazy processing', flush=True)


if __name__ == '__main__':
    main()
