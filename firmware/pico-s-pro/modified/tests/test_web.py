#!/usr/bin/env python3
"""Exercise the browser against the real portable C API/store with simulated I/O.

The local HTTP harness supplies authentication and static assets; production
HTTP framing/auth and SimpleLink bindings have their own sanitizer suites.
No physical brewing, Wi-Fi or flash behavior is exercised here.
"""
import importlib.util
import base64
import json
import os
from pathlib import Path
import shutil
import threading
import unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from playwright.sync_api import sync_playwright, expect

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("local_api_tests", ROOT / "tests/test_local_api.py")
backend = importlib.util.module_from_spec(spec)
spec.loader.exec_module(backend)
# Deliberately public test-only credential. Never written to a firmware artifact.
TEST_KEY = "a" * 64
LOCK = threading.Lock()


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def handle_request(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
        encoding = None
        if self.path.startswith("/api/"):
            if self.path != "/api/v1/overview" and self.headers.get("Authorization") != "Bearer " + TEST_KEY:
                code, data = 401, {"error": "unauthorized", "message": "Invalid device key"}
            else:
                with LOCK:
                    code, data = backend.request(self.command, self.path, raw=body)
            payload = json.dumps(data).encode() if data is not None else b""
            mime = "application/json"
        else:
            assets = {"/": ("index.html", "text/html"), "/app.js": ("app.js", "text/javascript"),
                      "/style.css": ("style.css", "text/css"),
                      "/api-explorer.js": ("api-explorer.js", "text/javascript"),
                      "/history.js": ("history.js", "text/javascript")}
            if self.path not in assets:
                code, payload, mime = 404, b"", "text/plain"
            else:
                name, mime = assets[self.path]
                payload = (ROOT / "firmware/web" / (name + ".gz")).read_bytes()
                encoding, code = "gzip", 200
        self.send_response(code)
        self.send_header("Content-Type", mime)
        self.send_header("Content-Length", str(len(payload)))
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; object-src 'none'; frame-ancestors 'none'; base-uri 'none'")
        if encoding:
            self.send_header("Content-Encoding", encoding)
        self.end_headers()
        self.wfile.write(payload)

    do_GET = do_POST = do_PUT = do_PATCH = do_DELETE = handle_request


class BrowserWorkflow(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server = HTTPServer(("127.0.0.1", 0), Handler)
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()
        cls.base = "http://127.0.0.1:" + str(cls.server.server_port)
        cls.playwright = sync_playwright().start()
        executable = os.environ.get("PICOBREW_CHROMIUM") or shutil.which("chromium")
        cls.browser = cls.playwright.chromium.launch(executable_path=executable, headless=True,
                                                     args=["--no-sandbox"])
        cls.output = ROOT / "build/web-preview"
        cls.output.mkdir(parents=True, exist_ok=True)

    @classmethod
    def tearDownClass(cls):
        cls.browser.close()
        cls.playwright.stop()
        cls.server.shutdown()
        cls.server.server_close()
        cls.thread.join()

    def setUp(self):
        with LOCK:
            backend.LIB.test_reset(1)
            backend.request("PUT", "/api/v1/recipes/house-ale", backend.recipe())
        self.context = self.browser.new_context(viewport={"width": 1440, "height": 1000})
        self.page = self.context.new_page()
        self.errors = []
        self.page.on("pageerror", lambda error: self.errors.append(str(error)))
        self.page.goto(self.base)

    def tearDown(self):
        self.context.close()
        self.assertEqual(self.errors, [])

    def connect(self):
        if not self.page.locator("#connectDialog").is_visible():
            self.page.locator("#connectButton").click()
        self.page.locator("#apiKey").fill(TEST_KEY)
        self.page.locator("#submitConnect").click()
        expect(self.page.locator("#connectionText")).to_have_text("Connected")
        expect(self.page.locator("#connectDialog")).not_to_be_visible()

    def test_offline_favicon_uses_the_original_icon(self):
        href = self.page.locator('link[rel="icon"]').get_attribute("href")
        self.assertEqual(href, "data:image/x-icon;base64," + base64.b64encode(
            (ROOT / "web/favicon.ico").read_bytes()).decode("ascii"))
        # Decoding an image exercises the same img-src policy used for the tab icon.
        self.assertTrue(self.page.evaluate("""async (href) => {
            const icon = new Image();
            icon.src = href;
            await icon.decode();
            return icon.naturalWidth > 0;
        }""", href))

    def test_pair_using_dial_without_entering_a_key(self):
        requests = []
        def pairing(route):
            operation = route.request.url.rsplit("/", 1)[-1]
            nonce = route.request.post_data_json["nonce"]
            self.assertRegex(nonce, "^[0-9a-f]{64}$")
            self.assertNotIn("authorization", route.request.headers)
            requests.append((operation, nonce))
            if operation == "request":
                route.fulfill(status=202, json={"state": "pending", "code": nonce[:8]})
            elif len(requests) == 2:
                route.fulfill(status=202, json={"state": "pending"})
            else:
                route.fulfill(status=200, json={"api_key": TEST_KEY})
        self.page.route("**/api/v1/pairing/**", pairing)
        self.page.locator("#connectButton").click()
        self.page.locator("#pairButton").click()
        expect(self.page.locator("#connectError")).to_contain_text("Confirm code")
        expect(self.page.locator("#connectionText")).to_have_text("Connected")
        self.assertEqual([row[0] for row in requests], ["request", "claim", "claim"])
        self.assertEqual(len({row[1] for row in requests}), 1)
        self.assertEqual(self.page.evaluate("sessionStorage.getItem('pico-api-key')"), TEST_KEY)

    def test_hub_pairing_link_is_removed_before_redemption(self):
        nonce = "ab" * 32
        paths = []
        def claim(route):
            self.assertEqual(route.request.post_data_json, {"nonce": nonce})
            paths.append(self.page.url)
            route.fulfill(status=200, json={"api_key": TEST_KEY})
        self.page.route("**/api/v1/pairing/claim", claim)
        self.page.goto("about:blank")
        self.page.goto(self.base + "/#pair=" + nonce)
        expect(self.page.locator("#connectionText")).to_have_text("Connected")
        self.assertEqual(len(paths), 1)
        self.assertNotIn("#", paths[0])
        self.assertNotIn(nonce, self.page.url)

    def test_initial_frame_and_connection_result(self):
        scripts, overviews = [], []
        self.page.route("**/app.js", lambda route: scripts.append(route))
        self.page.route("**/api/v1/overview", lambda route: overviews.append(route))
        self.page.reload(wait_until="commit")
        pending = self.page.locator("#publicOverview")
        expect(pending).to_be_visible()
        for width in (2560, 1440, 390):
            self.page.set_viewport_size({"width": width, "height": 900})
            self.assertGreater(pending.bounding_box()["width"], min(width, 1400) * 0.85)
            expect(self.page.locator("#connectionText")).to_have_text("Connecting…")
            expect(self.page.locator("#connectButton")).to_be_hidden()
        scripts[0].continue_()
        self.page.wait_for_load_state("domcontentloaded")
        expect(self.page.locator("#connectionText")).to_have_text("Connecting…")
        overviews[0].fulfill(status=503, json={"error": "unavailable"})
        expect(self.page.locator("#publicState")).to_have_text("Machine status unavailable.")
        expect(self.page.locator("#connectButton")).to_be_visible()
        self.page.unroute("**/api/v1/overview")
        self.connect()

    def test_navigation_and_settings_actions(self):
        self.connect()
        nav = self.page.get_by_role("navigation")
        expect(nav.get_by_role("button")).to_have_text(["Brewery", "Recipes 1", "Brew journal", "Settings", "API explorer"])
        nav.get_by_role("button", name="Settings").click()
        expect(nav.get_by_role("button", name="Settings")).to_have_attribute("aria-current", "page")
        browser_access = self.page.get_by_role("region", name="Browser access")
        expect(browser_access.get_by_role("button")).to_have_text(["Disconnect"])
        expect(self.page.get_by_role("region", name="Machine controls").get_by_role("button")).to_have_text(["Restart brewer"])
        for width in (2560, 1440, 390):
            self.page.set_viewport_size({"width": width, "height": 900})
            heading = browser_access.get_by_role("heading").bounding_box()
            action = browser_access.get_by_role("button").bounding_box()
            self.assertAlmostEqual(heading["x"], action["x"], delta=2)
            self.assertLess(action["y"] - heading["y"], 120)
            self.assertFalse(self.page.evaluate("document.documentElement.scrollWidth > innerWidth"))

    def test_connection_live_frame_and_desktop_layout(self):
        expect(self.page.locator("#publicTemperatures")).to_contain_text("72.4°F")
        expect(self.page.locator("#startRecipe")).to_be_disabled()
        self.connect()
        expect(self.page.locator("#wortTemperature")).to_have_text("72.4°F")
        expect(self.page.locator("#displayHint")).to_have_text("LIVE OLED · 128 × 64")
        pixels = self.page.locator("#display").evaluate("c => Array.from(c.getContext('2d').getImageData(0,63,2,1).data)")
        self.assertEqual(pixels, [199, 225, 180, 255, 18, 38, 32, 255])
        self.assertEqual(self.page.evaluate("sessionStorage.getItem('pico-api-key')"), TEST_KEY)
        self.assertFalse(self.page.evaluate("document.documentElement.scrollWidth > innerWidth"))
        self.page.screenshot(path=str(self.output / "overview-desktop.png"), full_page=True)

    def test_oled_panel_bytes_render_in_upright_screen_coordinates(self):
        # Independent panel-byte landmarks: corners, page boundary and
        # nonzero bit offsets distinguish a half-turn from either reflection.
        raw = bytearray(1024)
        raw[1023], raw[1021], raw[772], raw[110] = 0x80, 0x10, 0x40, 0x20
        frame = {"width": 128, "height": 64, "format": "page-lsb-hex", "data": raw.hex()}
        self.page.route("**/api/v1/display", lambda route: route.fulfill(json=frame))
        self.connect()
        expect(self.page.locator("#displayHint")).to_have_text("LIVE OLED · 128 × 64")
        lit = self.page.locator("#display").evaluate("""canvas => {
          const data = canvas.getContext('2d').getImageData(0,0,128,64).data;
          const result = [];
          for (let y=0;y<64;y++) for (let x=0;x<128;x++) {
            const at=(y*128+x)*4;
            if (data[at]===199 && data[at+1]===225 && data[at+2]===180)
              result.push([x,y]);
          }
          return result;
        }""")
        self.assertEqual(lit, [[0, 0], [2, 3], [123, 9], [17, 58]])

    def test_recipe_save_load_export_and_empty_delete_response(self):
        self.connect()
        self.page.locator('[data-tab="recipes"]').click()
        self.page.locator("#newRecipe").click()
        self.page.locator("#recipeName").fill("Browser test ale")
        self.page.locator("#saveRecipe").click()
        expect(self.page.locator("#notice")).to_have_text("Recipe saved on the Pico.")
        expect(self.page.locator("#recipeCapacity")).to_have_text("2 / 16")
        self.page.locator(".recipe-item").filter(has_text="Browser test ale").click()
        expect(self.page.locator("#recipeName")).to_have_value("Browser test ale")
        with self.page.expect_download() as download:
            self.page.locator("#exportRecipe").click()
        document = json.loads(Path(download.value.path()).read_text())
        self.assertEqual(document["id"], "browser-test-ale")
        self.assertEqual(document["steps"][0]["target_f"], 152)
        self.assertEqual(document["steps"][0]["location"], 2)
        expect(self.page.locator('[data-field="location"] option:checked')).to_have_text("Mash")
        self.page.locator("#newRecipe").click()
        self.page.locator("#recipeName").fill("Browser-Test Ale")
        self.page.locator('[data-field="minutes"]').fill("3")
        self.page.locator("#saveRecipe").click()
        expect(self.page.locator("#notice")).to_contain_text("Recipe ID is already in use")
        expect(self.page.locator('[data-field="minutes"]')).to_have_value("3")
        with LOCK:
            self.assertEqual(backend.request("GET", "/api/v1/recipes/browser-test-ale")[1]["steps"][0]["minutes"], 60)
        self.page.once("dialog", lambda dialog: dialog.accept())
        self.page.locator(".recipe-item").filter(has_text="Browser test ale").click()
        self.page.screenshot(path=str(self.output / "recipes-desktop.png"), full_page=True)
        self.page.once("dialog", lambda dialog: dialog.accept())
        self.page.locator("#deleteRecipe").click()
        expect(self.page.locator("#notice")).to_have_text("Recipe deleted.")
        expect(self.page.locator("#recipeCapacity")).to_have_text("1 / 16")

    def test_stale_editor_keeps_draft_and_newer_saved_recipe(self):
        path = "/api/v1/recipes/house-ale"
        self.connect()
        self.page.locator('[data-tab="recipes"]').click()
        self.page.locator(".recipe-item").filter(has_text="House ale").click()
        expect(self.page.locator("#editorHeading")).to_have_text("Edit recipe")
        self.page.locator("#recipeName").fill("My draft")
        with LOCK:
            backend.request("PUT", path, backend.recipe("Other brewer's edit"))
        self.page.locator("#saveRecipe").click()
        expect(self.page.locator("#notice")).to_contain_text("Reopen this recipe")
        expect(self.page.locator("#recipeName")).to_have_value("My draft")
        self.page.once("dialog", lambda dialog: dialog.accept())
        self.page.locator("#deleteRecipe").click()
        expect(self.page.locator("#notice")).to_contain_text("Reopen this recipe")
        with LOCK:
            self.assertEqual(backend.request("GET", path)[1]["name"], "Other brewer's edit")

    def test_import_rejects_unrepresentable_steps_before_touching_the_editor(self):
        self.connect()
        self.page.locator('[data-tab="recipes"]').click()
        self.page.locator('#newRecipe').click()
        title = self.page.locator('#recipeName')
        title.fill("Keep these edits")
        recipe = dict(id="imported", **backend.recipe("Valid recipe"))
        step = recipe['steps'][0]
        for steps in ([{**step, 'minutes': "10"}], [{**step, 'name': 'x' * 22}],
                      [{**step, 'target_f': 45}], [{**step, 'location': 7}],
                      [{**step, 'drain_minutes': None}], [{**step, 'pause': True}]):
            body = dict(recipe, steps=steps)
            self.page.locator('#importFile').set_input_files({
                "name": "invalid.json", "mimeType": "application/json", "buffer": json.dumps(body).encode()})
            expect(self.page.locator('#notice')).to_contain_text('Step 1')
            expect(title).to_have_value('Keep these edits')
        wire = json.dumps(recipe).replace('"minutes":', r'"min\u0075tes": 0, "minutes":', 1)
        self.page.locator('#importFile').set_input_files({
            "name": "repeated.json", "mimeType": "application/json", "buffer": wire.encode()})
        expect(self.page.locator('#notice')).to_contain_text('repeated field: minutes')
        expect(title).to_have_value('Keep these edits')
        self.page.once('dialog', lambda dialog: dialog.accept())
        body = dict(recipe, steps=[{k:v for k,v in step.items() if k != 'drain_minutes'}])
        self.page.locator('#importFile').set_input_files({
            "name": "valid.json", "mimeType": "application/json", "buffer": json.dumps(body).encode()})
        expect(title).to_have_value('Valid recipe')
        expect(self.page.locator('input[data-field="drain_minutes"]').first).to_have_value('0')

    def test_recipe_text_fields_enforce_the_device_character_set(self):
        self.connect()
        self.page.locator('[data-tab="recipes"]').click()
        self.page.locator('#newRecipe').click()
        new_id = self.page.locator('#recipeId')
        new_id.fill('my-recipe_1')
        self.assertTrue(new_id.evaluate('e => e.checkValidity()'))
        new_id.fill('my recipe')
        self.assertFalse(new_id.evaluate('e => e.checkValidity()'))
        for selector in ('#recipeName', '#steps input[data-field="name"]'):
            field = self.page.locator(selector).first
            field.fill('Märzen')
            self.assertFalse(field.evaluate('e => e.checkValidity()'))
            field.fill('Marzen (dark)')
            self.assertTrue(field.evaluate('e => e.checkValidity()'))

    def test_control_state_and_restart_follow_firmware(self):
        with LOCK:
            backend.LIB.test_ready(0)
        self.connect()
        expect(self.page.locator("#modeBadge")).to_have_text("NOT READY")
        expect(self.page.locator("#encoderClick")).to_be_disabled()
        with LOCK:
            backend.LIB.test_ready(1)
            backend.LIB.test_restart_state(4, False, -1)
        expect(self.page.locator("#encoderClick")).to_be_enabled()
        with self.page.expect_response("**/api/v1/commands"):
            self.page.evaluate("""() => {
                const button = document.getElementById('encoderClick');
                button.click(); button.click();
                if (!button.disabled || document.getElementById('stopButton').disabled)
                    throw Error('Dial must wait for its command; stop must stay available');
            }""")
        self.page.locator('[data-tab="settings"]').click()
        restart = self.page.locator('#restartBrewer')
        expect(restart).to_be_enabled()
        self.page.once("dialog", lambda dialog: dialog.dismiss())
        restart.click()
        with LOCK:
            self.assertFalse(backend.LIB.test_restart_pending())
            backend.LIB.test_busy(1)
        expect(restart).to_be_disabled()
        with LOCK:
            backend.LIB.test_busy(0)
        expect(restart).to_be_enabled()
        self.page.once("dialog", lambda dialog: dialog.accept())
        with self.page.expect_response("**/api/v1/system/restart") as response:
            restart.click()
        self.assertEqual(response.value.status, 202)
        with LOCK:
            self.assertTrue(backend.LIB.test_restart_pending())

    def test_program_encoder_stop_and_idle_only_clear(self):
        self.connect()
        for name, arrow, delta in [("Move up", "↑", -1), ("Move down", "↓", 1)]:
            button = self.page.get_by_role("button", name=name, exact=True)
            expect(button).to_have_text(arrow)
            with self.page.expect_response(lambda response: response.url.endswith("/api/v1/commands")):
                button.click()
            expect(self.page.locator("#notice")).to_be_hidden()
            with LOCK:
                self.assertEqual(backend.LIB.test_last_delta(), delta)
        self.page.locator("#startRecipe").click()
        expect(self.page.locator("#startRecipe")).to_be_disabled()
        self.page.locator("#stopButton").click()
        expect(self.page.locator("#modeBadge")).to_have_text("STOPPED")
        expect(self.page.locator("#clearStop")).to_be_disabled()
        with LOCK:
            backend.LIB.test_busy(0)  # Simulated workflow has unwound.
            backend.LIB.test_restart_state(4, False, -1)  # Idle phase, outputs off.
        expect(self.page.locator("#clearStop")).to_be_enabled(timeout=8000)
        self.page.locator("#clearStop").click()
        expect(self.page.locator("#modeBadge")).to_have_text("READY")
        self.page.locator("#program").select_option(label="Deep Clean")
        self.page.locator("#runProgram").click()
        expect(self.page.locator("#modeBadge")).to_have_text("IN PROGRESS")

    def test_restore_starters_edit_zero_minute_step_and_preserve_changes(self):
        self.connect()
        self.page.locator('[data-tab="recipes"]').click()
        self.page.once("dialog", lambda dialog: dialog.accept())
        with self.page.expect_request("**/api/v1/recipes/restore-defaults") as request:
            self.page.locator("#restoreRecipes").click()
        self.assertEqual(request.value.method, "POST")
        self.assertEqual(request.value.headers["content-type"], "application/json")
        expect(self.page.locator("#recipeCapacity")).to_have_text("9 / 16")
        self.page.locator(".recipe-item").filter(has_text="Pico Pale").click()
        expect(self.page.locator('#steps input[data-field="minutes"]').first).to_have_value("0")
        self.page.locator("#recipeName").fill("My Pico Pale")
        self.page.locator("#saveRecipe").click()
        expect(self.page.locator("#notice")).to_have_text("Recipe saved on the Pico.")
        self.page.once("dialog", lambda dialog: dialog.accept())
        self.page.locator("#restoreRecipes").click()
        expect(self.page.locator("#notice")).to_have_text("All starter recipes are already on the Pico.")
        expect(self.page.locator("#recipeName")).to_have_value("My Pico Pale")
        for width in (1440, 390):
            self.page.set_viewport_size({"width": width, "height": 900})
            self.assertFalse(self.page.evaluate("document.documentElement.scrollWidth > innerWidth"))
        self.page.screenshot(path=str(self.output / "starter-recipes-mobile.png"), full_page=True)

    def test_settings_and_disconnect(self):
        self.connect()
        self.page.locator('[data-tab="settings"]').click()
        self.page.locator("#temperatureUnit").select_option("C")
        self.page.locator("#language").select_option("fr")
        self.page.locator("#detailedStatus").check()
        self.page.locator("#saveSettings").click()
        expect(self.page.locator("#notice")).to_have_text("Preferences saved on the Pico.")
        self.assertEqual(self.page.locator('[data-tab="activity"]').count(), 0)
        self.assertEqual(self.page.locator('#exportEvents').count(), 0)
        self.page.locator('[data-tab="overview"]').click()
        expect(self.page.locator("#wortTemperature")).to_have_text("22.4°C")
        self.page.locator('[data-tab="settings"]').click()
        self.page.locator("#disconnectButton").click()
        expect(self.page.locator("#connectionText")).to_have_text("Disconnected")
        self.assertIsNone(self.page.evaluate("sessionStorage.getItem('pico-api-key')"))

    def test_auth_failure_and_untrusted_recipe_label(self):
        self.page.locator("#connectButton").click()
        self.page.locator("#apiKey").fill("b" * 64)
        self.page.locator("#submitConnect").click()
        expect(self.page.locator("#connectError")).to_have_text("Invalid device key")
        expect(self.page.locator("#startRecipe")).to_be_disabled()
        with LOCK:
            backend.request("PUT", "/api/v1/recipes/markup", backend.recipe("<img src=x onerror=alert(1)>"))
        self.connect()
        self.page.locator('[data-tab="recipes"]').click()
        expect(self.page.locator("#recipeList")).to_contain_text("<img src=x onerror=alert(1)>")
        self.assertEqual(self.page.locator("#recipeList img").count(), 0)

    def test_mobile_layout_and_import(self):
        self.page.set_viewport_size({"width": 390, "height": 844})
        self.connect()
        self.assertFalse(self.page.evaluate("document.documentElement.scrollWidth > innerWidth"))
        self.page.screenshot(path=str(self.output / "overview-mobile.png"), full_page=True)
        self.page.locator('[data-tab="recipes"]').click()
        document = dict(id="imported", **backend.recipe("Imported recipe"))
        self.page.locator("#importFile").set_input_files({"name": "recipe.json", "mimeType": "application/json",
                                                        "buffer": json.dumps(document).encode()})
        expect(self.page.locator("#recipeName")).to_have_value("Imported recipe")
        self.page.locator("#saveRecipe").click()
        expect(self.page.locator("#notice")).to_have_text("Recipe saved on the Pico.")
        self.assertFalse(self.page.evaluate("document.documentElement.scrollWidth > innerWidth"))
        self.page.screenshot(path=str(self.output / "recipes-mobile.png"), full_page=True)

        # Legal labels need not contain spaces: neither a maximum-length name
        # nor its ID should force the two-column mobile recipe list wider.
        long_name, long_id = "W" * 31, "w" * 20
        document = dict(id=long_id, **backend.recipe(long_name))
        self.page.locator("#importFile").set_input_files({"name": "long-recipe.json", "mimeType": "application/json",
                                                        "buffer": json.dumps(document).encode()})
        expect(self.page.locator("#recipeName")).to_have_value(long_name)
        self.page.locator("#saveRecipe").click()
        expect(self.page.locator("#notice")).to_have_text("Recipe saved on the Pico.")
        item = self.page.locator(".recipe-item").filter(has_text=long_name)
        expect(item.locator("strong")).to_have_text(long_name)
        expect(item.locator("small")).to_contain_text(long_id)
        self.assertTrue(item.evaluate("el => el.scrollWidth <= el.clientWidth"))
        self.assertFalse(self.page.evaluate("document.documentElement.scrollWidth > innerWidth"))
        self.page.screenshot(path=str(self.output / "recipes-mobile-long-label.png"), full_page=True)


if __name__ == "__main__":
    unittest.main(verbosity=2)
