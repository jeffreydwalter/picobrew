#!/usr/bin/env python3
"""Browser history and parameterized API requests against deterministic fixtures.

The existing portable C API handles ordinary console requests. History routes
model paginated retained recordings; no physical USB, radio or flash is used.
"""
import csv
import io
import json
from pathlib import Path
import unittest
from urllib.parse import parse_qs, urlsplit

from playwright.sync_api import expect
import test_web as base

TEST_KEY = base.TEST_KEY

FIELDS = ['elapsed_ms', 'wort_f', 'thermoblock_f', 'target_f', 'step_seconds',
          'phase', 'controller_status', 'compartment', 'current_step', 'flags', 'device_state']


def recording(identifier, count=70, base=70):
    rows = [[i * 15000, base + i / 2, base + 20 + i / 3, 152, 900 - i * 15,
             0 if i < 30 else 1, 1, 2, 0 if i < 30 else 1,
             1 | 2 | 8 | (16 if i == 40 else 0), 1] for i in range(count)]
    return {'meta': {'id': identifier, 'command': 'brew', 'recipe_name': 'Brew ' + str(identifier),
                     'state': 'completed', 'duration_ms': rows[-1][0], 'record_count': count,
                     'persisted_record_count': count, 'gap_count': int(count > 40),
                     'truncated': False, 'storage_error': False, 'recovered_after_restart': False},
            'rows': rows}


