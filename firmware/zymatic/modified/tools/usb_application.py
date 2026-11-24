"""Make a private standalone application for a Zymatic's verified stock flash."""

import hashlib
import json
import os
from pathlib import Path
import re
import secrets

import usb_layout
import usb_stock as stock

PAGE = 256
EXTENSION_LIMIT = usb_layout.CONSERVATIVE


def read_key(path, *, create):
    if not path:
        raise ValueError("Use an absolute private key file path")
    path = Path(path)
    if not path.is_absolute():
        raise ValueError("Use an absolute private key file path")
    try:
        raw = stock.private_files.read_regular(path, 66, private=True)
    except FileNotFoundError:
        if not create:
            raise ValueError("Updating an installed image requires its existing private API key")
        key = secrets.token_bytes(32)
        stock.save_private(path, key.hex().encode() + b"\n")
        return key
    if not re.fullmatch(rb"[0-9a-fA-F]{64}(\r?\n)?", raw):
        raise ValueError("Expected a private key containing 64 hexadecimal characters")
    return bytes.fromhex(raw.strip().decode())


def end_page(length, limit=EXTENSION_LIMIT):
    if not stock.APP_SIZE < length <= limit <= usb_layout.MAX_APPLICATION or limit % PAGE:
        raise ValueError("Standalone application falls outside its verified flash region")
    return (length + PAGE - 1) // PAGE * PAGE


def install_pages(before, original, installed, key_file, host, offline_program=None, *, limit=EXTENSION_LIMIT):
    if len(before) != 128 * 1024:
        raise ValueError("Standalone installation requires a complete 128 KiB flash read")
    if not installed and not stock.is_original_app(before[:stock.APP_SIZE]):
        raise ValueError("Initial conversion requires verified Zymatic stock 1.1.14 on the connected brewer")
    if not stock.is_original_app(original):
        raise ValueError("Installation requires the verified original 1.1.14 application")
    previous_end = stock.APP_SIZE if not installed else installed["application_bytes"]
    if previous_end > limit:
        raise ValueError("Verify this unit’s configured boot region before changing its installed application")
    if before[previous_end:limit].strip(b"\xff"):
        raise ValueError("Occupied flash above the installed application prevents standalone setup")
    uid = installed["uid"] if installed else secrets.token_hex(16)
    # The Hub selects a distinct key by application UID even for different
    # stock machines connected to the same physical USB port.
    path = key_file(uid) if callable(key_file) else key_file
    key = read_key(path, create=not installed)
    if installed:
        prior_release = usb_layout.installed_release(installed)
        prior, _ = prior_release.build_application(original, key, uid, installed["hub_host"], installed["offline_program"])
        if before[:len(prior)] != prior:
            raise ValueError("Supplied API key does not match the installed image")
        if offline_program is None:
            offline_program = installed["offline_program"]
    hub_host = host or (installed["hub_host"] if installed else "picobrew.com")
    release = usb_layout.best_fit(limit)
    target, metadata = release.build_application(original, key, uid, hub_host, offline_program)
    if not release.recognize_private_application(target) or metadata["uid"] != uid:
        raise ValueError("The generated standalone image failed verification")
    end = end_page(max(len(target), previous_end), limit)
    return target.ljust(end, b"\xff"), metadata


def transaction_prefix(identity):
    if not identity:
        return None
    _, serial, location = identity
    if not serial and not location:
        return None
    # A genuine USB serial survives a port change. When only the USB topology
    # is available, retain qualification on that connection and compare flash.
    scope = ("serial", serial) if serial else ("location", location)
    return "transaction-" + hashlib.sha256(json.dumps(scope).encode()).hexdigest() + "-"


def latest_transaction(directory, prefix):
    if prefix is None:
        return None
    records = (p for p in directory.glob(prefix + "*.json")
               if re.fullmatch(re.escape(prefix) + r"[0-9]{8}", p.stem))
    return max(records, default=None)


