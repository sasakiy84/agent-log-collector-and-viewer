#!/usr/bin/env python3
"""Skewed large-session and 13k-session navigation; synthetic data only."""
import argparse
import json
import pathlib
import sqlite3
import statistics
import tempfile
import time

from session_performance_test import SESSION, api, fixture, server, browser_check


def measure(binary, database, label, browser):
    expected = {}
    with server(binary, database) as base:
        for path in ['/api/sessions?offset=0', '/api/sessions?project=name:large',
                     *['/api/session?id=' + SESSION + '&' + query for query in (
                         'view=conversation&position=latest', 'view=conversation&offset=0',
                         'view=conversation&position=latest&origin=otel',
                         'view=conversation&position=latest&origin=import')]]:
            samples = []
            for _ in range(4):
                started = time.perf_counter()
                result = api(base, path)
                samples.append((time.perf_counter() - started) * 1000)
            expected[path] = result
            print(label, path, 'first_ms', round(samples[0], 2),
                  'median_ms', round(statistics.median(samples[1:]), 2), flush=True)
        result = expected['/api/session?id=' + SESSION + '&view=conversation&position=latest']
        assert result['count'] == 858 and len(result['events']) == 58
        assert sum(group['count'] for group in result['processing']) > result['total_count'] * .6
        if browser:
            browser_check(base)
    return expected


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True)
    parser.add_argument('--baseline')
    parser.add_argument('--records', type=int, default=650962)
    parser.add_argument('--sessions', type=int, default=13085)
    parser.add_argument('--browser', action='store_true')
    args = parser.parse_args()
    assert args.records >= 5000 and args.sessions >= 1
    with tempfile.TemporaryDirectory(prefix='agentlog-navigation-perf-') as directory:
        database = pathlib.Path(directory) / 'fixture.db'
        fixture(args.baseline or args.binary, database, args.records, skewed=True)
        with sqlite3.connect(database) as db:
            preview = json.dumps({'name':'codex.user_prompt','kind':'user','title':'User',
                                  'blocks':[{'label':'Request','text':'Synthetic question ' + 'x' * 700}],
                                  'fields':[],'attributes':{}})
            db.executemany('''INSERT INTO sessions(session,source,parent,count,start,end,
 import_count,import_start,import_end,otel_count,otel_start,otel_end,preview)
 VALUES(?,'codex','',1,'2026-11-01T00:00:00.000000000Z','2026-11-01T00:00:00.000000000Z',
 0,'','',1,'2026-11-01T00:00:00.000000000Z','2026-11-01T00:00:00.000000000Z',?)''',
                           ((f'codex:other-{i:08d}',preview) for i in range(args.sessions-1)))
        # Upgrade the same database in place: no extra multi-GB fixture copies.
        before = measure(args.baseline, database, 'baseline', args.browser) if args.baseline else None
        after = measure(args.binary, database, 'current', args.browser)
        if before is not None:
            assert before == after, 'Navigation results changed during optimization'
        with sqlite3.connect(database) as db:
            print('summary_rows', db.execute('SELECT COUNT(*) FROM processing_buckets').fetchone()[0], flush=True)
    print('PASS: skewed 858-message session, collapsed counts/origins, list ordering and response equality', flush=True)


if __name__ == '__main__':
    main()
