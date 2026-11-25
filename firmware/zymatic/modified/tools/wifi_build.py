"""Compose released Wi-Fi layouts from a verified OEM image and zero-identity template delta."""

import json
import struct
import zlib
from functools import lru_cache

import zymatic_build as build
import usb_stock as stock


def xor_bytes(left, right):
    return bytes(a ^ b for a, b in zip(left, right, strict=True))


def port_instructions(image, old, new):
    for _, address, before, after in build.hub_port_patches(new, previous=old):
        if not image.startswith(before, address):
            raise ValueError("Incorrect Hub port instruction in Zymatic Wi-Fi application")
        image[address:address+len(after)] = after


class WifiApplication:
    VERSION = build.VERSION

    def __init__(self, profile, limit):
        self.PROFILE = profile
        self.limit = limit

    @lru_cache(maxsize=2)
    def template(self):
        root = build.TEMPLATE / "wifi" / self.PROFILE
        raw = (root / "layout.json").read_bytes()
        if build.sha(raw) != MANIFEST_HASHES[self.PROFILE]:
            raise ValueError("Unrecognized Zymatic Wi-Fi firmware manifest")
        layout = json.loads(raw)
        delta = (root / "application.delta").read_bytes()
        if (len(delta) != layout["application_bytes"] or not stock.APP_SIZE < len(delta) <= self.limit
                or build.sha(delta) != layout["extension_sha256"]):
            raise ValueError("Invalid Zymatic Wi-Fi firmware delta")
        return delta, layout

    def provision(self, image, key, uid, host, program):
        _, layout = self.template()
        _, port = build.hub_origin(host)
        config = build.provisioning(key, uid, host)
        config["provision_recipe"] = build.recipes.pack_program(program)
        for name, value in config.items():
            at = layout["symbols"][name]
            image[at:at+len(value)] = value
        port_instructions(image, 80, port)
        struct.pack_into("<H", image, layout["symbols"]["provision_legacy_sum"], build.legacy_sum(image))
        struct.pack_into("<I", image, len(image)-4, zlib.crc32(image[:-4]))
        return {
            "schema_version": 1, "model": "zymatic", "firmware_version": self.VERSION,
            "build_profile": self.PROFILE, "oem_application_sha256": stock.APP_SHA256,
            "binary_sha256": build.sha(image), "application_bytes": len(image), "uid": uid,
            "hub_host": host, "hub_port": port, "offline_program": build.recipes.unpack_program(config["provision_recipe"]),
            "extension_sha256": layout["extension_sha256"], "compiler": layout["compiler"],
            "first_possible_bootloader": 0x1e000, "minimum_boot_start": self.limit,
            "sram_reserved_bytes": layout["sram_reserved_bytes"],
            "config": {name: {"address": layout["symbols"][name], "bytes": size}
                       for name, size in build.FIELDS.items()},
        }

    def build_application(self, original, key, uid, hub_host="picobrew.com", offline_program=None, *, legacy_menu=False):
        if legacy_menu or not stock.is_original_app(original):
            raise ValueError("Zymatic Wi-Fi layout requires the verified 1.1.14 original")
        delta, layout = self.template()
        image = bytearray(xor_bytes(original.ljust(len(delta), b"\xff"), delta))
        if build.sha(image) != layout["template_sha256"]:
            raise ValueError("Zymatic unprovisioned application hash differs")
        manifest = self.provision(image, key, uid, hub_host, offline_program)
        return bytes(image), manifest

    def recognize_private_application(self, flash):
        delta, layout = self.template()
        if len(flash) < len(delta):
            return None
        image = bytearray(flash[:len(delta)])
        if zlib.crc32(image[:-4]) != int.from_bytes(image[-4:], "little"):
            return None
        symbols = layout["symbols"]
        fields = {}
        for name, size in (build.FIELDS | build.DERIVED).items():
            address = symbols[name]
            fields[name] = bytes(image[address:address+size])
            image[address:address+size] = bytes(size)
        try:
            uid = fields["provision_uid"][:-1].decode("ascii")
            host = fields["provision_host"].split(b"\0")[0].decode("ascii")
            recipe = build.recipes.unpack_program(fields["provision_recipe"])
            _, port = build.hub_origin(host)
            port_instructions(image, port, 80)
            if build.sha(image) != layout["template_sha256"]:
                return None
            manifest = self.provision(image, fields["provision_key"], uid, host, recipe)
        except ValueError:
            return None
        return manifest if image == flash[:len(image)] else None


COMPACT = WifiApplication("compact", 0x1e000)
EXTENDED = WifiApplication("extended", 0x1fc00)
MANIFEST_HASHES = {
    "compact": "6f4769ec07730f3d58e5ef982a173b392588ec24dab1e0ae6b2d9fe1587a4170",
    "extended": "8a7ed2135f97f439cfababa280d379dda83e6efd599fc8dd30806ed40f9c259a",
}