def remember_transaction(directory, identity, before, target, standalone, extent):
    prefix = transaction_prefix(identity)
    if prefix is None:
        raise ValueError("USB identity is required to journal the Zymatic application change")
    latest = latest_transaction(directory, prefix)
    sequence = int(latest.stem[-8:]) + 1 if latest else 1
    if sequence > 99999999:
        raise ValueError("Zymatic transaction sequence is exhausted")
    record = {"extent": extent}
    for name, contents in (("before", before), ("target", target), ("standalone", standalone)):
        digest = hashlib.sha256(contents).hexdigest()
        stock.save_private(directory / ("avr-flash-" + digest + ".bin"), contents)
        record[name] = digest
    # Publish the immutable record last, before the first programmer write.
    path = directory / f"{prefix}{sequence:08d}.json"
    stock.save_private(path, (json.dumps(record, sort_keys=True) + "\n").encode())
    if os.name == "posix":
        fd = os.open(directory, os.O_RDONLY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)
    return path


def matches_interrupted_write(live, before, target, end):
    """AVRDUDE issues ascending page writes. Only its current page may be torn."""
    source_only = False
    for offset in range(0, end, PAGE):
        span = slice(offset, offset + PAGE)
        page = live[span]
        if not source_only and page == target[span]:
            continue
        if page != before[span] and source_only:
            return False
        # This page is still from the baseline, or is part way through a page
        # erase/program. All later pages must exactly match the captured input.
        source_only = True
    return True


def verified_transaction(path):
    try:
        digest = stock.private_files.read_regular(path.with_suffix(".verified"), 64, private=True)
    except FileNotFoundError:
        return False
    if not re.fullmatch(rb"[0-9a-f]{64}", digest):
        raise ValueError("Invalid private Zymatic USB verification record")
    return True


def finish_transaction(path, live):
    stock.save_private(path.with_suffix(".verified"), hashlib.sha256(live).hexdigest().encode())


def interrupted_standalone(path, live, original, *, pending):
    directory = path.parent
    record = json.loads(stock.private_files.read_regular(path, 1024, private=True))
    if not isinstance(record, dict) or set(record) != {"before", "target", "standalone", "extent"}:
        raise ValueError("Invalid private Zymatic USB transaction")
    images = {}
    for name in ("before", "target", "standalone"):
        digest = record[name]
        if not isinstance(digest, str) or not re.fullmatch("[0-9a-f]{64}", digest):
            raise ValueError("Invalid private Zymatic USB transaction digest")
        data = stock.private_files.read_regular(directory / ("avr-flash-" + digest + ".bin"),
                                               len(live), private=True)
        if len(data) != len(live) or digest != hashlib.sha256(data).hexdigest():
            raise ValueError("Saved Zymatic USB transaction image failed verification")
        images[name] = data
    standalone = images["standalone"]
    metadata = usb_layout.identify(standalone)
    end = record["extent"]
    prior = usb_layout.identify(images["before"])
    extent = max(metadata["application_bytes"] if metadata else 0,
                 prior["application_bytes"] if prior else 0)
    if not metadata or type(end) is not int or end != end_page(extent, usb_layout.MAX_APPLICATION):
        raise ValueError("Saved standalone transaction layout failed verification")
    before, target = images["before"], images["target"]
    if (before[end:] != target[end:] or target[end:] != standalone[end:]
            or (target != standalone and target[:end] != original.ljust(end, b"\xff"))):
        raise ValueError("Saved Zymatic USB transaction target or bounds failed verification")
    if live[end:] != before[end:] or not matches_interrupted_write(live, before, target, end):
        if not pending:
            # A completed historical transaction cannot authorize or block a
            # stock-prefix restore after another application was installed.
            return None
        raise ValueError("The live flash differs from the last USB transaction for this brewer; "
                         "its custom application area cannot be safely restored")
    return standalone, metadata
