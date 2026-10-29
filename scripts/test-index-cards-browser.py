"""Optional real-browser smoke test for an isolated offline Indexes workspace.

Requires installed Python Playwright and Chromium. Does not download a browser,
change browser policy, use a real profile or open external Bitcoin/Ord sources.
"""
import argparse
import json
import pathlib
import subprocess
import tempfile
import time
import urllib.request
from playwright.sync_api import sync_playwright

parser = argparse.ArgumentParser()
parser.add_argument('--binary', required=True)
parser.add_argument('--browser', default='/usr/bin/chromium')
parser.add_argument('--out', required=True)
args = parser.parse_args()
out = pathlib.Path(args.out).resolve()
out.mkdir(parents=True, exist_ok=True)
with tempfile.TemporaryDirectory(prefix='gateway-cards-') as tmp:
    profile = pathlib.Path(tmp)
    (profile / 'settings.json').write_text(json.dumps({
        'network_disabled': True, 'headers_paused': True,
        'core_disabled': True, 'core_mount_disabled': True,
        'serve_data': False, 'share_cache': False, 'onboarded': True,
    }))
    with (out / 'runtime.txt').open('w') as log:
        process = subprocess.Popen([str(pathlib.Path(args.binary).resolve()), '-data', tmp,
                                    '-background', '-no-tray', '-http', '127.0.0.1:0'],
                                   stdout=log, stderr=subprocess.STDOUT)
        try:
            endpoint = None
            for _ in range(100):
                if process.poll() is not None:
                    raise RuntimeError('Fixture runtime exited before startup')
                runtime = profile / 'runtime.json'
                if runtime.exists():
                    endpoint = json.loads(runtime.read_text())['url']
                    try:
                        urllib.request.urlopen(endpoint + '/api/v1/runtime/ping', timeout=1).close()
                        break
                    except OSError:
                        pass
                time.sleep(.1)
            if not endpoint:
                raise RuntimeError('No fixture endpoint')
            with sync_playwright() as p:
                browser = p.chromium.launch(headless=True, executable_path=args.browser)
                try:
                    page = browser.new_page(viewport={'width': 1280, 'height': 900})
                    errors = []
                    page.on('pageerror', lambda error: errors.append(str(error)))
                    page.goto(endpoint + '/indexes', wait_until='networkidle', timeout=15000)
                    page.locator('#index-cards .index-card').nth(1).wait_for()
                    assert page.locator('#index-cards .index-card').count() == 2
                    assert page.locator('select#index, select#query-index, select#peer-index').count() == 0
                    page.screenshot(path=str(out / 'cards-desktop.png'), full_page=True)
                    switch = page.get_by_role('switch', name='bitmap On', exact=True)
                    switch.focus()
                    page.keyboard.press('Space')
                    page.wait_for_function('document.querySelector(\'[aria-label="bitmap On"]\').checked')
                    assert (profile / 'indexes' / 'live.json').exists(), 'Fresh On must persist'
                    assert not page.locator('#selected-index-label').is_visible(), 'On only selected a card'
                    live = page.get_by_role('switch', name='bitmap Live', exact=True)
                    assert not live.is_disabled(), 'Fresh Live unavailable'
                    live.focus()
                    page.keyboard.press('Space')
                    page.wait_for_function('document.querySelector(\'[aria-label="bitmap Live"]\').checked && document.querySelector(\'[aria-label="bitmap On"]\').checked')
                    page.reload(wait_until='networkidle')
                    page.wait_for_function('document.querySelector(\'[aria-label="bitmap Live"]\')?.checked && document.querySelector(\'[aria-label="bitmap On"]\')?.checked')
                    page.locator('[data-index="bitmap"] button').click()
                    assert page.locator('#plan-form').is_visible()
                    assert page.locator('#selected-index-label').inner_text() == 'Bitmap districts'
                    assert page.locator('#build').is_disabled()
                    assert not (profile / 'indexes' / 'job.json').exists(), 'Offline Live attempted a scan'
                    page.set_viewport_size({'width': 390, 'height': 844})
                    assert page.evaluate('document.documentElement.scrollWidth <= window.innerWidth'), 'Narrow overflow'
                    page.screenshot(path=str(out / 'cards-narrow.png'), full_page=True)
                    assert not errors, errors
                    (out / 'result.json').write_text(json.dumps({'result':'PASS','scope':'fresh offline runtime, desktop/narrow, persisted keyboard On and initial Live enabling On/reload, honest offline no-scan','page_errors':errors}, indent=2))
                    print('Index cards browser: PASS (isolated offline runtime, actual DOM/CSP and keyboard; no public-network acceptance).')
                finally:
                    browser.close()
        except Exception as error:
            (out / 'result.json').write_text(json.dumps({'result':'NOT_PASSED','error':str(error)}, indent=2))
            raise
        finally:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
