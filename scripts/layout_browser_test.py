#!/usr/bin/env python3
"""Capture real UI layouts with long content and measure shared alignment axes."""
import json, os, pathlib, socket, subprocess, tempfile, time, urllib.request
from playwright.sync_api import sync_playwright, expect
from test_support import wait_for_ingestion

binary = os.getenv('AGENTLOG_BIN', './agentlog')
out = pathlib.Path(os.getenv('AGENTLOG_SCREENSHOT_DIR', '/tmp/agentlog-layout-shots'))
out.mkdir(parents=True, exist_ok=True)
measurements = []
verify = os.getenv("AGENTLOG_LAYOUT_CHECKS", "1") != "0"
with tempfile.TemporaryDirectory() as folder:
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0)); port = sock.getsockname()[1]
    base = f'http://127.0.0.1:{port}'
    proc = subprocess.Popen([binary, '--listen', f'127.0.0.1:{port}', '--db', folder+'/layout.db'], env={**os.environ, 'AGENTLOG_TOKEN':''}, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        for _ in range(100):
            try: urllib.request.urlopen(base, timeout=1).close(); break
            except OSError: time.sleep(.05)
        def attrs(values):
            return [{'key':k,'value':{'boolValue':v} if isinstance(v,bool) else {'stringValue':str(v)}} for k,v in values.items()]
        def record(session, index, name, values, old=False):
            return {'eventName':name,'timeUnixNano':str((1735689600000000000 if old else 1794736800000000000)+index*1000000000),'attributes':attrs({'session.id':session,**values})}
        rows = [record('review',0,'codex.user_prompt',{'service.namespace':'Agentlog','cwd':'/work/agentlog','prompt':'検索結果の表示を確認して、プロジェクト一覧とセッション詳細の位置・余白を揃えてください。','turn.id':'turn-one'})]
        rows += [record('review',1,'codex.skill_invocation',{'skill.name':'review-layout-and-accessibility','skill.plugin_id':'workspace-tools','turn.id':'turn-one'}),
            record('review',2,'gen_ai.tool.start',{'tool_name':'exec_command','call_id':'layout-test','arguments':json.dumps({'cmd':'go test ./internal/app -run TestSessionLayout -count=1'})}),
            record('review',3,'gen_ai.tool.result',{'tool_name':'exec_command','call_id':'layout-test','output':'FAIL: expected aligned column headers\n'+('/work/a-long-directory-name/'*5)+'layout_test.go:42','success':False,'error':'Exit code 1'}),
            record('review',4,'codex.agent_response',{'response':'検索欄、一覧の列、詳細パネルの見出しを確認しました。長いプロジェクト名や日本語の説明も確認します。','turn.id':'turn-one','model':'gpt-6.1-sol'}),
            record('review',5,'gen_ai.client.operation',{'gen_ai.request.model':'gpt-6.1-sol','gen_ai.usage.input_tokens':14522,'gen_ai.usage.output_tokens':820,'gen_ai.usage.cache_read.input_tokens':4096})]
        for i,(session,project,prompt) in enumerate([('other','Documentation','セットアップガイドの手順を整理して'),('long','日本語の長いプロジェクト名-long-project-name-for-layout-review','長いプロジェクト名でも操作ボタンが隠れないか確認してください'),('archive','Agentlog','以前の調査ログを確認する')]):
            rows += [record(session,10+i,'codex.user_prompt',{'service.namespace':project,'cwd':'/work/'+session,'prompt':prompt},old=session=='archive')]
        for i,name in enumerate(['日本語ドキュメントのレビュー','a-very-long-skill-name-without-spaces-to-check-narrow-columns','review-core']):
            rows += [record('other',20+i,'codex.skill_invocation',{'skill.name':name,'skill.plugin_id':'long-plugin-name-for-layout-verification','turn.id':str(i),'skill.scope':'project'})]
        rows += [record('legacy',0,'codex.user_prompt',{'cwd':'/work/long-archive-folder-name-for-spacing-review','prompt':'Archived fixture'},old=True)]
        req=urllib.request.Request(base+'/v1/logs',json.dumps({'resourceLogs':[{'resource':{'attributes':attrs({'service.name':'codex'})},'scopeLogs':[{'logRecords':rows}]}]}).encode(),headers={'Content-Type':'application/json'})
        urllib.request.urlopen(req).close(); wait_for_ingestion(base,'')
        with sync_playwright() as p:
            browser=p.chromium.launch(executable_path=os.getenv('AGENTLOG_CHROMIUM', '/usr/bin/chromium'),headless=True,args=['--no-sandbox'])
            page=browser.new_page(locale='ja-JP',timezone_id='Asia/Tokyo'); errors=[]
            page.on('pageerror',lambda error:errors.append(str(error)))
            def capture(name,width):
                page.evaluate('document.activeElement?.blur()')
                page.screenshot(path=str(out/f'{name}-{width}.png'))
                measurements.append({'view':name,'width':width,**page.evaluate('''() => {
                  const box=s=>{const e=document.querySelector(s);if(!e||!e.getClientRects().length)return null;const r=e.getBoundingClientRect();return {x:r.x,y:r.y,w:r.width,h:r.height,right:r.right,bottom:r.bottom}};
                  return {overflow:document.documentElement.scrollWidth>innerWidth, brand:box('.brand'),search:box('.search-query'),time:box('#searchtime'),sidebarTitle:box('#projectsidebar h2'),heading:box('.pageheading'),table:box('.tablehead'),row:box('.session'),skills:box('.skill-row'),inspector:box('#inspector'),inspectorHeading:box('.inspectorheading'),tabs:box('.inspectortabs'),body:box('.inspectorbody'),detailHeading:box('#detailpage .pageheading'),dialog:box('#archivepanel')};
                }''')})
                assert page.evaluate('document.documentElement.scrollWidth<=innerWidth'),(name,width)
                if verify:
                    assert page.evaluate("Math.abs(document.querySelector('.brand').getBoundingClientRect().x-(document.querySelector('main:not([hidden])').getBoundingClientRect().x+parseFloat(getComputedStyle(document.querySelector('main:not([hidden])')).paddingLeft)))<1"),(name,width,'shell axes')
                    if name in ('inspector','inspector-json') and width>900:
                        assert page.locator('#inspector').bounding_box()['y']+page.locator('#inspector').bounding_box()['height']<=page.viewport_size['height']-8,(name,width,'inspector below viewport')
                    if name=='time-picker' and width<=720:
                        picker=page.locator('.time-picker').bounding_box();query=page.locator('.search-query').bounding_box()
                        assert abs(picker['x']-query['x'])<1 and abs(picker['width']-query['width'])<1,'mobile time popup axes'
                    if name=='inspector-loading':
                        assert page.locator('#inspectstatus').evaluate('(e)=>{const r=document.createRange();r.selectNodeContents(e);return r.getBoundingClientRect().left-e.closest("#inspector").getBoundingClientRect().left}')>=12,'loading text touches panel border'
                    if name=='sessions' and width<=720:
                        assert page.locator('.session .source').first.bounding_box()['y'] < page.locator('.session small').first.bounding_box()['y'],(width,'agent on empty row')
                    if name=='archive':
                        right=page.locator('#closearchive').bounding_box();edge=right['x']+right['width']
                        for box in page.locator('.archive-folder-row>span:last-child').all():
                            text_right=box.evaluate('(e)=>{const r=document.createRange();r.selectNodeContents(e);return r.getBoundingClientRect().right}');assert abs(text_right-edge)<2,(width,'archive size alignment')
                    if name=='skills' and width>720:
                        for col in range(2,4):
                            heading=page.locator(f'.skill-tablehead>span:nth-child({col})').bounding_box()
                            for cell in page.locator(f'.skill-row>span:nth-child({col})').all():
                                text_right=cell.evaluate('(e)=>{const r=document.createRange();r.selectNodeContents(e);return r.getBoundingClientRect().right}');assert abs(text_right-heading['x']-heading['width'])<1,(width,'skill numeric alignment')
            for width in [1920,1440,1024,768,390,320]:
                page.set_viewport_size({'width':width,'height':900 if width>720 else 844})
                page.goto(base);expect(page.locator('.session')).to_have_count(5);capture('sessions',width)
                page.locator('#searchoptions summary').click();capture('search-options',width);page.keyboard.press('Escape')
                page.locator('#searchtime summary').click();page.locator('#searchrange').select_option('custom');capture('time-picker',width);page.keyboard.press('Escape')
                page.locator('[data-project="name:日本語の長いプロジェクト名-long-project-name-for-layout-review"]').click();expect(page.locator('.session')).to_have_count(1);capture('long-project',width)
                page.goto(base+'/skills');expect(page.locator('.skill-row')).to_have_count(4);capture('skills',width)
                page.goto(base+'/sessions/codex%3Areview?view=activity');expect(page.locator('#timeline .event')).to_have_count(5)
                page.evaluate('window.scrollTo(0,0)');capture('session-detail',width)
                held=[]
                if width==1024:page.route('**/api/event?**',lambda route:held.append(route))
                page.locator('#timeline .event.tool').first.click();expect(page.locator('#inspector')).to_be_visible()
                if width==1024:
                    expect(page.locator('#inspectstatus')).to_have_text('Loading details…');capture('inspector-loading',width)
                    held[0].continue_();page.unroute('**/api/event?**')
                expect(page.locator('#inspectstatus')).to_have_text('');capture('inspector',width)
                page.locator('#jsontab').click();expect(page.locator('#eventjson')).not_to_be_empty();capture('inspector-json',width);page.keyboard.press('Escape')
                page.goto(base);expect(page.locator('.session')).to_have_count(5)
                page.locator('#dataoptions summary').click();page.locator('#openarchive').click();expect(page.locator('.archive-row')).to_have_count(2);capture('archive',width);page.keyboard.press('Escape')
            # A shorter window and scrolling must keep the sticky inspector usable.
            if verify:
                page.set_viewport_size({'width':1024,'height':520});page.goto(base+'/sessions/codex%3Areview?view=activity')
                expect(page.locator('#timeline .event')).to_have_count(5);page.locator('#timeline .event.tool').first.click()
                expect(page.locator('#inspectstatus')).to_have_text('')
                page.wait_for_function("document.querySelector('#inspector').getBoundingClientRect().bottom <= innerHeight-8")
                capture('inspector-short',1024)
                page.evaluate('window.scrollBy(0,120)')
                page.wait_for_function("document.querySelector('#inspector').getBoundingClientRect().bottom <= innerHeight-8")
                body=page.locator('.inspectorbody');header=page.locator('.inspectorheader').bounding_box()
                body.evaluate('(e)=>e.scrollTop=e.scrollHeight')
                assert abs(page.locator('.inspectorheader').bounding_box()['y']-header['y'])<1,'inspector header moved on body scroll'
                page.evaluate('window.scrollTo(0,0)')
                page.evaluate('()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))')
                assert page.evaluate('scrollY')==0,'inspector prevents scrolling back to the session header'
            assert not errors,errors
            browser.close()
    finally:
        proc.terminate();proc.wait(timeout=20)
(out/'measurements.json').write_text(json.dumps(measurements,ensure_ascii=False,indent=2))
print('PASS: actual screenshots across six widths, long labels, list/skills, search/time controls, detail/JSON inspector and archive; no document overflow')
