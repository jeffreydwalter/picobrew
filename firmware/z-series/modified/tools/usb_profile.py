"""Z-series USB qualification against preserved stock ESP32 application images."""

import hashlib
import json
from pathlib import Path
import sys

FIRMWARE = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(FIRMWARE / "common" / "tools"))
import esp_stock_partitions

VERSIONS = {
    "app/firmware/zseries/zseries_0_0_116.bin": "0.0.116",
    "app/firmware/zseries/zseries_0_0_119.bin": "0.0.119",
}


def qualify_stock(image, model):
    """Identify the selected application, independently of the name on the stack."""
    if model not in ("z-series", "z1", "z2", "z3", "z4"):
        raise ValueError("Expected a Z-series brewing module")
    app = memoryview(image)[esp_stock_partitions.selected_app_slice(image)]
    manifest = json.loads((FIRMWARE / "upstream-manifest.json").read_bytes())

    def version(candidate):
        for entry in manifest["files"]:
            name = VERSIONS.get(entry["source_path"])
            if name and entry["size"] <= len(candidate):
                if hashlib.sha256(candidate[:entry["size"]]).hexdigest() == entry["sha256"]:
                    return name
        return ""

    selected = version(app)
    if not selected:
        raise ValueError("Selected ESP32 app does not match an archived Z-series stock image")
    esp_stock_partitions.verify_app_slots(image, version)
    return selected


def qualify_install_layout(image, model):
    if model != "z-series":
        raise ValueError("Choose an individual Z-series module; stack size does not identify firmware")
    if qualify_stock(image, model) != "0.0.119":
        raise ValueError("Generic Z-series setup requires the verified 0.0.119 application")
    esp_stock_partitions.qualify_install_layout(image, preserve_z_eeprom=True)
