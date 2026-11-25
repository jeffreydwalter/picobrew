"""Hub USB worker. Only newline-delimited protocol events leave stdout."""

import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import sys


def source_root():
    if getattr(sys, "frozen", False):
        return Path(sys._MEIPASS)
    return Path(__file__).resolve().parents[3]


ROOT = source_root()
ZYMATIC = ROOT / "firmware/zymatic/modified/tools"
ESP = ROOT / "firmware/common/tools"
CC = ROOT / "firmware/pico-s-pro/modified/tools"
for path in (ROOT / "tools", ESP, ZYMATIC, CC, ROOT / "server/tools/usb"):
    sys.path.insert(0, str(path))
import private_files

ESP_MODELS = ["z-series", "z1", "z2", "z3", "z4", "picostill", "picoferm"]
# USB-to-UART bridges and native USB used for serial service. The connected
# controller is independently identified by the existing ROM flash procedures.
ESP_ADAPTERS = {
    (0x10C4, 0xEA60),
    (0x10C4, 0xEA70),
    (0x1A86, 0x7523),
    (0x1A86, 0x55D4),
    (0x0403, 0x6001),
    (0x0403, 0x6010),
    (0x0403, 0x6011),
    (0x0403, 0x6014),
    (0x303A, 0x1001),
}


class UserError(Exception):
    """Error message deliberately suitable for display outside the USB worker."""


def read_message(stream):
    raw = stream.readline(8193)
    if not raw or len(raw) > 8192 or not raw.endswith(b"\n"):
        raise UserError("The USB request was interrupted or exceeded the size limit.")
    try:
        value = json.loads(raw)
    except (ValueError, UnicodeError):
        raise UserError("The Hub sent an invalid USB request.") from None
    if not isinstance(value, dict):
        raise UserError("The Hub sent an invalid USB request.")
    return value


def serial_devices(ports):
    devices = {}
    for p in ports:
        if (p.vid, p.pid) == (0x20A0, 0x421E):
            models, label = ["zymatic"], "Zymatic USB"
        elif (p.vid, p.pid) in ESP_ADAPTERS:
            models, label = ESP_MODELS, "ESP serial adapter"
        else:
            continue
        serial = json.dumps(
            [p.vid, p.pid, p.serial_number, p.location, p.device], separators=(",", ":")
        ).encode()
        token = "serial-" + hashlib.sha256(serial).hexdigest()[:32]
        # Display the OS port only, never a hardware-supplied description.
        name = Path(p.device).name
        if re.fullmatch(r"[A-Za-z0-9._-]{1,80}", name):
            label += " · " + name
        devices[token] = {"port": token, "label": label, "models": models, "path": p.device}
    return devices


def devices():
    from serial.tools import list_ports
    import cc_service

    result = serial_devices(list_ports.comports())
    for device in cc_service.devices():
        result[device["port"]] = device
    return result


def run(request, stream, emit, discover=devices):
    if set(request) - {
        "action",
        "model",
        "port",
        "display",
        "data_dir",
        "job_id",
        "hub_host",
        "expected_uid",
        "offline_program",
    }:
        raise UserError("The Hub sent an unsupported USB option.")
    action = request.get("action")
    if action not in ("list", "backup", "install", "restore"):
        raise UserError("Choose a supported USB operation.")
    program = request.get("offline_program")
    if "offline_program" in request:
        if (action != "install" or request.get("model") != "zymatic" or not isinstance(program, dict)
                or program.get("model") != "zymatic"):
            raise UserError("An offline program requires a Zymatic installation.")
        import offline_program

        try:
            offline_program.pack_program(program)
        except ValueError as exc:
            raise UserError(str(exc)) from None
    entries = discover()
    if action == "list":
        emit(
            {
                "type": "devices",
                "devices": [
                    {key: item[key] for key in ("port", "label", "models")} for item in entries.values()
                ],
            }
        )
        return
    port, model = request.get("port"), request.get("model")
    if not isinstance(port, str) or port not in entries or model not in entries[port]["models"]:
        raise UserError("The selected USB adapter is unavailable. Refresh the connected devices.")
    if model in ("z1", "z2", "z3", "z4") and action == "install":
        raise UserError(
            "Choose a single Z-series module for conversion so its flash layout can be verified."
        )
    display = request.get("display", "")
    if (model == "pico-c" and display not in ("ssd1309", "ssd1325")) or (model != "pico-c" and display):
        raise UserError("Select the matching Pico C display controller.")
    expected = request.get("expected_uid") or None
    if expected and (
        not isinstance(expected, str) or not re.fullmatch(r"[0-9a-f]{12}|[0-9a-f]{32}", expected)
    ):
        raise UserError("The registered machine identity is invalid.")
    if not isinstance(request.get("data_dir"), str):
        raise UserError("The Hub data directory is unavailable.")
    data = Path(request["data_dir"])
    if not data.is_absolute():
        raise UserError("The Hub data directory is unavailable.")
    job = request.get("job_id")
    if not isinstance(job, str) or not re.fullmatch(r"[a-f0-9]{32}", job):
        raise UserError("The Hub supplied an invalid USB job identifier.")
    import usb_avr

    directory = data / "usb-jobs"
    private_files.ensure_private_directory(directory, parents=True, exist_ok=True)
    private_files.assert_private(directory, directory=True)
    # The worker owns this OS lock across reads, physical confirmations, writes
    # and readback. A restarting Hub cannot overlap a surviving worker.
    try:
        with usb_avr.service_lock(directory):
            return _service(
                request, stream, emit, entries, action, model, port, display, data, job, expected
            )
    except usb_avr.ServiceBusy:
        raise UserError(
            "Another USB operation is still using this Hub. Wait for it to finish."
        ) from None


