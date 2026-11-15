"""Wait explicitly for derived data in synchronous integration-test requests.

Production OTLP success means durable acceptance, not completion of indexing.
Tests of queue latency/durability must use urllib.request.urlopen directly.
"""
import json
import time
import urllib.error
import urllib.parse
import urllib.request

_ready = set()


def wait_for_ingestion(base, token, timeout=120):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        request = urllib.request.Request(base + '/api/status', headers={'Authorization': 'Bearer ' + token})
        try:
            with urllib.request.urlopen(request, timeout=min(5, max(.1, deadline-time.monotonic()))) as response:
                state = json.load(response)
        except urllib.error.HTTPError as error:
            if error.code == 404:  # Published baseline binaries predate the inbox.
                _ready.add((base, token))
                return
            raise
        if state['state'] == 'failed':
            raise RuntimeError('Collector preparation failed; inspect the collector log')
        if state['state'] == 'ready' and state['pending_batches'] == 0:
            _ready.add((base, token))
            return
        time.sleep(.01)
    raise TimeoutError('Collector has not finished preparation/indexing')


def settled_urlopen(request, *args, **kwargs):
    """Preserve urllib's response API, waiting for views at startup/after OTLP."""
    if not isinstance(request, urllib.request.Request):
        return urllib.request.urlopen(request, *args, **kwargs)
    parsed = urllib.parse.urlsplit(request.full_url)
    base = urllib.parse.urlunsplit((parsed.scheme, parsed.netloc, '', '', ''))
    authorization = request.get_header('Authorization', '')
    token = authorization.removeprefix('Bearer ')
    timeout = kwargs.get('timeout', 120)
    if parsed.path.startswith('/api/') and parsed.path not in ('/api/status', '/api/login', '/api/logout') and (base, token) not in _ready:
        wait_for_ingestion(base, token, timeout)
    response = urllib.request.urlopen(request, *args, **kwargs)
    if request.get_method() == 'POST' and parsed.path in ('/v1/logs', '/v1/traces', '/v1/metrics'):
        try:
            wait_for_ingestion(base, token, timeout)
        except BaseException:
            response.close()
            raise
    return response
