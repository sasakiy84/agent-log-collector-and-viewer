#!/usr/bin/env python3
"""Palette semantics, contrast and scroll visibility on desktop/mobile. Synthetic data."""
import json
import os
import socket
import subprocess
import tempfile
import time
import urllib.parse
import urllib.request
from pathlib import Path

from playwright.sync_api import expect, sync_playwright

BINARY = os.getenv("AGENTLOG_BIN", "./agentlog")
TOKEN = "synthetic-palette-browser-token"


def attrs(values):
    return [{"key": key, "value": {"stringValue": str(value)}} for key, value in values.items()]


def log(session, number, name, values):
    return {"eventName": name, "timeUnixNano": str(1780000000000000000 + number * 1000000000),
            "attributes": attrs({"session.id": session, "cwd": "/work/palette", **values})}


with tempfile.TemporaryDirectory() as folder:
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    base = f"http://127.0.0.1:{port}"
    server = subprocess.Popen([BINARY, "--listen", f"127.0.0.1:{port}", "--db", folder + "/test.db"],
                              env={**os.environ, "AGENTLOG_TOKEN": TOKEN}, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        for _ in range(200):
            try:
                urllib.request.urlopen(base, timeout=1).close()
                break
            except OSError:
                time.sleep(.05)
        resource = {"attributes": attrs({"service.name": "codex"})}
        rows = []
        for turn in range(12):
            kinds = [
                ("user_prompt", {"prompt": "検索結果を確認して、問題を修正してほしい。"}),
                ("assistant_response", {"response": "検索条件とセッションの集約処理を確認します。"}),
                ("tool_result", {"tool_name": "exec_command", "arguments": '{"cmd":"go test ./..."}', "output": "PASS: stable ordering", "success": "true"}),
                ("gen_ai.client.operation", {"gen_ai.operation.name": "chat", "gen_ai.request.model": "example-model", "gen_ai.usage.input_tokens": "123"}),
                ("codex.skill_invocation", {"skill.name": "review", "turn.id": str(turn)}),
                ("invoke_agent", {"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": "Reviewer"}),
                ("tool_result", {"tool_name": "exec_command", "output": "\n".join(f"Line {line}: synthetic failure detail" for line in range(100)), "success": "false", "error": "Expected stable ordering; received a different row."}),
            ]
            rows += [log("palette", turn * 10 + index, name, values) for index, (name, values) in enumerate(kinds)]
        rows += [log(f"list-{index:03d}", 1000 + index, "user_prompt", {"prompt": f"Review session {index:03d}"}) for index in range(100)]
        def send(signal, body):
            request = urllib.request.Request(base + "/v1/" + signal, json.dumps(body).encode(), headers={"Authorization": "Bearer " + TOKEN, "Content-Type": "application/json"})
            urllib.request.urlopen(request).close()
        send("logs", {"resourceLogs": [{"resource": resource, "scopeLogs": [{"logRecords": rows}]}]})
        send("metrics", {"resourceMetrics": [{"resource": resource, "scopeMetrics": [{"metrics": [{"name": "gen_ai.client.token.usage", "unit": "tokens", "sum": {"aggregationTemporality": 1, "dataPoints": [{"timeUnixNano": "1780000200000000000", "asInt": "123", "attributes": attrs({"session.id": "palette"})}]}}]}]}]})
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path="/usr/bin/chromium", headless=True, args=["--no-sandbox"])
            page = browser.new_page(viewport={"width": 1440, "height": 1000})
            errors = []
            page.on("pageerror", lambda error: errors.append(str(error)))
            page.goto(base + "/?folder=" + urllib.parse.quote("/work/palette", safe=""))
            page.locator("#token").fill(TOKEN)
            page.locator("#loginform button").click()
            expect(page.locator(".session")).to_have_count(101)
            page.locator(".session").nth(50).scroll_into_view_if_needed()
            assert page.locator(".topbar").bounding_box()["y"] == 0
            assert abs(page.locator(".tablehead").bounding_box()["y"] - page.locator(".topbar").bounding_box()["height"]) < 1
            page.goto(base + "/sessions/codex%3Apalette?page=1")
            expect(page.locator(".event")).to_have_count(85)
            tones = {}
            for kind in ("user", "assistant", "tool", "model", "skill", "metric"):
                heading = page.locator(f".event.{kind}:not(.error) .eventheading").first
                tones[kind] = heading.evaluate("el=>getComputedStyle(el).backgroundColor")
            assert len(set(tones.values())) == len(tones), tones
            failed = page.locator(".event.error").nth(3)
            failed.evaluate("el=>el.scrollIntoView({block:'start'})")
            toolbar = page.locator(".logbar").bounding_box()
            assert abs(toolbar["y"] - page.locator(".topbar").bounding_box()["height"]) < 1
            assert failed.bounding_box()["y"] >= toolbar["y"] + toolbar["height"]
            failed.click()
            expect(page.locator("#inspector")).to_have_attribute("data-error", "true")
            assert page.locator("#inspector").bounding_box()["y"] >= toolbar["y"] + toolbar["height"]
            header = page.locator(".inspectorheader").bounding_box()
            page.locator(".inspectorbody").evaluate("el=>el.scrollTop=el.scrollHeight")
            assert page.locator(".inspectorheader").bounding_box() == header
            # All visible text uses AA contrast against its actual opaque surface.
            failures = page.evaluate("""() => {
              const rgb=s=>s.match(/[\\d.]+/g).map(Number);
              const lum=c=>c.slice(0,3).map(x=>{x/=255;return x<=.04045?x/12.92:((x+.055)/1.055)**2.4}).reduce((a,x,i)=>a+x*[.2126,.7152,.0722][i],0);
              const background=el=>{while(el){const c=rgb(getComputedStyle(el).backgroundColor);if(c.length===3||c[3]===1)return c;el=el.parentElement}return [255,255,255]};
              return [...document.querySelectorAll('.eventheading h2,.eventheading time,.eventheading .kind,.event .prose,.status,.blocklabel,.inspectorheading h2')].filter(el=>el.getClientRects().length).flatMap(el=>{
                const a=lum(rgb(getComputedStyle(el).color)),b=lum(background(el)),ratio=(Math.max(a,b)+.05)/(Math.min(a,b)+.05);
                return ratio<4.5?[{text:el.textContent.slice(0,30),ratio}]:[];
              });
            }""")
            assert not failures, failures
            # A primitive-token change updates multiple components without reload.
            before = page.locator(".event.user .eventheading").first.evaluate("el=>getComputedStyle(el).backgroundColor")
            page.evaluate("document.documentElement.style.setProperty('--indigo-3',getComputedStyle(document.documentElement).getPropertyValue('--indigo-4'))")
            changed = page.locator(".event.user .eventheading").first.evaluate("el=>getComputedStyle(el).backgroundColor")
            assert changed != before
            assert page.locator('.logbar [data-kind=""].active').evaluate("el=>getComputedStyle(el).backgroundColor") == changed
            page.evaluate("document.documentElement.style.removeProperty('--indigo-3')")
            output = Path(os.getenv("AGENTLOG_SCREENSHOT_DIR", folder))
            output.mkdir(parents=True, exist_ok=True)
            page.screenshot(path=str(output / "ui-palette-scroll.png"))
            for width in (768, 390, 320):
                page.locator("#closeinspector").click()
                page.set_viewport_size({"width": width, "height": 844})
                failed.evaluate("el=>el.scrollIntoView({block:'start'})")
                page.wait_for_function("Math.abs(document.querySelector('.logbar').getBoundingClientRect().height-parseFloat(getComputedStyle(document.querySelector('#detailpage')).getPropertyValue('--timeline-toolbar-height')))<1")
                toolbar = page.locator(".logbar").bounding_box()
                assert failed.bounding_box()["y"] >= toolbar["y"] + toolbar["height"] - 1
                assert page.evaluate("document.documentElement.scrollWidth<=innerWidth"), page.evaluate("({width:innerWidth,scroll:document.documentElement.scrollWidth,offenders:[...document.querySelectorAll('body *')].filter(el=>el.getBoundingClientRect().right>innerWidth+1).slice(0,12).map(el=>({tag:el.tagName,id:el.id,class:el.className,right:el.getBoundingClientRect().right}))})")
                failed.click()
                expect(page.locator("#inspector")).to_have_attribute("aria-modal", "true")
                assert page.locator("#inspector").bounding_box() == {"x": 0, "y": 0, "width": width, "height": 844}
                if width == 390:
                    page.screenshot(path=str(output / "ui-palette-mobile.png"))
            assert not errors, errors
            browser.close()
    finally:
        server.terminate()
        server.wait(timeout=20)
print("PASS: distinct semantic tones, AA text contrast, CSS-variable propagation, sticky list/filters, unobscured drawer, fixed inspector header and mobile widths")
