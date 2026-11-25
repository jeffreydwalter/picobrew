"""Write or verify the installer's credential-free delta against the actual simulated application."""
import json
import zlib

import wifi_build
import zymatic_build as build
import usb_stock as stock


def export(original, image, symbols, profile, destination=None):
    if not stock.is_original_app(original):
        raise ValueError("Wi-Fi layout requires the verified OEM application")
    template = bytearray(image)
    fields = build.provisioning(bytes(range(32)), "0a"*16, "brewery.lan:8080")
    fields["provision_recipe"] = build.recipes.pack_program(None)
    fields["provision_crc"] = zlib.crc32(image[:-4]).to_bytes(4, "little")
    fields["provision_legacy_sum"] = build.legacy_sum(image).to_bytes(2, "little")
    for name, expected in fields.items():
        address = symbols[name]
        if not stock.APP_SIZE <= address <= len(image)-len(expected) or not image.startswith(expected, address):
            raise ValueError("Unexpected synthetic provisioning field " + name)
        template[address:address+len(expected)] = bytes(len(expected))
    wifi_build.port_instructions(template, 8080, 80)
    if symbols["provision_crc"] != len(template) - 4:
        raise ValueError("Wi-Fi checksum must terminate the application")
    delta = wifi_build.xor_bytes(original.ljust(len(image), b"\xff"), template)
    manifest = {
        "compiler": "Arduino AVR GCC 7.3.0-atmel3.6.1-arduino7",
        "application_bytes": len(image), "extension_sha256": build.sha(delta), "template_sha256": build.sha(template),
        "sram_reserved_bytes": symbols["__extension_bss_end"] - 0x800000 - build.BSS,
        "symbols": {name: symbols[name] for name in fields},
    }
    files = {"application.delta": delta, "layout.json": (json.dumps(manifest, sort_keys=True, indent=2)+"\n").encode()}
    if destination:
        destination.mkdir(parents=True, exist_ok=True)
        for name, data in files.items():
            (destination/name).write_bytes(data)
    else:
        shipped = build.TEMPLATE / "wifi" / profile
        for name, data in files.items():
            if (shipped/name).read_bytes() != data:
                raise ValueError("Shipped Zymatic Wi-Fi layout differs from linked executable: " + name)

        # Exercise the USB worker with the precise private image used by the AVR tests.
        builder = wifi_build.COMPACT if profile == "compact" else wifi_build.EXTENDED
        composed, record = builder.build_application(original, bytes(range(32)), "0a"*16, "brewery.lan:8080")
        if composed != image or builder.recognize_private_application(image) != record:
            raise ValueError("Installer did not reproduce the simulated Wi-Fi application")
