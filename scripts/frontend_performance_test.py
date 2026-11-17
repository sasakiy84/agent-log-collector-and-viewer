#!/usr/bin/env python3
"""Measure browser work separately from session size and real HTTP latency."""
import argparse
import json
from pathlib import Path
import statistics
import tempfile
import urllib.parse

from playwright.sync_api import expect, sync_playwright
from session_performance_test import SESSION, fixture, server

STAMP = '2026-11-17T00:00:00.000000000Z'
TEXT = ('ユーザーの入力とモデルの回答を読みやすく表示します。'
        '処理ログは必要な時に開いて確認できます。' * 20)[:600]
TOTALS = (2000, 20000, 277022)

# Test-only wrappers: production CSP and source assets remain unchanged.
INIT = 'window.__front = {boundCards: []};'
WRAP = """
const originalRenderProbe = renderEvents;
renderEvents = function(...args) {
  const start = performance.now();
  const result = originalRenderProbe.apply(this, args);
  window.__front.renderMS = performance.now() - start;
  requestAnimationFrame(() => requestAnimationFrame(() => {
    window.__front.afterFrames = performance.now();
    document.documentElement.dataset.frontendProbeReady = '1';
  }));
  return result;
};
const originalBindProbe = bindEventCards;
bindEventCards = function(root) {
  window.__front.boundCards.push(root.querySelectorAll('[data-event]').length);
  return originalBindProbe(root);
};
"""


def event(index, kind='assistant'):
    ident = f'{index:064x}'
    detail = {'name': 'codex.' + kind, 'kind': kind, 'title': kind.title(),
              'blocks': [{'label': 'Request' if kind == 'user' else 'Response',
                          'text': TEXT, 'code': False}],
              'fields': [], 'attributes': {}}
    return {'id': ident, 'session': SESSION, 'source': 'codex', 'time': STAMP,
            'signal': 'logs', 'detail': detail, 'origins': ['otel'],
            'records': [{'id': ident, 'session': SESSION, 'source': 'codex',
                         'origin': 'otel', 'name': detail['name'], 'time': STAMP, 'signal': 'logs'}]}


def payload(total):
    events = [event(400 + i, 'user' if i % 2 == 0 else 'assistant') for i in range(200)]
    count, remainder = divmod(total - 600, 201)
    groups = [{'after': f'{399 + i:064x}', 'before': events[i]['id'] if i < 200 else '',
               'count': count + (i < remainder), 'activity': max(1, count // 2),
               'errors': int(i % 23 == 0)} for i in range(201)]
    return {'id': SESSION, 'source': 'codex', 'parent': '', 'count': 600,
            'total_count': total, 'start': STAMP, 'end': STAMP,
            'preview': events[0]['detail'], 'usage': {'sessions': 1, 'values': {}, 'coverage': {}},
            'offset': 400, 'limit': 200, 'children': [], 'events': events,
            'processing': groups, 'skills': [], 'view': 'conversation'}


def serve_app(route):
    response = route.fetch()
    route.fulfill(response=response, body=response.text() + WRAP)


def open_session(browser, base, body=None):
    page = browser.new_page(viewport={'width': 1440, 'height': 1000},
                            locale='ja-JP', timezone_id='Asia/Tokyo')
    errors, requests = [], []
    page.on('pageerror', lambda error: errors.append(str(error)))
    page.on('request', lambda request: requests.append(request.url))
    page.add_init_script(INIT)
    page.route('**/app.js', serve_app)
    if body is not None:
        page.route('**/api/session?*', lambda route: route.fulfill(json=body))
    page.goto(base + '/sessions/' + urllib.parse.quote(SESSION, safe=''))
    page.wait_for_selector('html[data-frontend-probe-ready="1"]', state='attached')
    result = page.evaluate("""() => {
      const m = window.__front;
      const r = performance.getEntriesByType('resource').find(e => e.name.includes('/api/session?'));
      return {render_ms: m.renderMS, response_to_frames_ms: m.afterFrames - r.responseEnd,
        session_http_ms: r.responseEnd - r.startTime,
        nodes: document.querySelectorAll('*').length,
        cards: document.querySelectorAll('#timeline .event').length};
    }""")
    assert result['cards'] <= 200
    assert not any('/api/processing?' in url or '/api/event?' in url for url in requests)
    expect(page.locator('#timeline > .event').last).to_be_in_viewport()
    assert not errors, errors
    return page, result, errors


def summarize(label, results):
    medians = {key: round(statistics.median(row[key] for row in results), 2)
               for key in results[0]}
    print(label, json.dumps(medians), flush=True)


def append_check(page):
    # Twenty explicit pages: old cards must not be initialized on every append.
    def processing(route):
        params = urllib.parse.parse_qs(urllib.parse.urlsplit(route.request.url).query)
        offset = int(params.get('cursor', ['0'])[0])
        route.fulfill(json={'events': [event(i, 'tool') for i in range(1000 + offset, 1050 + offset)],
                            'next': str(offset + 50) if offset < 950 else ''})

    page.route('**/api/processing?*', processing)
    page.evaluate('window.__front.boundCards = []')
    group = page.locator('.processing-group').last
    group.locator('summary').click()
    for index in range(20):
        if index:
            group.locator('.processing-more').click()
        expect(group.locator('.event')).to_have_count((index + 1) * 50)
        expect(group.locator('.processing-events')).to_have_attribute('aria-busy', 'false')
    expect(group.locator('.processing-more')).to_be_hidden()
    counts = page.evaluate('window.__front.boundCards')
    print('processing_append', json.dumps({'pages': len(counts), 'bound_cards': sum(counts),
                                          'largest_bind': max(counts)}), flush=True)
    assert counts == [50] * 20, counts


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', default='./agentlog')
    parser.add_argument('--repeats', type=int, default=5)
    parser.add_argument('--real-api', action='store_true', help='also seed/read real 2k/20k/277k databases')
    args = parser.parse_args()
    if args.repeats < 1:
        parser.error('--repeats must be positive')
    with tempfile.TemporaryDirectory(prefix='agentlog-frontend-perf-') as temporary, sync_playwright() as pw:
        directory = Path(temporary)
        browser = pw.chromium.launch(executable_path='/usr/bin/chromium', headless=True,
                                     args=['--no-sandbox'])
        try:
            with server(args.binary, directory / 'empty.db') as base:
                results = {total: [] for total in TOTALS}
                for repeat in range(args.repeats):
                    for total in TOTALS if repeat % 2 == 0 else reversed(TOTALS):
                        page, row, errors = open_session(browser, base, payload(total))
                        assert row['cards'] == 200
                        results[total].append(row)
                        if repeat == 0 and total == TOTALS[-1]:
                            append_check(page)
                            assert not errors, errors
                        page.close()
                for total, rows in results.items():
                    summarize(f'controlled_frontend total={total}', rows)
                assert len({row['nodes'] for rows in results.values() for row in rows}) == 1
            if args.real_api:
                for total in TOTALS:
                    database = directory / f'real-{total}.db'
                    fixture(args.binary, database, total)
                    with server(args.binary, database) as base:
                        rows = []
                        for _ in range(args.repeats):
                            page, row, _ = open_session(browser, base)
                            rows.append(row)
                            page.close()
                        summarize(f'real_api total={total}', rows)
        finally:
            browser.close()
    print('PASS: bounded initial DOM, lazy requests and page-local append binding', flush=True)


if __name__ == '__main__':
    main()