def _service(request, stream, emit, entries, action, model, port, display, data, job, expected):
    job_dir = data / "usb-jobs" / job
    private_files.ensure_private_directory(job_dir, parents=True, exist_ok=True)
    private_files.assert_private(job_dir, directory=True)
    job_key = job_dir / "device-key" if action == "install" else None

    def progress(message):
        emit({"type": "progress", "message": message})

    def confirm(message):
        token = secrets.token_hex(12)
        emit({"type": "confirm", "id": token, "message": message})
        if read_message(stream) != {"confirm": token}:
            raise UserError("The USB operation was canceled.")

    if model in ("pico-c", "pico-s", "pico-pro"):
        import cc_service

        try:
            cc_service.run(
                action,
                model,
                port,
                display,
                data,
                confirm,
                progress,
                job_key_path=job_key,
                expected_uid=expected,
            )
        except cc_service.CCError as exc:
            raise UserError(str(exc)) from None
    elif model == "zymatic":
        import usb_avr

        key_store = data / "usb-stock" / "zymatic" / "keys"
        private_files.ensure_private_directory(key_store, parents=True, exist_ok=True)
        # A port token includes its path. The Zymatic programmer independently
        # rechecks its hardware identity before each AVRDUDE read and write.
        identity = usb_avr.usb_identity(entries[port]["path"])
        stable = identity[1] or identity[2]
        if action == "install" and not stable:
            raise UserError("The Zymatic USB port needs a stable hardware identity for installation.")

        def key_path(uid):
            if not isinstance(uid, str) or not re.fullmatch(r"[0-9a-f]{32}", uid):
                raise UserError("The Zymatic image has an invalid pairing identity.")
            return key_store / (uid + ".key")

        confirm("Keep the idle Zymatic powered and connected while its original flash is read twice.")
        progress("Reading the Zymatic and comparing both flash captures.")

        def avr_confirm(prompt):
            match = re.fullmatch(
                r"Type (INSTALL ZYMATIC|RESTORE ZYMATIC|WRITE [0-9]+\.[0-9]+\.[0-9]+): ", prompt
            )
            if not match:
                raise UserError("The Zymatic requested an unrecognized confirmation.")
            phase = match[1]
            if phase == "INSTALL ZYMATIC":
                confirm("Install standalone firmware on this Zymatic?")
            elif phase == "RESTORE ZYMATIC":
                confirm(
                    "Return this Zymatic to stock? Its standalone website and Hub controls will be removed."
                )
            else:
                confirm(
                    "Write Zymatic firmware "
                    + phase[6:]
                    + " now? Keep power connected through verification."
                )
                progress("Writing application pages and verifying the complete flash.")
            return phase

        uid = usb_avr.service(
            data,
            entries[port]["path"],
            action,
            confirm=avr_confirm,
            key_file=key_path if action == "install" else None,
            hub_host=request.get("hub_host") or None,
            expected_uid=expected,
            offline_program=request.get("offline_program"),
        )
        if job_key:
            import usb_stock

            if not isinstance(uid, str) or not re.fullmatch(r"[0-9a-f]{32}", uid):
                raise UserError(
                    "The Zymatic image was verified but its pairing identity was unavailable."
                )
            usb_stock.save_private(job_dir / "device-uid", uid.encode("ascii"))
            usb_stock.save_private(job_key, private_files.read_regular(key_path(uid), 128, private=True))
    else:
        import esp_usb

        confirm("Connect only the selected idle module in ROM download mode. Begin reading its flash?")
        progress("Identifying the controller and reading its flash.")
        with esp_usb.open_device(entries[port]["path"], model) as device:
            # PicoStill uses esp_efuse_mac_get_default, Z encodes getEfuseMac
            # least-significant byte first, and PicoFerm reports the station MAC.
            # An alternate station MAC on PicoFerm cannot match this ROM check.
            if expected is not None and expected != device.identity.mac:
                raise UserError(
                    "The connected module differs from the registered machine. No flash was written."
                )

            def esp_confirm(prompt):
                if prompt.startswith("Restore all original slots"):
                    confirm(
                        "Restore all original slots, settings and calibration? Standalone Hub controls will be removed."
                    )
                    return "RESTORE"
                if prompt == f"Type the device MAC {device.identity.mac} to confirm {action}: ":
                    confirm(
                        "Write the verified image to this " + model + " and verify the flash readback?"
                    )
                    progress("Writing changed sectors and comparing the complete flash.")
                    return device.identity.mac
                raise UserError("The module requested an unrecognized confirmation.")

            if action != "install":
                esp_usb.operation(action, model, device, data, ask=esp_confirm)
                return
            import usb_application
            import usb_stock

            # The device-specific private key predates the first write and
            # survives retries. It becomes a pairing result only after readback.
            key_path = esp_usb.backup_path(data, device.identity).parent / "standalone-api.key"
            key = usb_application.read_key(key_path, create=True).hex()
            esp_usb.operation(action, model, device, data, ask=esp_confirm, api_key=key)
            usb_stock.save_private(job_dir / "device-uid", device.identity.mac.encode("ascii"))
            usb_stock.save_private(job_key, key.encode("ascii"))