class WebFeatures(unittest.TestCase):
    setUpClass = classmethod(base.BrowserWorkflow.setUpClass.__func__)
    tearDownClass = classmethod(base.BrowserWorkflow.tearDownClass.__func__)
    setUp = base.BrowserWorkflow.setUp
    tearDown = base.BrowserWorkflow.tearDown
    connect = base.BrowserWorkflow.connect

    def history(self, histories=None, evict_after_first_page=False):
        self.histories = histories or {7: recording(7), 6: recording(6, 5, 50)}
        self.history_requests = []
        self.eviction_done = False

        def handler(route):
            request = route.request
            url = urlsplit(request.url)
            self.history_requests.append((url.path, parse_qs(url.query), request.headers))
            self.assertEqual(request.method, 'GET')
            if request.headers.get('authorization') != 'Bearer ' + TEST_KEY:
                route.fulfill(status=401, json={'error': 'unauthorized'})
                return
            if url.path == '/api/v1/history':
                entries = [self.histories[key]['meta'] for key in sorted(self.histories, reverse=True)]
                route.fulfill(json={**entries[0], 'available': True, 'sessions': entries,
                                    'fields': FIELDS, 'sample_interval_ms': 15000,
                                    'checkpoint_interval_ms': 60000,
                                    'capacity_records': 4224, 'capacity_sessions': 8})
                return
            offset = int(url.path.rsplit('/', 1)[1])
            identifier = int(parse_qs(url.query)['id'][0])
            if identifier not in self.histories:
                route.fulfill(status=409, json={'error': 'history_changed'})
                return
            rows = self.histories[identifier]['rows']
            page = rows[offset:offset + 32]
            route.fulfill(json={'id': identifier, 'offset': offset, 'samples': page,
                                'next_offset': offset + len(page) if offset + len(page) < len(rows) else None})
            if evict_after_first_page and not self.eviction_done:
                self.eviction_done = True
                self.histories = {8: recording(8, 70, 90)}

        self.page.route('**/api/v1/history**', handler)

    def test_browser_appearance_and_authentication(self):
        root = self.page.locator('html')
        chooser = self.page.get_by_label('Appearance', exact=True)
        light, dark = 'rgb(245, 246, 241)', 'rgb(23, 28, 25)'
        self.page.emulate_media(color_scheme='dark')
        expect(root).to_have_css('background-color', dark)
        expect(chooser).to_be_hidden()
        self.connect()
        self.page.locator('[data-tab="settings"]').click()
        expect(chooser).to_have_value('system')
        writes = []
        self.page.on('request', lambda request: writes.append(request.url) if request.method != 'GET' else None)
        for width in (1440, 390):
            self.page.set_viewport_size({'width': width, 'height': 900})
            for choice, background in [('light', light), ('dark', dark), ('system', dark)]:
                chooser.select_option(choice)
                expect(root).to_have_css('background-color', background)
                chooser.focus()
                expect(chooser).to_be_focused()
                self.assertFalse(self.page.evaluate('document.documentElement.scrollWidth > innerWidth'))
                self.page.screenshot(path=self.output / f'settings-{width}-{choice}.png', animations='disabled')
        self.page.emulate_media(color_scheme='light')
        expect(root).to_have_css('background-color', light)
        chooser.select_option('dark')
        self.assertEqual(writes, [])
        self.page.reload()
        expect(self.page.locator('#connectionText')).to_have_text('Connected')
        self.page.locator('[data-tab="settings"]').click()
        expect(chooser).to_have_value('dark')
        self.page.locator('#disconnectButton').click()
        self.page.reload()
        expect(chooser).to_be_hidden()
        expect(root).to_have_css('background-color', dark)
        self.page.locator('#connectButton').click()
        expect(self.page.locator('#connectDialog')).to_have_css('background-color', 'rgb(35, 42, 37)')
        self.page.screenshot(path=self.output / 'connect-dark-mobile.png')
        self.assertEqual(self.page.evaluate('({...localStorage})'), {'picobrew-theme': 'dark'})

    def test_system_theme_recolors_history_and_preserves_oled(self):
        self.page.emulate_media(color_scheme='light')
        self.history()
        self.connect()
        expect(self.page.locator('#displayHint')).to_contain_text('LIVE OLED')
        oled = self.page.locator('#display')
        frame = oled.evaluate('canvas => canvas.toDataURL()')
        self.page.emulate_media(color_scheme='dark')
        self.assertEqual(oled.evaluate('canvas => canvas.toDataURL()'), frame)
        self.page.locator('[data-tab="history"]').click()
        expect(self.page.locator('#historyExportJson')).to_be_enabled()
        has_gap_color = """([r,g,b]) => {
          const canvas=document.getElementById('historyChart');
          const pixels=canvas.getContext('2d').getImageData(0,0,canvas.width,canvas.height).data;
          for(let i=0;i<pixels.length;i+=4)
            if(pixels[i]===r && pixels[i+1]===g && pixels[i+2]===b) return true;
          return false;
        }"""
        self.page.wait_for_function(has_gap_color, arg=[88,62,54])
        self.page.emulate_media(color_scheme='light')
        self.page.wait_for_function(has_gap_color, arg=[243,215,203])

    def test_system_appearance_when_local_storage_is_blocked(self):
        self.context.add_init_script("""Object.defineProperty(window, 'localStorage', {
            get() { throw new Error('Storage is blocked'); }
        });""")
        self.page.emulate_media(color_scheme='dark')
        self.page.reload()
        self.connect()
        self.page.locator('[data-tab="settings"]').click()
        expect(self.page.get_by_label('Appearance', exact=True)).to_have_value('system')
        expect(self.page.locator('html')).to_have_css('background-color', 'rgb(23, 28, 25)')

    def open_history(self):
        self.connect()
        self.page.locator('[data-tab="history"]').click()
        expect(self.page.locator('#historyExportJson')).to_be_enabled(timeout=10000)

    def test_cleaning_guidance_matches_brewer(self):
        self.connect()
        guide = self.page.locator('article').filter(has=self.page.get_by_role('heading', name='Cleaning and maintenance'))
        expect(guide).to_contain_text('cleaning tablet in the center of the step filter')
        expect(guide).to_contain_text('Run Deep Clean after descaling.')

    def test_long_device_identity_stays_inside_sidebar_and_version_is_visible(self):
        with base.LOCK:
            _, status = base.backend.request('GET', '/api/v1/status')
        identity = '0123456789abcdef' * 4
        status['device_uid'] = identity
        self.page.route('**/api/v1/status', lambda route: route.fulfill(json=status))
        self.connect()
        expect(self.page.locator('#firmwareVersion')).to_have_text('Firmware 1.0.0')
        expect(self.page.locator('#modelName')).to_have_text('Pico C brewing console')
        expect(self.page).to_have_title('Pico C · Pico Local')
        expect(self.page.locator('#deviceId')).to_have_text('Device ' + identity)
        for width in (1440, 1100):
            self.page.set_viewport_size({'width': width, 'height': 1000})
            self.assertTrue(self.page.locator('#deviceId').evaluate('''node => {
              const sidebar=node.closest('nav').getBoundingClientRect();
              const range=document.createRange();range.selectNodeContents(node);
              return [...range.getClientRects()].every(rect =>
                rect.left >= sidebar.left && rect.right <= sidebar.right);
            }'''))
            self.assertFalse(self.page.evaluate('document.documentElement.scrollWidth > innerWidth'))

    def test_c_identity_is_consistent_for_the_alternate_display(self):
        with base.LOCK:
            _, info = base.backend.request('GET', '/api/v1')
            _, status = base.backend.request('GET', '/api/v1/status')
        for data in (info, status):
            data.update(model='pico-c', model_name='Pico C', model_family='pico-c', display_variant='ssd1309')
        self.page.route('**/api/v1', lambda route: route.fulfill(json=info))
        self.page.route('**/api/v1/status', lambda route: route.fulfill(json=status))
        self.connect()
        expect(self.page.locator('#modelName')).to_have_text('Pico C brewing console')
        expect(self.page).to_have_title('Pico C · Pico Local')

    def test_hub_connection_is_read_only_and_updates_from_device(self):
        config = {'enabled': False, 'host': '', 'port': 8080,
                  'status': {'state': 'disabled', 'sent': 0, 'failed': 0, 'dropped': 0,
                             'last_http_status': 0, 'storage_error': False}}
        requests = []

        def telemetry(route):
            request = route.request
            self.assertEqual(request.headers.get('authorization'), 'Bearer ' + TEST_KEY)
            self.assertEqual(request.url, self.base + '/api/v1/telemetry')
            requests.append((request.method, request.post_data_json if request.method == 'PUT' else None))
            if request.method == 'PUT':
                config.update(request.post_data_json)
                config['status']['state'] = 'ready'
            route.fulfill(json=config)

        self.page.route('**/api/v1/telemetry', telemetry)
        self.connect()
        self.page.locator('[data-tab="settings"]').click()
        expect(self.page.locator('#forwardingHeading')).to_have_text('Hub connection')
        expect(self.page.locator('#telemetryStatus')).to_contain_text('connect automatically')
        expect(self.page.locator('#hubDestination')).to_have_text('No Hub destination configured.')
        expect(self.page.locator('#telemetryForm, #telemetryHost, #telemetryPort, #saveTelemetry')).to_have_count(0)
        self.assertTrue(all(method == 'GET' for method, _ in requests))
        for name, width, height in [('desktop', 1440, 1000), ('wide', 1920, 1080),
                                    ('mobile', 390, 844)]:
            self.page.set_viewport_size({'width': width, 'height': height})
            self.assertFalse(self.page.evaluate('document.documentElement.scrollWidth > innerWidth'))
            self.page.screenshot(path=str(self.output / ('settings-' + name + '.png')), full_page=True)
        # Simulate the Hub pairing through the retained management API.
        config.update(enabled=True, host='brewbox.local', port=8123)
        config['status']['state'] = 'ready'
        self.page.evaluate('refreshTelemetry()')
        expect(self.page.locator('#hubDestination')).to_have_text('Sending live readings to brewbox.local:8123.')
        expect(self.page.locator('#telemetryStatus')).to_contain_text('Ready to send readings.')
        self.assertTrue(all(method == 'GET' for method, _ in requests))

    def test_complete_recording_pages_gaps_selection_and_exports(self):
        self.history()
        self.open_history()
        expect(self.page.locator('#historySummary')).to_contain_text('70 readings (70 saved)')
        expect(self.page.locator('#historySummary')).to_contain_text('1 gaps')
        self.assertEqual([path.rsplit('/', 1)[1] for path, _, _ in self.history_requests if '/samples/' in path],
                         ['0', '32', '64'])
        expect(self.page.locator('#historySession option')).to_have_count(2)
        expect(self.page.locator('#historyPhases')).to_contain_text('Holding')
        self.page.locator('#historySample').evaluate("input => {input.value=40;input.dispatchEvent(new Event('input'));}")
        details = json.loads(self.page.locator('#historyValues').text_content())
        self.assertEqual(details['elapsed_ms'], 600000)
        self.assertTrue(details['sampling_gap'])
        self.assertTrue(self.page.locator('#historyChart').evaluate("""canvas => {
          const pixels=canvas.getContext('2d').getImageData(0,0,canvas.width,canvas.height).data;
          for(let i=0;i<pixels.length;i+=4) if(pixels[i]===243 && pixels[i+1]===215 && pixels[i+2]===203) return true;
          return false;
        }"""))
        with self.page.expect_download() as download:
            self.page.locator('#historyExportJson').click()
        saved = json.loads(Path(download.value.path()).read_text())
        self.assertEqual(saved['metadata']['id'], 7)
        self.assertEqual(saved['samples'], self.histories[7]['rows'])
        with self.page.expect_download() as download:
            self.page.locator('#historyExportCsv').click()
        table = list(csv.reader(io.StringIO(Path(download.value.path()).read_text())))
        self.assertEqual(table[0], FIELDS)
        self.assertEqual(len(table), 71)
        self.assertEqual(table[-1][0], '1035000')
        self.page.locator('#historySession').select_option('6')
        expect(self.page.locator('#historySummary')).to_contain_text('5 readings (5 saved)')
        expect(self.page.locator('#historyReadout')).to_contain_text('Wort: 50.0°F')
        self.page.screenshot(path=str(self.output / 'history-desktop.png'), full_page=True)
        self.page.set_viewport_size({'width': 390, 'height': 844})
        self.assertFalse(self.page.evaluate('document.documentElement.scrollWidth > innerWidth'))
        self.page.screenshot(path=str(self.output / 'history-mobile.png'), full_page=True)

    def test_history_refresh_eviction_never_mixes_brews(self):
        self.history(evict_after_first_page=True)
        self.open_history()
        expect(self.page.locator('#historySession')).to_have_value('8')
        expect(self.page.locator('#historySummary')).to_contain_text('70 readings')
        with self.page.expect_download() as download:
            self.page.locator('#historyExportJson').click()
        saved = json.loads(Path(download.value.path()).read_text())
        self.assertEqual(saved['metadata']['id'], 8)
        self.assertEqual(saved['samples'], self.histories[8]['rows'])
        self.assertTrue(any(query.get('id') == ['7'] and path.endswith('/32')
                            for path, query, _ in self.history_requests))

    def test_failed_brew_selection_cannot_export_the_previously_selected_brew(self):
        self.history()
        self.open_history()
        self.page.route('**/api/v1/history/samples/*?id=6', lambda route:
                        route.fulfill(status=503, json={'message': 'Recorded samples could not be read.'}))
        self.page.locator('#historySession').select_option('6')
        expect(self.page.locator('#historySummary')).to_have_text('Recorded samples could not be read.')
        expect(self.page.locator('#historyExportJson')).to_be_disabled()
        expect(self.page.locator('#historyExportCsv')).to_be_disabled()
        expect(self.page.locator('#historyValues')).to_have_text('')
        self.page.locator('#historySession').select_option('7')
        expect(self.page.locator('#historyExportJson')).to_be_enabled()
        expect(self.page.locator('#historySummary')).to_contain_text('Brew 7')

    def test_history_uses_shared_queue_and_discards_disconnected_work(self):
        self.history()
        self.connect()
        self.page.evaluate('() => {requestQueue=new Promise(resolve=>{window.releaseHistoryQueue=resolve;});}')
        self.page.locator('[data-tab="history"]').click()
        self.page.wait_for_timeout(100)
        self.assertEqual(self.history_requests, [])
        self.page.locator('[data-tab="settings"]').click()
        self.page.locator('#disconnectButton').click()
        self.page.evaluate('releaseHistoryQueue()')
        self.page.wait_for_timeout(100)
        self.assertEqual(self.history_requests, [])
        expect(self.page.locator('[data-tab="history"]')).to_be_hidden()
        expect(self.page.locator('#historyExportJson')).to_be_disabled()
        expect(self.page.locator('#historyValues')).to_have_text('')

    def test_history_units_do_not_change_canonical_export(self):
        self.history()
        self.open_history()
        self.page.locator('[data-tab="settings"]').click()
        self.page.locator('#temperatureUnit').select_option('C')
        self.page.locator('#saveSettings').click()
        expect(self.page.locator('#notice')).to_have_text('Preferences saved on the Pico.')
        self.page.locator('[data-tab="history"]').click()
        expect(self.page.locator('#historyReadout')).to_contain_text('Wort: 21.1°C')
        expect(self.page.locator('#historyExportJson')).to_be_enabled()
        with self.page.expect_download() as download:
            self.page.locator('#historyExportJson').click()
        saved = json.loads(Path(download.value.path()).read_text())
        self.assertEqual(saved['samples'][0][1], 70)

    def test_explorer_catalog_and_parameterized_history_request(self):
        self.history()
        self.connect()
        self.page.locator('[data-tab="api"]').click()
        spec = json.loads((Path(__file__).resolve().parents[1] / 'docs/openapi.json').read_text())
        expected = {method.upper() + ' ' + path for path, item in spec['paths'].items()
                    for method in item if method in {'get', 'put', 'post', 'patch', 'delete', 'head', 'options'}}
        expected = {entry for entry in expected if '/pairing/' not in entry}
        expected.remove('PUT /api/v1/telemetry')  # Hub-managed, never edited by this website.
        actual = set(self.page.locator('#apiEndpoint option').evaluate_all('options=>options.map(option=>option.value)'))
        self.assertEqual(actual, expected)
        self.page.locator('#apiEndpoint').select_option('GET /api/v1/history/samples/{offset}')
        self.page.locator('#apiParameter').fill('32')
        self.page.locator('#apiParameter1').fill('7')
        self.page.locator('#apiSend').click()
        expect(self.page.locator('#apiStatus')).to_contain_text('HTTP 200')
        result = json.loads(self.page.locator('#apiResponse').text_content())
        self.assertEqual((result['id'], result['offset']), (7, 32))
        self.page.locator('#apiParameter1').fill('99')
        self.page.locator('#apiSend').click()
        expect(self.page.locator('#apiStatus')).to_contain_text('HTTP 409')
        expect(self.page.locator('#apiResponse')).to_contain_text('history_changed')
        requests = len(self.history_requests)
        self.page.locator('#apiParameter').fill('-1')
        self.page.locator('#apiSend').click()
        expect(self.page.locator('#apiStatus')).to_contain_text('Invalid offset')
        self.assertEqual(len(self.history_requests), requests)
        self.page.locator('#apiEndpoint').select_option('POST /api/v1/commands')
        actual_commands = set(self.page.locator('#apiExample option').evaluate_all('options=>options.map(option=>option.value)'))
        self.assertEqual(actual_commands, set(spec['components']['schemas']['CommandRequest']['discriminator']['mapping']))
        self.assertTrue(all(headers.get('authorization') == 'Bearer ' + TEST_KEY
                            for _, _, headers in self.history_requests))


if __name__ == '__main__':
    unittest.main(verbosity=2)
