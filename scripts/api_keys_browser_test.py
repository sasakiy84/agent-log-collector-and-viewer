#!/usr/bin/env python3
"""Owner Settings, recoverable read keys, agent curl discovery and layout."""
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.request
from playwright.sync_api import sync_playwright, expect

binary = os.getenv('AGENTLOG_BIN', './agentlog')
token = 'synthetic-settings-browser-token'
with tempfile.TemporaryDirectory(prefix='agentlog-api-keys-') as directory:
    with socket.socket() as sock:
        sock.bind(('127.0.0.1',0))
        port = sock.getsockname()[1]
    base = f'http://127.0.0.1:{port}'
    process = subprocess.Popen([binary,'--db',directory+'/test.db','--listen',f'127.0.0.1:{port}'],
                               env={**os.environ,'AGENTLOG_TOKEN':token},stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    try:
        for _ in range(200):
            try:
                with urllib.request.urlopen(base) as response:
                    break
            except OSError:
                time.sleep(.05)
        with sync_playwright() as pw:
            browser = pw.chromium.launch(executable_path=os.getenv('AGENTLOG_CHROMIUM', '/usr/bin/chromium'),headless=True,args=['--no-sandbox'])
            context = browser.new_context(viewport={'width':1440,'height':1000},locale='ja-JP',permissions=['clipboard-read','clipboard-write'])
            page = context.new_page()
            errors, requests = [], []
            page.on('pageerror',lambda error:errors.append(str(error)))
            page.on('request',lambda request:requests.append(request.url))
            page.goto(base+'/settings')
            expect(page.locator('#login')).to_be_visible()
            page.locator('#token').fill(token)
            page.locator('#loginform button').click()
            expect(page.locator('#settingspage')).to_be_visible()
            expect(page).to_have_title('Settings · AGrahpite')
            expect(page.locator('#settingsnav')).to_have_attribute('aria-current','page')
            expect(page.locator('#apikeycount')).to_have_text('0 keys')
            expect(page.locator('#serverversion')).to_contain_text('Version ')
            assert not any('/api/projects' in url or '/api/sessions?' in url for url in requests)
            name = 'Agent <b>demo</b>'
            page.locator('#apikeyname').fill(name)
            page.locator('#createapikey button').click()
            expect(page.locator('.api-key-row')).to_have_count(1)
            expect(page.locator('.api-key-secret input')).to_have_attribute('type','password')
            assert page.locator('#apikeyrows b').count()==0
            owner = page.request.get(base+'/api/settings/api-keys').json()['keys'][0]
            key = owner['key']
            page.locator('[data-action=reveal]').click()
            expect(page.locator('.api-key-secret input')).to_have_attribute('type','text')
            expect(page.locator('.api-key-secret input')).to_have_value(key)
            page.locator('[data-action=reveal]').click()
            page.locator('[data-action=copy]').click()
            expect(page.locator('#settingsstatus')).to_have_text('Key copied')
            assert page.evaluate('navigator.clipboard.readText()')==key
            page.locator('#copyreadmecurl').click()
            expect(page.locator('#settingsstatus')).to_have_text('Command copied')
            command = page.evaluate('navigator.clipboard.readText()')
            assert '<API_KEY>' in command and base+'/api/readme' in command and key not in command
            result = subprocess.run(['curl','-fsS','-H','Authorization: Bearer '+key,base+'/api/readme'],capture_output=True,text=True,check=True)
            assert '# AGrahpite API' in result.stdout and '/api/metrics' in result.stdout
            metrics = page.request.get(base+'/api/metrics',headers={'Authorization':'Bearer '+key})
            assert metrics.status==200 and metrics.json()['events']==0
            assert page.request.get(base+'/api/settings/api-keys',headers={'Authorization':'Bearer '+key}).status==403
            page.locator('.api-key-name input').fill('分析エージェント')
            page.locator('.api-key-name button').click()
            expect(page.locator('#settingsstatus')).to_have_text('Name saved')
            page.reload()
            expect(page.locator('.api-key-name input')).to_have_value('分析エージェント')
            assert page.request.get(base+'/api/settings/api-keys').json()['keys'][0]['key']==key
            output = Path(os.getenv('AGENTLOG_SCREENSHOT_DIR','/tmp/agentlog-settings-shots'))
            output.mkdir(parents=True,exist_ok=True)
            for width in (1440,768,390,360):
                page.set_viewport_size({'width':width,'height':900})
                page.evaluate('document.activeElement?.blur()')
                assert page.evaluate('document.documentElement.scrollWidth<=innerWidth'),width
                page.screenshot(path=str(output/f'settings-{width}.png'),full_page=True)
            page.once('dialog',lambda dialog:dialog.accept())
            page.locator('[data-action=delete]').click()
            expect(page.locator('#apikeycount')).to_have_text('0 keys')
            assert page.request.get(base+'/api/readme',headers={'Authorization':'Bearer '+key}).status==401
            page.locator('#sessionsnav').click()
            expect(page.locator('#listpage')).to_be_visible()
            page.locator('#settingsnav').click()
            expect(page.locator('#settingspage')).to_be_visible()
            page.locator('#logout').click()
            expect(page.locator('#login')).to_be_visible()
            assert not errors,errors
            browser.close()
    finally:
        process.terminate()
        process.wait(timeout=20)
print('PASS: Settings create/rename/reveal/copy/delete, placeholder curl discovery, read-only keys, reload persistence, owner login/logout and four widths')
