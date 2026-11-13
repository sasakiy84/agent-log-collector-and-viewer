#!/usr/bin/env python3
"""Requires a running server with scripts/demo.py fixtures, and Playwright + Chromium."""
import json, os, pathlib, urllib.request
from playwright.sync_api import sync_playwright
BASE=os.getenv('AGENTLOG_URL','http://127.0.0.1:4318')
TOKEN=os.environ['AGENTLOG_TOKEN']
OUT=pathlib.Path(__file__).resolve().parents[1]/'docs'
OUT.mkdir(exist_ok=True)
with sync_playwright() as p:
 browser=p.chromium.launch(executable_path='/usr/bin/chromium',headless=True,args=['--no-sandbox'])
 page=browser.new_page(viewport={'width':1440,'height':1100},device_scale_factor=1)
 errors=[]
 page.on('pageerror',lambda e:errors.append(str(e)))
 page.goto(BASE)
 page.locator('#token').fill('invalid')
 page.locator('#loginform button').click()
 page.get_by_text('Invalid access token.').wait_for()
 page.locator('#token').fill(TOKEN)
 page.locator('#loginform button').click()
 page.locator('.session').first.wait_for()
 page.locator('[data-source="codex"]').click()
 page.wait_for_function("() => [...document.querySelectorAll('.session .badge')].every(x=>x.textContent==='Codex CLI')")
 page.locator('[data-id="codex:refactor-auth-middleware"]').click()
 page.locator('.event').first.wait_for()
 page.wait_for_function("() => document.querySelector('#detailhead h2').textContent==='codex:refactor-auth-middleware'")
 page.locator('.event details').first.locator('summary').click()
 page.wait_for_timeout(5500)
 assert page.locator('.event details').first.get_attribute('open') is not None,'refresh closed raw record'
 with page.expect_download() as d:page.get_by_text('Export JSON →').click()
 data=json.load(open(d.value.path()))
 assert len(data)>=8 and data[0]['source']=='codex'
 page.locator('[data-source=""]').click()
 page.locator('#search').fill('refactor')
 page.wait_for_function("() => document.querySelectorAll('.session').length===1")
 page.locator('#search').fill('no-match-xyz')
 page.get_by_text('No sessions yet. Send OTLP data to get started.').wait_for()
 page.locator('#search').fill('')
 page.wait_for_function("() => document.querySelectorAll('.session').length===10")
 page.locator('[data-id="claude:review-api-boundaries"]').click()
 page.locator('[data-parent="claude:review-api-boundaries/agent:reviewer"]').wait_for()
 page.locator('[data-parent="claude:review-api-boundaries/agent:reviewer"]').click()
 page.wait_for_function("() => document.querySelector('#detailhead h2').textContent.endsWith('/agent:reviewer')")
 page.locator('[data-id="codex:refactor-auth-middleware"]').click()
 page.wait_for_function("() => document.querySelector('#detailhead h2').textContent==='codex:refactor-auth-middleware'")
 page.locator('.event details').first.locator('summary').click()
 page.screenshot(path=str(OUT/'desktop.png'),full_page=True)
 assert page.evaluate('() => document.documentElement.scrollWidth <= innerWidth')
 page.set_viewport_size({'width':390,'height':844})
 page.screenshot(path=str(OUT/'mobile.png'),full_page=True)
 assert page.evaluate('() => document.documentElement.scrollWidth <= innerWidth'),'mobile overflow'
 # Error feedback and recovery.
 page.route('**/api/stats',lambda route:route.fulfill(status=500,body='failure'))
 page.wait_for_function("() => document.querySelector('#error').textContent.includes('Could not load')",timeout=10000)
 page.unroute('**/api/stats')
 page.wait_for_function("() => document.querySelector('#error').textContent===''",timeout=10000)
 assert not errors,errors
 browser.close()
print('PASS: login, filters, search/empty, relation links, refresh/raw state, export, error recovery, desktop/mobile layout; screenshots saved.')
