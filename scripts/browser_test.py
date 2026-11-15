#!/usr/bin/env python3
"""Exercise the revised UI against a fresh scripts/demo.py database."""
import os,pathlib
from playwright.sync_api import sync_playwright
BASE=os.getenv('AGENTLOG_URL','http://127.0.0.1:4318');TOKEN=os.environ['AGENTLOG_TOKEN']
OUT=pathlib.Path(__file__).resolve().parents[1]/'docs';OUT.mkdir(exist_ok=True)
with sync_playwright() as p:
 browser=p.chromium.launch(executable_path='/usr/bin/chromium',headless=True,args=['--no-sandbox'])
 page=browser.new_page(viewport={'width':1440,'height':1060});errors=[];page.on('pageerror',lambda e:errors.append(str(e)))
 page.goto(BASE);page.locator('#token').fill('incorrect');page.locator('#loginform button').click();page.get_by_text('Invalid access token.').wait_for()
 page.locator('#token').fill(TOKEN);page.locator('#loginform button').click();page.locator('.session').first.wait_for();page.screenshot(path=str(OUT/'ui-folders.png'),full_page=True);page.locator('[data-project="path:/projects/agentlog"]').click();page.locator('.session').first.wait_for()
 assert page.locator('#detailpage').is_hidden();assert page.locator('.stats').count()==0
 page.screenshot(path=str(OUT/'ui-sessions.png'),full_page=True)
 page.locator('#search').fill('no-match-xyz');page.get_by_text('No sessions',exact=True).wait_for()
 page.locator('#search').fill('');page.locator('[data-session="claude:7d4b3a91-pagination"]').wait_for();page.locator('[data-session="claude:7d4b3a91-pagination"]').click()
 page.locator('.prose').filter(has_text='expected 5 records').wait_for();assert '/sessions/' in page.url
 assert page.locator('#listpage').is_hidden();assert page.locator('.event').count()==6;assert page.locator('details[open]').count()==0
 assert page.locator('.event > time').count()==0
 assert page.locator('.eventheading time').count()==6
 assert page.locator('.status.failed').count()==1
 assert page.locator('.status.succeeded').count()==2
 assert page.locator('.failure-detail').evaluate('(el) => getComputedStyle(el).color')=='rgb(180, 35, 24)'
 assert page.locator('.event').first.bounding_box()['x'] < 40
 assert page.locator('.eventheading time').first.bounding_box()['y'] < page.locator('.event').first.bounding_box()['y'] + 35
 assert page.get_by_text('go test ./internal/app -run TestPagination',exact=True).is_visible()
 page.screenshot(path=str(OUT/'ui-claude.png'),full_page=True)
 page.locator('.event.error').click()
 assert page.locator('#inspector').is_visible()
 assert page.locator('.event.error').get_attribute('aria-expanded')=='true'
 page.locator('#inspectdetails').get_by_text('ShellError',exact=True).wait_for()
 assert page.evaluate('() => !document.querySelector("#eventlist").inert')
 page.screenshot(path=str(OUT/'ui-event-details.png'),full_page=True)
 page.locator('#jsontab').click();page.locator('#eventjson').wait_for();page.wait_for_function("() => document.querySelector('#inspectstatus').textContent===''")
 assert 'tool_parameters' in page.locator('#eventjson').inner_text()
 page.context.grant_permissions(['clipboard-read','clipboard-write'],origin=BASE)
 page.locator('#copyjson').click();page.get_by_text('Copied',exact=True).wait_for()
 assert 'tool_parameters' in page.evaluate('() => navigator.clipboard.readText()')
 page.wait_for_timeout(5200)
 assert page.locator('#jsontab').get_attribute('aria-selected')=='true'
 assert page.locator('.event.error.selected').count()==1
 page.screenshot(path=str(OUT/'ui-event-json.png'),full_page=True)
 page.keyboard.press('Escape');assert page.locator('#inspector').is_hidden()
 assert page.locator('.event.error').evaluate('(el) => el===document.activeElement')
 page.keyboard.press('Enter');assert page.locator('#inspector').is_visible()
 page.locator('#closeinspector').click()
 page.locator('.event').first.click();page.locator('#detailtab').click()
 page.keyboard.press('ArrowRight');assert page.locator('#jsontab').get_attribute('aria-selected')=='true'
 page.locator('#closeinspector').click()
 page.locator('button[data-kind="error"]').click();page.wait_for_function("document.querySelectorAll('.event').length===1")
 page.locator('.event').click();page.locator('button[data-kind="conversation"]').click();assert page.locator('#inspector').is_hidden()
 page.locator('button[data-kind=""]').click()
 with page.expect_download() as d:page.get_by_text('Export JSON',exact=True).click()
 assert d.value.suggested_filename=='session.json'
 page.get_by_text('Subagent: reviewer',exact=True).click();page.locator('#subagentevents .prose').filter(has_text='Reviewed the query:').wait_for();page.locator('#subagentfull').click();page.locator('#timeline .prose').filter(has_text='Reviewed the query:').wait_for()
 page.get_by_text('Parent session',exact=True).click();page.locator('.event').first.wait_for()
 page.reload();page.locator('.event').first.wait_for();assert page.locator('#listpage').is_hidden()
 for sid,image in [('codex:c82f16a0-auth','ui-codex.png'),('copilot:e95b2c40-search','ui-copilot.png')]:
  page.goto(BASE+'/sessions/'+__import__('urllib.parse',fromlist=['quote']).quote(sid,safe=''));page.locator('.event').first.wait_for();page.screenshot(path=str(OUT/image),full_page=True)
 page.locator('.event.tool').click();page.locator('#detailtab').click();page.locator('#inspectparent').click();assert page.locator('.event.model.selected').count()==1
 page.locator('#closeinspector').click()
 page.locator('button[data-kind="conversation"]').click();assert page.locator('.event').count()==1
 page.goto(BASE+'/sessions/claude%3A7d4b3a91-pagination');page.locator('.event').first.wait_for();page.set_viewport_size({'width':390,'height':844});assert page.evaluate('() => document.documentElement.scrollWidth <= innerWidth');page.screenshot(path=str(OUT/'ui-mobile.png'),full_page=True)
 page.locator('.event.error').click();assert page.locator('#inspector').get_attribute('aria-modal')=='true'
 assert page.evaluate('() => document.querySelector("#eventlist").inert')
 page.locator('#detailtab').click();page.screenshot(path=str(OUT/'ui-event-mobile.png'))
 assert page.locator('#inspector').bounding_box()=={'x':0,'y':0,'width':390,'height':844}
 page.locator('#jsontab').click();page.locator('#eventjson').focus();page.keyboard.press('Tab')
 assert page.locator('#closeinspector').evaluate('(el) => el===document.activeElement')
 assert page.evaluate('() => document.documentElement.scrollWidth <= innerWidth')
 page.keyboard.press('Escape');assert page.evaluate('() => !document.querySelector("#eventlist").inert')
 assert page.locator('#autorefresh').count()==0
 page.route('**/api/session?*',lambda r:r.fulfill(status=500,body='failure'));page.reload();page.get_by_text('Unable to load (HTTP 500).').wait_for(timeout=10000);page.unroute('**/api/session?*');page.reload();page.wait_for_function("() => document.querySelector('#error').textContent===''",timeout=10000)
 page.goto(BASE);page.locator('.session').first.wait_for();page.screenshot(path=str(OUT/'ui-folders-mobile.png'),full_page=True)
 assert not errors,errors
 browser.close()
print('PASS: folder drill-down, standalone routes/reload, list filters/search, per-agent rendering, subagent navigation, inspector details/JSON, manual-refresh behavior, keyboard/focus trap, filters, errors, export, no automatic refresh, mobile; 10 screenshots.')
