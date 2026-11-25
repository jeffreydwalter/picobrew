"""Select a Zymatic application using the boot window verified for this unit."""

from functools import partial
from types import SimpleNamespace

import hashlib
import json
import re

import boot_evidence
import zymatic_build as build
import compact_build
import wifi_build
import usb_stock as stock

CONSERVATIVE = 0x1e000
MAX_APPLICATION = 0x1fc00


def release_for(profile, version=build.VERSION):
    if profile == build.PROFILE and version in (build.VERSION, *build.ARCHIVED_MANIFESTS):
        module = build
    elif profile == compact_build.PROFILE and version in (build.VERSION, build.INITIAL_RELEASE, compact_build.PREVIEW_VERSION):
        module = compact_build
    else:
        raise ValueError("Unknown Zymatic build profile or firmware version")
    if version == build.VERSION:
        return wifi_build.EXTENDED if profile == build.PROFILE else wifi_build.COMPACT
    return SimpleNamespace(
        VERSION=version.partition("-")[0], PROFILE=profile, template=partial(module.template, version=version),
        build_application=partial(module.build_application, version=version),
        recognize_private_application=partial(module.recognize_private_application, version=version),
    )


def known_releases():
    yield wifi_build.COMPACT
    yield wifi_build.EXTENDED
    yield compact_build
    yield build
    yield release_for(compact_build.PROFILE, build.INITIAL_RELEASE)
    yield release_for(compact_build.PROFILE, compact_build.PREVIEW_VERSION)
    for version in build.ARCHIVED_MANIFESTS:
        yield release_for(build.PROFILE, version)


def installed_release(installed):
    for release in known_releases():
        if release.PROFILE == installed["build_profile"] and release.VERSION == installed["firmware_version"]:
            if release.template()[1]["extension_sha256"] == installed["extension_sha256"]:
                return release
    raise ValueError("Unknown installed Zymatic firmware build")


def identify(flash):
    if stock.is_original_app(flash[:stock.APP_SIZE]):
        return None
    for release in known_releases():
        installed = release.recognize_private_application(flash)
        if installed:
            return installed
    return None


def best_fit(limit):
    if limit not in (CONSERVATIVE, 0x1f000, 0x1f800, MAX_APPLICATION):
        raise ValueError("Unknown Zymatic application window")
    return wifi_build.EXTENDED if limit >= wifi_build.EXTENDED.limit else wifi_build.COMPACT


def qualified_limit(directory, flash, installed):
    """Match a prior ISP measurement using the provisioned unit and exact boot bytes.

    A shared stock image and the same USB port cannot identify which unit
    supplied the fuses. Convert stock using the conservative layout first.
    Conflicting fuse observations use the smaller application window.
    """
    if not installed or len(flash) != boot_evidence.FLASH_BYTES:
        return CONSERVATIVE
    limits = []
    for path in directory.glob("isp-evidence-v1-*.json"):
        raw = stock.private_files.read_regular(path, 16384, private=True)
        if path.name != "isp-evidence-v1-" + hashlib.sha256(raw).hexdigest() + ".json":
            continue
        report = json.loads(raw)
        digest = report.get("flash_sha256", "")
        if (report.get("schema_version") != 1 or report.get("interface") != "atmelice_isp"
                or report.get("signature") != "1e9705" or not re.fullmatch("[a-f0-9]{64}", digest)
                or digest != report.get("usb_backup_sha256")):
            continue
        measured = stock.private_files.read_regular(directory / f"isp-flash-{digest}.bin",
                                                    boot_evidence.FLASH_BYTES, private=True)
        if len(measured) != len(flash) or hashlib.sha256(measured).hexdigest() != digest:
            continue
        prior = identify(measured)
        if not prior or prior["uid"] != installed["uid"]:
            continue
        hfuse = int(report["registers"]["hfuse"], 16)
        lock = int(report["registers"]["lock"], 16)
        size = boot_evidence.BOOT_BYTES[(hfuse >> 1) & 3]
        region = report["configured_boot_region"]
        same_boot = measured[-size:] == flash[-size:] and len(set(flash[-size:])) > 1
        readable = lock & 3 in (2, 3) and lock & 12 == 12
        if (not same_boot or not readable or hfuse & 1 or region["size_bytes"] != size
                or region["sha256"] != hashlib.sha256(flash[-size:]).hexdigest()):
            limits.append(CONSERVATIVE)
        else:
            limits.append(len(flash) - size)
    return min(limits, default=CONSERVATIVE)
