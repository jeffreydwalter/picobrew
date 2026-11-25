"""Native requests against the actual common engine, with fault-injected ports."""
import ctypes
import json
import pathlib
import subprocess
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
BUILD = ROOT / "build"


def build():
    BUILD.mkdir(exist_ok=True)
    sources = [ROOT / "src" / name for name in (
        "engine.cpp", "api.cpp", "values.cpp", "records.cpp", "history.cpp", "history_records.cpp", "telemetry.cpp")]
    library = BUILD / "libengine_test.so"
    transport = BUILD / "http_server_test.o"
    subprocess.run(["gcc", "-std=c11", "-O1", "-Wall", "-Wextra", "-Werror", "-fPIC",
                    "-I" + str(ROOT / "include"), "-c", str(ROOT / "src/http_server.c"), "-o", str(transport)], check=True)
    subprocess.run(["g++", "-std=c++11", "-O1", "-g", "-Wall", "-Wextra", "-Werror", "-shared", "-fPIC",
                    "-I" + str(ROOT / "include"), "-I" + str(ROOT / "src"),
                    *map(str, sources), str(ROOT / "tests" / "engine_bridge.cpp"), str(transport), "-o", str(library)], check=True)
    lib = ctypes.CDLL(str(library))
    signatures = {
        "world_new": (ctypes.c_void_p, [ctypes.c_uint]), "world_delete": (None, [ctypes.c_void_p]),
        "world_status": (ctypes.c_uint, [ctypes.c_void_p]),
        "world_request": (ctypes.c_char_p, [ctypes.c_void_p, ctypes.c_uint, ctypes.c_char_p, ctypes.c_char_p, ctypes.c_uint]),
        "world_tick": (None, [ctypes.c_void_p, ctypes.c_uint, ctypes.c_uint]),
        "world_reboot": (None, [ctypes.c_void_p]),
        "world_fault": (None, [ctypes.c_void_p, ctypes.c_uint, ctypes.c_uint]),
        "world_file": (None, [ctypes.c_void_p, ctypes.c_char_p, ctypes.c_uint]),
        "world_metric": (ctypes.c_uint, [ctypes.c_void_p, ctypes.c_uint]),
        "world_sent": (ctypes.c_char_p, [ctypes.c_void_p]),
        "world_response": (None, [ctypes.c_void_p, ctypes.c_char_p]),
        "world_prepare": (ctypes.c_uint, [ctypes.c_void_p, ctypes.c_uint]),
        "world_care": (ctypes.c_uint, [ctypes.c_void_p, ctypes.c_uint]),
        "world_cancel": (None, [ctypes.c_void_p]),
        "world_recovery": (None, [ctypes.c_void_p, ctypes.c_uint]),
        "world_preferences": (ctypes.c_uint, [ctypes.c_void_p, ctypes.c_char]),
        "world_pairing_mode": (None, [ctypes.c_void_p, ctypes.c_bool]),
        "world_http": (ctypes.c_char_p, [ctypes.c_void_p, ctypes.c_char_p, ctypes.c_bool]),
        "world_legacy_codec_test": (ctypes.c_bool, []),
    }
    for name, (restype, args) in signatures.items():
        fn = getattr(lib, name); fn.restype = restype; fn.argtypes = args
    return lib


class EngineTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.lib = build()

    def setUp(self):
        self.worlds = []
        self.world = self.new(1)

    def tearDown(self):
        for world in self.worlds:
            self.lib.world_delete(world)

    def new(self, model):
        world = self.lib.world_new(model); self.worlds.append(world)
        self.assertEqual(self.lib.world_status(world), 200)
        return world

    def request(self, path, body=None, method=0, auth=3, expected=200, world=None):
        world = world or self.world
        if body is not None and not isinstance(body, str):
            body = json.dumps(body, separators=(",", ":"))
        data = self.lib.world_request(world, method, path.encode(), body.encode() if body is not None else None, auth)
        decoded = json.loads(data)
        self.assertEqual(self.lib.world_status(world), expected, decoded)
        self.assertLess(len(data), 4096)
        return decoded

    def command(self, name, expected=202, world=None, **fields):
        return self.request("/api/v1/commands", {"command": name, **fields}, 1, expected=expected, world=world)

    def tick(self, milliseconds=15000, times=1, world=None):
        self.lib.world_tick(world or self.world, milliseconds, times)

    def fault(self, kind, value, world=None):
        self.lib.world_fault(world or self.world, kind, value)

    def file(self, name, action, world=None):
        self.lib.world_file(world or self.world, name.encode(), action)

    def recipe(self, name="Test mash", location=1):
        return {"name": name, "steps": [{"name": "Mash", "location": location, "target_f": 152, "minutes": 60, "drain_minutes": 1}]}

    def test_discovery_capabilities_and_no_fictional_routes(self):
        ferm = self.request("/api/v1")
        self.assertEqual(ferm["release_channel"], "stable")
        self.assertFalse(ferm["capabilities"]["recipes"])
        self.assertFalse(ferm["recipe_create_only"])
        self.assertTrue(self.request("/api/v1", world=self.new(0))["recipe_create_only"])
        self.assertTrue(ferm["capabilities"]["fermentation"])
        self.assertEqual(ferm["history_capacity_records"], 4224)
        self.assertEqual(ferm["command_fields"]["calibrate"]["required"], ["confirm"])
        for route in ("recipes", "display"):
            self.request("/api/v1/" + route, expected=404)
        still = self.new(2)
        status = self.request("/api/v1/status", world=still)
        self.assertEqual(len(status["sensors"]), 5)
        self.assertEqual([a["id"] for a in status["actuators"]], ["pump", "fan1", "fan2"])

    def test_device_care_is_persistent_and_a_rinse_does_not_reset_the_brew_count(self):
        def due(world):
            return self.request("/api/v1/status", world=world)["maintenance"]["due"]
        z = self.new(0)
        for _ in range(2):
            self.assertEqual(self.lib.world_care(z, 0), 200)
            self.assertEqual(due(z), ["rinse"])
            self.assertEqual(self.lib.world_care(z, 2), 200)
            self.assertEqual(due(z), [])
            self.lib.world_reboot(z)
        self.assertEqual(self.lib.world_care(z, 0), 200)
        self.assertEqual(due(z), ["rinse", "clean"])
        self.assertEqual(self.lib.world_care(z, 2), 200)
        self.assertEqual(due(z), ["clean"])
        self.assertEqual(self.lib.world_care(z, 3), 200)
        self.lib.world_reboot(z)
        self.assertEqual(due(z), [])
        ferm = self.world
        self.assertEqual(due(ferm), ["sanitize_seal"])
        self.assertEqual(self.lib.world_care(ferm, 7), 200)
        self.lib.world_reboot(ferm)
        self.assertEqual(due(ferm), [])
        self.assertEqual(self.lib.world_care(ferm, 6), 200)
        self.assertEqual(due(ferm), ["sanitize_seal"])
        still = self.new(2)
        self.assertEqual(self.lib.world_care(still, 4), 200)
        self.lib.world_reboot(still)
        self.assertEqual(due(still), ["clean_condenser"])
        self.assertEqual(self.lib.world_care(still, 5), 200)
        self.assertEqual(due(still), [])

    def test_compact_history_preserves_legacy_rows_and_integer_extremes(self):
        self.assertTrue(self.lib.world_legacy_codec_test())

    def test_auth_health_and_methods(self):
        for auth in (0, 1, 2, 3):
            health = self.request("/api/v1/health", auth=auth)
            self.assertEqual(health["auth_configured"], bool(auth & 1))
        self.request("/api/v1/status", auth=0, expected=503)
        self.request("/api/v1/status", auth=1, expected=401)
        self.request("/api/v1/commands", expected=405)
        self.request("/api/v1/unknown", expected=404)
        self.assertEqual(self.lib.world_metric(self.world, 0), 0)

    def test_recovery_metadata_and_explicit_confirmed_dispatch(self):
        self.assertIsNone(self.request("/api/v1/status")["recovery"])
        brewer = self.new(0)
        self.lib.world_recovery(brewer, 1)
        empty = self.request("/api/v1/status", world=brewer)["recovery"]
        self.assertEqual(empty, dict(available=False, valid=True, recipe_name="", step_name="", current_step=-1, step_seconds=0, elapsed_seconds=0))
        self.lib.world_recovery(brewer, 2)
        self.tick(world=brewer)
        saved = self.request("/api/v1/status", world=brewer)["recovery"]
        self.assertEqual(saved, dict(available=True, valid=True, recipe_name="Interrupted ale", step_name="Mash", current_step=2, step_seconds=1234, elapsed_seconds=0xffffffff))
        self.assertEqual(self.lib.world_metric(brewer, 9), 0)
        self.command("resume-interrupted", expected=400, world=brewer)
        self.command("resume-interrupted", expected=400, world=brewer, confirm=False)
        self.command("resume-interrupted", expected=400, world=brewer, confirm=True, recipe_id="extra")
        self.assertEqual(self.lib.world_metric(brewer, 9), 0)
        self.command("resume-interrupted", world=brewer, confirm=True)
        self.assertEqual(self.lib.world_metric(brewer, 9), 1)
        self.assertFalse(self.request("/api/v1/status", world=brewer)["recovery"]["available"])
        self.lib.world_recovery(brewer, 3)
        invalid = self.request("/api/v1/status", world=brewer)["recovery"]
        self.assertFalse(invalid["available"])
        self.assertFalse(invalid["valid"])
        self.command("resume-interrupted", expected=409, world=brewer, confirm=True)
        self.command("discard-interrupted", expected=400, world=brewer, confirm=True)
        self.command("discard-interrupted", world=brewer)
        self.assertEqual(self.lib.world_metric(brewer, 10), 1)
        self.lib.world_recovery(brewer, 7)
        preparation = self.request("/api/v1/status", world=brewer)["recovery"]
        self.assertTrue(preparation["available"])
        self.assertEqual(preparation["current_step"], -1)
        self.assertEqual(preparation["step_name"], "")
        for malformed in (4, 5, 6, 8):
            self.lib.world_recovery(brewer, malformed)
            self.request("/api/v1/status", expected=503, world=brewer)
        self.command("stop", world=brewer)

    def test_fragmented_real_http_transport_auth_and_alias_dispatch(self):
        key = "0123456789abcdef" * 4
        for provisioned, authorization, expected in ((False, "", 503), (True, "", 401), (True, key, 200)):
            request = "GET /api/v1/status HTTP/1.1\r\nHost: pico.local\r\n"
            if authorization: request += f"Authorization: Bearer {authorization}\r\n"
            response = self.lib.world_http(self.world, (request + "\r\n").encode(), provisioned).decode()
            headers, body = response.split("\r\n\r\n", 1)
            self.assertIn(f" {expected} ", headers.splitlines()[0])
            json.loads(body)
        body = '{"temperature_unit":"C"}'
        request = (f"PATCH /api/v1/settings HTTP/1.1\r\nHost: pico.local\r\nAuthorization: Bearer {key}\r\n"
                   f"Content-Type: application/json\r\nContent-Length: {len(body)}\r\n\r\n{body}")
        response = self.lib.world_http(self.world, request.encode(), True).decode()
        self.assertEqual(json.loads(response.split("\r\n\r\n", 1)[1])["temperature_unit"], "C")

    def test_physical_and_headless_pairing_require_the_same_verified_claim(self):
        key = "0123456789abcdef"*4
        nonce = "fda817b2"*8
        payload = json.dumps({"nonce": nonce}).encode()
        def call(world, path, auth=False):
            post = path.startswith("pairing/")
            headers = [f"{'POST' if post else 'GET'} /api/v1/{path} HTTP/1.1", "Host: pico.local"]
            if auth:
                headers.append("Authorization: Bearer "+key)
            if post:
                headers += ["Content-Type: application/json", f"Content-Length: {len(payload)}"]
            request = ("\r\n".join(headers)+"\r\n\r\n").encode() + (payload if post else b"")
            wire = self.lib.world_http(world, request, True).decode()
            headers, body = wire.split("\r\n\r\n",1)
            return int(headers.split()[1]), json.loads(body)

        for physical in (False, True):
            world = self.new(0)
            self.lib.world_pairing_mode(world,physical)
            self.assertEqual(bool(call(world,"overview")[1].get("pairing_v1")), physical)
            self.assertEqual(call(world,"pairing/grant")[0],401)
            self.assertEqual(call(world,"pairing/claim")[0],404)
            requested, data = call(world,"pairing/request")
            self.assertEqual(requested,202 if physical else 401)
            if physical:
                self.assertEqual(data,{"state":"pending","code":nonce[:8]})
                self.assertEqual(call(world,"pairing/claim"),(202,{"state":"pending"}))
            else:
                self.assertEqual(call(world,"pairing/request",True)[0],404)
            self.assertEqual(call(world,"pairing/grant",True),(202,{"state":"approved"}))
            self.assertEqual(call(world,"pairing/claim"),(200,{"api_key":key}))
            self.assertEqual(call(world,"pairing/claim")[0],404)

    def test_strict_command_parsing_and_argument_bounds(self):
        bad = ['{}', '{"command":"start","command":"stop"}', '{"command":"start","enabled":true}',
               '{"command":"start",}', '{"command":"start"}garbage', '{"command":"start","x":0}',
               '{"command":"set-sampling-interval","value":NaN}', '{"command":"set-sampling-interval","value":1e999}',
               '{"command":"set-sampling-interval","value":060}', '{"command":"start\\n"}']
        for body in bad:
            with self.subTest(body=body):
                self.request("/api/v1/commands", body, 1, expected=400)
        self.command("calibrate", expected=400)
        self.command("calibrate", confirm=False, expected=400)
        self.command("calibrate", confirm=True)
        for value in (59, 60.5, 86401): self.command("set-sampling-interval", value=value, expected=400)
        for value in (60, 300, 86400): self.command("set-sampling-interval", value=value)
        self.assertEqual(self.lib.world_metric(self.world, 0), 0)

    def test_alias_settings_native_preferences_and_rollback(self):
        self.request("/api/v1/settings", {"temperature_unit": "C"}, 3)
        self.request("/api/v1/settings", '{"temperature_unit":"F","temperature_unit":"C"}', 3, expected=400)
        self.request("/api/v1/settings", {"detailed_status": True}, 3, expected=400)
        self.fault(0, 1)
        self.request("/api/v1/settings", {"temperature_unit": "F"}, 3, expected=503)
        self.assertEqual(self.request("/api/v1/settings")["temperature_unit"], "C")
        self.assertEqual(self.lib.world_preferences(self.world, b"F"), 200)
        self.lib.world_reboot(self.world)
        self.assertEqual(self.lib.world_status(self.world), 200)
        self.assertEqual(self.request("/api/v1/settings")["temperature_unit"], "F")
        self.fault(2, 1)
        self.request("/api/v1/settings", {"temperature_unit": "C"}, 3, expected=503)
        self.assertEqual(self.request("/api/v1/settings")["temperature_unit"], "F")

    def test_recipe_transactions_full_catalogue_native_validation(self):
        brewer = self.new(0)
        self.request("/api/v1/recipes/mash", dict(self.recipe(), create_only=True), 2, expected=201, world=brewer)
        self.request("/api/v1/recipes/mash", dict(self.recipe("Duplicate"), create_only=True), 2, expected=409, world=brewer)
        self.assertEqual(self.request("/api/v1/recipes/mash", world=brewer)["name"], "Test mash")
        self.request("/api/v1/recipes/mash", dict(self.recipe(), create_only="true"), 2, expected=400, world=brewer)
        self.request("/api/v1/recipes/bad", self.recipe(location=6), 2, expected=400, world=brewer)
        self.fault(0, 2, brewer)
        self.request("/api/v1/recipes/mash", self.recipe("Replacement"), 2, expected=503, world=brewer)
        self.lib.world_reboot(brewer)
        self.assertEqual(self.lib.world_status(brewer), 200)
        self.assertEqual(self.request("/api/v1/recipes/mash", world=brewer)["name"], "Test mash")
        self.request("/api/v1/recipes/mash", self.recipe("Replacement"), 2, world=brewer)
        for n in range(15): self.request(f"/api/v1/recipes/r{n}", self.recipe(), 2, expected=201, world=brewer)
        self.request("/api/v1/recipes/full", self.recipe(), 2, expected=507, world=brewer)
        self.request("/api/v1/recipes/mash", method=4, world=brewer)
        self.request("/api/v1/recipes/new", self.recipe("New slot"), 2, expected=201, world=brewer)
        self.lib.world_reboot(brewer)
        self.assertEqual(self.lib.world_status(brewer), 200)
        self.assertEqual(len(self.request("/api/v1/recipes", world=brewer)["recipes"]), 16)
        self.assertEqual(self.request("/api/v1/recipes/new", world=brewer)["name"], "New slot")
        self.command("brew", recipe_id="new", world=brewer)
        self.request("/api/v1/recipes/new", self.recipe(), 2, expected=409, world=brewer)

    def test_conditional_recipe_write_and_delete(self):
        brewer = self.new(0)
        path = "/api/v1/recipes/pale"
        self.assertTrue(self.request("/api/v1", world=brewer)["recipe_conditional_write"])
        saved = self.request(path, self.recipe(), 2, expected=201, world=brewer)
        revision = saved["revision"]
        self.assertGreater(revision, 0)
        def write(body, expected):
            return self.request(path, body, 2, expected=expected, world=brewer)
        for invalid in (0, -1, 1.5, "1", 4294967296):
            write(dict(self.recipe(), if_revision=invalid), 400)
            self.request(path, {"if_revision": invalid}, 4, expected=400, world=brewer)
        write(dict(saved, if_revision=revision), 400)
        write(dict(saved, create_only=True), 400)
        updated = write(dict(saved, name="Edited"), 200)
        self.assertGreater(updated["revision"], revision)
        self.lib.world_reboot(brewer)
        self.assertEqual(write(saved, 409)["error"], "recipe_changed")
        self.request(path, {"if_revision": revision}, 4, expected=409, world=brewer)
        self.assertEqual(self.request(path, world=brewer), updated)
        self.request(path, {"if_revision": updated["revision"]}, 4, world=brewer)
        write(updated, 409)
        write(self.recipe(), 201)
        write(updated, 409)

    def test_partial_write_never_returns_success(self):
        self.fault(1, 1)
        self.request("/api/v1/settings", {"temperature_unit": "C"}, 3, expected=503)
        self.lib.world_reboot(self.world)
        self.assertEqual(self.lib.world_status(self.world), 200)
        self.assertEqual(self.request("/api/v1/settings")["temperature_unit"], "F")

    def test_counter_corruption_does_not_reset_and_stop_still_works(self):
        self.command("start"); self.command("stop")
        self.file("seq.b", 1)
        self.lib.world_reboot(self.world)
        self.assertEqual(self.lib.world_status(self.world), 503)
        self.command("start", expected=503)
        self.command("stop")
        self.assertFalse(self.request("/api/v1/health")["storage_ready"])
        self.assertEqual(self.lib.world_metric(self.world, 0), 1)

    def test_missing_counter_is_not_reconstructed_from_retained_history(self):
        self.command("start"); self.command("stop")
        self.file("seq.a", 0); self.file("seq.b", 0)
        self.lib.world_reboot(self.world)
        self.assertEqual(self.lib.world_status(self.world), 503)
        self.command("start", expected=503)

    def test_storage_io_is_not_treated_as_absence(self):
        self.file("cfg.a", 2)
        writes = self.lib.world_metric(self.world, 2)
        self.lib.world_reboot(self.world)
        self.assertEqual(self.lib.world_status(self.world), 503)
        self.assertEqual(self.lib.world_metric(self.world, 2), writes)

    def test_pre_start_reservation_and_failed_start_are_atomic(self):
        self.fault(0, 1)
        self.command("start", expected=503)
        self.assertEqual(self.lib.world_metric(self.world, 0), 0)
        fresh = self.new(1)
        self.assertEqual(self.lib.world_prepare(fresh, 17), 200)
        self.assertEqual(self.lib.world_prepare(fresh, 17), 200)
        self.tick(world=fresh)
        history = self.request("/api/v1/history", world=fresh)
        self.assertEqual(history["id"], 1)
        self.assertEqual(history["record_count"], 0)
        self.lib.world_cancel(fresh)
        self.assertEqual(self.request("/api/v1/history", world=fresh)["state"], "interrupted")
        self.command("start", world=fresh)
        self.assertEqual(self.request("/api/v1/history", world=fresh)["id"], 2)

    def test_history_named_readings_recovery_and_unique_ids(self):
        self.command("start")
        self.tick(times=3)
        history = self.request("/api/v1/history")
        self.assertEqual(history["id"], 1)
        self.assertEqual(history["record_count"], 4)
        page = self.request("/api/v1/history/samples/0?id=1")
        self.assertEqual(page["fields"][-3:], ["sensor_temperature", "sensor_pressure_native", "sensor_battery"])
        self.assertEqual(page["field_metadata"]["sensor_pressure_native"]["unit"], "raw")
        self.assertIsNone(page["samples"][0][page["fields"].index("pressure_kpa")])
        self.assertEqual(page["samples"][0][8], 255)
        self.assertEqual(len(page["samples"]), 4)
        self.assertIsNone(page["next_offset"])
        self.lib.world_reboot(self.world)
        self.assertEqual(self.lib.world_status(self.world), 200)
        history = self.request("/api/v1/history")
        self.assertEqual(history["state"], "interrupted")
        self.assertTrue(history["recovered_after_restart"])
        self.assertFalse(self.request("/api/v1/status")["busy"])
        self.command("start")
        self.assertEqual(self.request("/api/v1/history")["id"], 2)
        self.assertEqual(self.request("/api/v1/history/samples/0?id=1")["fields"], page["fields"])

    def test_history_bounds_state_samples_and_wrapping_clock(self):
        self.fault(5, 0xFFFFFF00)
        self.command("start")
        self.fault(7, 1); self.tick(1)
        self.tick(15000)
        history = self.request("/api/v1/history")
        self.assertEqual(history["record_count"], 3)
        page = self.request("/api/v1/history/samples/2?id=1")
        self.assertEqual(page["samples"][0][0], 15001)
        for path in ("00?id=1", "0?id=0", "0?id=1&x=1", "4294967296?id=1"):
            self.request("/api/v1/history/samples/" + path, expected=400)
        self.request("/api/v1/history/samples/100?id=1", expected=400)
        self.request("/api/v1/history/samples/0?id=99", expected=409)

    def test_skipped_sampling_intervals_are_explicit(self):
        self.command("start")
        self.tick(45000)
        history = self.request("/api/v1/history")
        self.assertEqual(history["gap_count"], 2)
        page = self.request("/api/v1/history/samples/0?id=1")
        self.assertEqual(page["samples"][-1][9] & 16, 16)

    def test_fermentation_cadence_is_captured_and_elapsed_never_silently_wraps(self):
        self.fault(11, 300000)
        self.assertEqual(self.request("/api/v1")["history_sample_interval_ms"], 300000)
        self.command("start")
        self.tick(15000, 19)
        self.assertEqual(self.request("/api/v1/history")["record_count"], 1)
        self.tick(15000)
        self.assertEqual(self.request("/api/v1/history")["record_count"], 2)
        self.fault(11, 60000)  # Device changes cannot relabel an existing session.
        self.tick(60000)
        self.assertEqual(self.request("/api/v1/history")["sample_interval_ms"], 300000)
        self.assertEqual(self.request("/api/v1/history")["record_count"], 2)
        self.command("stop")
        self.command("start")
        self.assertEqual(self.request("/api/v1/history")["sample_interval_ms"], 60000)
        self.lib.world_reboot(self.world)
        self.assertEqual(self.lib.world_status(self.world), 200)
        sessions = self.request("/api/v1/history")["sessions"]
        self.assertEqual([s["sample_interval_ms"] for s in sessions], [60000, 300000])
        fresh = self.new(1)
        self.command("start", world=fresh)
        self.fault(5, 0xFFFFFF00, fresh); self.tick(1, world=fresh)
        self.tick(15000, world=fresh)
        history = self.request("/api/v1/history", world=fresh)
        self.assertTrue(history["truncated"])
        self.assertEqual(history["duration_ms"], 0xFFFFFFFF)

    def test_eight_sessions_and_full_4224_record_capacity(self):
        self.command("start")
        self.tick(times=4224)
        history = self.request("/api/v1/history")
        self.assertEqual(history["record_count"], 4224)
        self.assertTrue(history["truncated"])
        last = self.request("/api/v1/history/samples/4222?id=1")
        self.assertEqual(last["samples"][-1][0], 4223 * 15000)
        self.assertIsNone(last["next_offset"])
        self.assertLess(self.lib.world_metric(self.world, 4), 650000)
        self.fault(5, 0xFFFFFF00); self.tick(1)
        self.tick(15000)
        self.assertEqual(self.request("/api/v1/history")["duration_ms"], 0xFFFFFFFF)
        self.tick(100000000)
        self.assertEqual(self.request("/api/v1/history")["duration_ms"], 0xFFFFFFFF)
        self.command("stop")
        for _ in range(9):
            self.command("start"); self.tick(); self.command("stop")
        history = self.request("/api/v1/history")
        self.assertEqual(len(history["sessions"]), 8)
        self.assertEqual(history["id"], 10)
        self.request("/api/v1/history/samples/0?id=1", expected=409)
        self.lib.world_reboot(self.world)
        self.assertEqual(self.lib.world_status(self.world), 200)

    def test_still_largest_real_layout_and_catalogue_fit_arena(self):
        still = self.new(2)
        for _ in range(8):
            self.command("essential-oils", world=still); self.tick(world=still); self.command("stop", world=still)
        history = self.request("/api/v1/history", world=still)
        page = self.request("/api/v1/history/samples/0?id=8", world=still)
        self.assertEqual(len(history["sessions"]), 8)
        self.assertEqual(len(page["fields"]), 26)
        self.assertNotIn("actuator_pump_level", page["fields"])
        self.assertNotIn("actuator_pump_rpm", page["fields"])
        self.assertEqual(page["field_metadata"]["actuator_fan2_rpm"]["unit"], "rpm")

    def test_display_pages_revision_and_no_nonfinite_json(self):
        brewer = self.new(0)
        display = self.request("/api/v1/display", world=brewer)
        self.assertEqual(display["format"], "row-gray4-msb-hex")
        result = bytes.fromhex(display["data"])
        for offset in (1024, 2048, 3072):
            result += bytes.fromhex(self.request(f"/api/v1/display/{offset}?revision=42", world=brewer)["data"])
        self.assertEqual(result, bytes(range(256)) * 16)
        self.request("/api/v1/display/1?revision=42", expected=400, world=brewer)
        self.request("/api/v1/display/1024?revision=41", expected=409, world=brewer)
        self.fault(4, 1, brewer); self.request("/api/v1/display", expected=409, world=brewer)
        self.fault(6, 1)
        self.assertIsNone(self.request("/api/v1/status")["temperature_f"])

    def test_optional_telemetry_async_budget_envelope_and_persistence(self):
        config = {"enabled": True, "host": "hub.local", "port": 8080}
        self.request("/api/v1/telemetry", config, 2)
        self.tick(1, 40)
        telemetry = self.request("/api/v1/telemetry")
        self.assertEqual(telemetry["status"]["sent"], 1)
        request = self.lib.world_sent(self.world).decode()
        headers, payload = request.split("\r\n\r\n", 1)
        self.assertIn(f"Content-Length: {len(payload)}", headers)
        envelope = json.loads(payload)
        self.assertEqual(envelope["schema_version"], 1)
        self.assertEqual(envelope["status"]["model"], "picoferm")
        self.assertNotIn("Authorization", request)
        self.assertLessEqual(self.lib.world_metric(self.world, 7), 512)
        self.assertLessEqual(self.lib.world_metric(self.world, 8), 512)
        self.lib.world_reboot(self.world)
        self.assertTrue(self.request("/api/v1/telemetry")["enabled"])

    def test_telemetry_timeout_failures_and_invalid_hosts(self):
        for host in ("http://hub/", "bad\r\nhost", "-bad.local", "1.2.3.999", "0.0.0.0", "hub..local"):
            self.request("/api/v1/telemetry", {"enabled": True, "host": host, "port": 8080}, 2, expected=400)
        self.request("/api/v1/telemetry", {"enabled": True, "host": "hub", "port": 8080}, 2)
        self.fault(8, 0); self.tick(1); self.tick(5000)
        status = self.request("/api/v1/telemetry")["status"]
        self.assertEqual(status["last_error"], "timeout")
        self.assertEqual(status["failed"], 1)
        self.assertEqual(self.lib.world_metric(self.world, 6), 1)
        self.fault(8, 1); self.lib.world_response(self.world, b"HTTP/1.1 503 Unavailable\r\n")
        self.tick(30000); self.tick(1, 40)
        status = self.request("/api/v1/telemetry")["status"]
        self.assertEqual(status["last_http_status"], 503)
        self.assertEqual(status["last_error"], "http")


if __name__ == "__main__":
    unittest.main(verbosity=2)
