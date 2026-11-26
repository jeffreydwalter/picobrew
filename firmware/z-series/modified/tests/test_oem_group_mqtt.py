"""Run the Z119 MQTT client against a TLS wire peer bound only to IPv4 loopback."""

import base64
import hashlib
import hmac
import json
import os
from pathlib import Path
import shlex
import shutil
import socket
import ssl
import subprocess
import tempfile
import unittest
from urllib.parse import quote

from test_oem_group_dial import build_group_dial

HOST = "broker.test"
RECEIVER = "z119-test-receiver"
DEVICEBOUND = f"devices/{RECEIVER}/messages/devicebound/"


def mqtt_string(text):
    data = text.encode("ascii")
    return len(data).to_bytes(2, "big") + data


def packet_header(header, remaining):
    prefix = bytearray([header])
    while remaining >= 128:
        prefix.append((remaining % 128) | 128)
        remaining //= 128
    prefix.append(remaining)
    return bytes(prefix)


def packet(header, body):
    return packet_header(header, len(body)) + body


def expect(stream, expected, label):
    # A failing assertion must not dump the contents of a CONNECT packet.
    if not hmac.compare_digest(stream.read(len(expected)), expected):
        raise AssertionError(f"Unexpected MQTT {label}")


def exchange(tls):
    resource = HOST + "%2Fdevices%2F" + RECEIVER
    expiry = "2524636800"
    signed = hmac.digest(b"z119-loopback-fixture-only", (resource + "\n" + expiry).encode(), hashlib.sha256)
    signature = quote(base64.b64encode(signed), safe="")
    password = f"SharedAccessSignature sr={resource}&sig={signature}&se={expiry}"
    username = f"{HOST}/{RECEIVER}/api-version=2016-11-14"

    with tls.makefile("rb") as stream:
        # MQTT 3.1.1, clean session, password + username, 15-second keepalive.
        connect = mqtt_string("MQTT") + b"\x04\xc2\x00\x0f"
        connect += mqtt_string(RECEIVER) + mqtt_string(username) + mqtt_string(password)
        expect(stream, packet(0x10, connect), "CONNECT")
        tls.sendall(packet(0x20, b"\x00\x00"))

        # Echo the nonzero packet identifier in SUBACK; grant the requested QoS 0.
        subscribe = mqtt_string(DEVICEBOUND + "#") + b"\x00"
        expect(stream, packet_header(0x82, len(subscribe) + 2), "SUBSCRIBE header")
        identifier = stream.read(2)
        if len(identifier) != 2 or identifier == bytes(2):
            raise AssertionError("Missing MQTT subscription identifier")
        expect(stream, subscribe, "subscription topic or QoS")
        tls.sendall(packet(0x90, identifier + b"\x00"))

        invitation = {"FirmwareVersion": "0.0.119", "SessionType": 6, "ZProgramId": 24,
                      "Name": "Loopback offer", "RecipeID": 420}
        payload = json.dumps(invitation, separators=(",", ":")).encode()
        tls.sendall(packet(0x30, mqtt_string(DEVICEBOUND + "invitation") + payload))
        expect(stream, packet(0xE0, b""), "DISCONNECT")


class OEMGroupMqtt(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if os.name != "posix" or not shutil.which("openssl") or not shutil.which("pkg-config"):
            raise unittest.SkipTest("The loopback TLS fixture requires POSIX, OpenSSL and pkg-config")
        if subprocess.run(["pkg-config", "--atleast-version=1.1.1", "openssl"]).returncode:
            raise unittest.SkipTest("The loopback TLS fixture requires OpenSSL development files >= 1.1.1")
        temp = tempfile.TemporaryDirectory(prefix="z119-group-mqtt-")
        cls.addClassCleanup(temp.cleanup)
        cls.root = Path(temp.name)
        cls.binary = cls.root / "group-dial"
        cflags = shlex.split(subprocess.check_output(["pkg-config", "--cflags", "openssl"], text=True))
        libs = shlex.split(subprocess.check_output(["pkg-config", "--libs", "openssl"], text=True))
        build_group_dial(cls.binary, ["-DZ119_GROUP_LOOPBACK_TLS", *cflags], libs)

        def openssl(*args):
            subprocess.run(["openssl", *args], cwd=cls.root, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True)

        create_key = ["-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256", "-nodes"]
        for ca in ["ca", "other-ca"]:
            openssl("req", "-x509", *create_key, "-subj", f"/CN=Z119 fixture {ca}", "-days", "1",
                    "-addext", "basicConstraints=critical,CA:TRUE", "-keyout", f"{ca}.key", "-out", f"{ca}.pem")
        for serial, host in enumerate([HOST, "other.test"], start=1):
            extensions = cls.root / "extensions"
            extensions.write_text(f"subjectAltName=DNS:{host}\nextendedKeyUsage=serverAuth\n")
            openssl("req", "-new", *create_key, "-subj", f"/CN={host}", "-keyout", f"{host}.key",
                    "-out", "leaf.csr")
            openssl("x509", "-req", "-in", "leaf.csr", "-CA", "ca.pem", "-CAkey", "ca.key",
                    "-set_serial", str(serial), "-days", "1", "-extfile", "extensions", "-out", f"{host}.pem")

    def exercise(self, scenario, certificate=HOST, trust="ca"):
        server = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        server.load_cert_chain(self.root / f"{certificate}.pem", self.root / f"{certificate}.key")
        # Only MQTT application records are needed after the handshake.
        server.num_tickets = 0
        names = []
        server.set_servername_callback(lambda _tls, name, _context: names.append(name))
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as listener:
            listener.bind(("127.0.0.1", 0))
            listener.listen(1)
            listener.settimeout(6)
            args = [str(self.binary), scenario, str(listener.getsockname()[1]), str(self.root / f"{trust}.pem")]
            client = subprocess.Popen(args)
            try:
                raw, addr = listener.accept()
                with raw:
                    raw.settimeout(6)
                    self.assertEqual(addr[0], "127.0.0.1")
                    if certificate != HOST or trust != "ca":
                        with self.assertRaises(ssl.SSLError):
                            server.wrap_socket(raw, server_side=True)
                    else:
                        with server.wrap_socket(raw, server_side=True) as tls:
                            exchange(tls)
                self.assertEqual(client.wait(timeout=6), 0)
                self.assertEqual(names, [HOST])
            finally:
                if client.poll() is None:
                    client.kill()
                client.wait()

    def test_subscribed_offer_starts_only_after_dial(self):
        self.exercise("mqtt-start")

    def test_subscribed_offer_exits_without_starting(self):
        self.exercise("mqtt-exit")

    def test_wrong_hostname_rejected_before_mqtt(self):
        self.exercise("mqtt-wrong-name", certificate="other.test")

    def test_untrusted_ca_rejected_before_mqtt(self):
        self.exercise("mqtt-untrusted-ca", trust="other-ca")


if __name__ == "__main__":
    unittest.main()