def self_test():
    import cc_service
    import esp_usb
    import esp_provisioning
    import esptool
    import libusb_package
    import pico_flash_backup_v2
    import pico_flash_install as cc
    import serial
    import usb_avr
    import usb_layout
    import usb_stock

    if (
        serial.VERSION != "3.5"
        or esptool.__version__ != "4.11.0"
        or not libusb_package.get_library_path()
    ):
        raise ValueError("USB dependency version or native library is missing")
    pico_flash_backup_v2.load_dependencies()
    cc._load_hub_recovery()
    for model in ("pico-s", "pico-pro"):
        cc.load_candidate(
            CC.parent / "firmware" / "standalone" / model / (model + "-standalone.bin"), model
        )
        cc.load_web_bundle(cc.WEB_DIRECTORY, model)
    for variant in ("ssd1309", "ssd1325"):
        model = "pico-c-" + variant
        cc.load_candidate(
            cc.C_FIRMWARE / "standalone" / "pico-c" / variant / "pico-c-standalone.bin", model
        )
        cc.load_web_bundle(cc.C_FIRMWARE / "web", model)
        cc._load_hub_recovery(model)
    usb_avr.programmer()
    for release in usb_layout.known_releases():
        release.template()
    for model, profile in (("z-series", "esp32-z-recovery"), ("picostill", "esp32-4m"), ("picoferm", "esp8266-2m")):
        _, parts = esp_usb.picoferm_ab_reference() if model == "picoferm" else esp_usb.reference(model)
        esp_provisioning.stage(parts["spiffs.bin"], profile, secrets.token_hex(32))
        if not esp_usb.legacy_applications(model):
            raise ValueError("Missing ESP USB migration identities")
    if not callable(cc_service.run) or not callable(usb_stock.prepare):
        raise ValueError("Missing USB service")


def main():
    if sys.argv[1:] == ["--version"]:
        print("1.0.0")
        return 0
    if getattr(sys, "frozen", False):
        os.environ["PATH"] = str(ROOT / "native" / "bin")
    if sys.argv[1:] == ["--self-test"]:
        # Release engineers need dependency diagnostics; this never opens USB or Hub data.
        self_test()
        print(json.dumps({"type": "complete", "message": "Offline self-test passed."}))
        return 0
    # The existing programmer libraries and AVRDUDE write human diagnostics.
    # Reserve a separate descriptor for protocol output. Hardware data, keys,
    # local paths and arbitrary exception strings must never enter browser jobs.
    output = os.fdopen(os.dup(sys.stdout.fileno()), "w", encoding="utf8", buffering=1)
    with open(os.devnull, "wb") as sink:
        os.dup2(sink.fileno(), 1)
        os.dup2(sink.fileno(), 2)

    def emit(event):
        output.write(json.dumps(event, separators=(",", ":")) + "\n")

    try:
        if len(sys.argv) == 1:
            run(read_message(sys.stdin.buffer), sys.stdin.buffer, emit)
        else:
            raise UserError("Unknown USB service option.")
        emit({"type": "complete", "message": "USB operation completed."})
    except UserError as error:
        emit({"type": "error", "message": str(error)})
        return 1
    except (OSError, ValueError, RuntimeError, ImportError) as error:
        if isinstance(error, PermissionError):
            message = "USB access was denied. Check the Hub account’s device permissions."
        else:
            message = "USB verification failed. Check the selected model, USB connection and original backup before retrying."
        emit({"type": "error", "message": message})
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
