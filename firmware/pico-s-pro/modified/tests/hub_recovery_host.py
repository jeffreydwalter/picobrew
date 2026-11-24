"""Expose the native recovery protocol on loopback for the Hub integration test."""
import ctypes
import subprocess
import sys
import tempfile
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "tools"))
from native_host import compiler, native_library, pic_flags, shared_flags, library_name

RECOVERY = ROOT / "recovery"
UID = "0123456789abcdef" * 2


def serve(directory, display):
    identity = directory / "identity.c"
    identity.write_text('#include <stdbool.h>\n#include <string.h>\n'
                        f'bool pico_recovery_read_uid(char uid[33]) {{ memcpy(uid, "{UID}", 33); return true; }}\n')
    library = directory / library_name("hub-recovery")
    http = ROOT / "platform/hub_recovery/http.c"
    flags = []
    if display:
        if display not in ("ssd1309", "ssd1325"):
            raise ValueError("Unknown Pico C display")
        http = ROOT.parents[1] / "pico-c/modified/platform/hub_recovery/variant_recovery.c"
        flags = [f'-DPICO_DISPLAY_VARIANT="{display}"']
    # The archived HTTP harness uses 8 KiB per file for its small request fixtures.
    # Give the loopback Hub a full application slot so it can transfer a real package.
    host = directory / "hub_recovery_http_host.c"
    fixture = (RECOVERY / "tests/recovery_http_host.c").read_text()
    declaration = "files[PICO_RECOVERY_FILE_COUNT][8192]"
    if fixture.count(declaration) != 1:
        raise RuntimeError("Could not locate the pinned recovery harness file buffer")
    host.write_text(fixture.replace(declaration, "files[PICO_RECOVERY_FILE_COUNT][PICO_RECOVERY_IMAGE_MAX]"))
    sources = [http, RECOVERY / "recovery/recovery.c",
               RECOVERY / "recovery/sha256.c", host, identity]
    subprocess.run([compiler(), "-std=c11", *pic_flags(), *shared_flags(), *flags,
                    "-I" + str(RECOVERY / "include"), "-I" + str(RECOVERY / "recovery"),
                    *sources, "-o", library], check=True)
    with native_library(library) as core:
        core.recovery_http_reset()
        core.recovery_http_call.argtypes = [ctypes.c_int, ctypes.c_char_p, ctypes.c_char_p,
                                            ctypes.c_size_t, ctypes.c_int, ctypes.c_int]
        core.recovery_http_response.restype = ctypes.c_char_p

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                data = self.rfile.read(int(self.headers.get("Content-Length", 0)))
                authenticated = self.headers.get("Authorization") == "Bearer " + "a" * 64
                status = core.recovery_http_call(int(self.command == "POST"), self.path.encode(),
                                                 data, len(data), authenticated, True)
                response = core.recovery_http_response()
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(response)

            do_POST = do_GET

            def log_message(self, *args):
                pass

        with HTTPServer(("127.0.0.1", 0), Handler) as server:
            print(server.server_port, flush=True)
            server.serve_forever()


if __name__ == "__main__":
    with tempfile.TemporaryDirectory() as directory:
        serve(Path(directory), sys.argv[1] if len(sys.argv) > 1 else "")
