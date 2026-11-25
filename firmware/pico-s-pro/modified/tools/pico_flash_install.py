#!/usr/bin/env python3
"""Provision, install or restore a supported Pico S, Pro or C over direct CH340 USB.

Keep this script beside pico_flash_backup_v2.py, pico_uart_probe.py,
pico_flash_preflight.py, and pico_inspect_backup.py. USB write operations
require a new absolute --output JSON path. Provide the exact original
full-flash backup, or use --oem-directory for restore-stock. All input images
are verified locally before USB is opened. Reports omit raw flash and credentials.

provision-key writes only /local/api.key, then proves an existing-file rewrite
of that same key. install repeats that proof, installs and reads back the exact
pinned browser bundle, then updates the existing /sys/mcuimg2.bin allocation.
restore rewrites only that existing application allocation
with the exact original image. restore-stock restores both the application and
the OEM recovery image from the profiled S/Pro. On Pico C the original
recovery must still match the backup and only the OEM application is restored. All commands
leave the boot record intact. None invokes a raw erase, deletes
files, formats flash, relocates allocations, or starts the application.

Opening an existing file for write erases that file's contents. An interrupted
application write can require restore. Restore attempts mode 0x1000 only;
it stops if the filesystem cannot reopen the existing allocation. It does not
claim recovery from every interrupted or damaged filesystem state.

With USB connected, power on or reset the idle Pico once at the initial
BREAK prompt. Leave it powered and connected until verification finishes.
"""

import argparse
from contextlib import contextmanager
from dataclasses import dataclass
from datetime import datetime, timezone
import hashlib
import gzip
import hmac
import json
import os
import pico_private_files as private_files
from pathlib import Path
import re
import stat
import struct
import sys
import time
from types import MappingProxyType

import pico_flash_backup_v2 as backup
import pico_flash_preflight as preflight
import pico_inspect_backup as inspection
import pico_uart_probe as probe


INSTALL_REVISION = 20
BACKUP_SIZE = 1048576
BACKUP_SHA256 = "837a728400da02b9da4a7d563e1b67fb24d6a5af03c054963c4b1de60c72343f"
ORIGINAL_OFFSET = 130 * 4096 + 8
ORIGINAL_RECOVERY_OFFSET = 214 * 4096 + 8
UID_BLOCK = 106
# Identity of the single physical brewer whose flash layout was verified.
PROFILE_UID_SHA256 = "7290674610eca1cf26581779a71c7f41a9c2fc367eb7189d0e93918319964bc7"
ORIGINAL_SIZE = 162792
ORIGINAL_SHA256 = "e18c240fa1cf35222674bf3b4fa992a306c6e63b0b99be8793604f199085d702"
CANDIDATE_SIZE = 169808
CANDIDATE_SHA256 = "4c60e2e90bf257872759a30be3cf1af4f74882e54e4f8792eff1b259bbb83a1f"
# Earlier 1.0.0 builds, taken from the v1.0.0 tag. The printed version
# matches the new menu build, so the USB installer uses per-model hashes.
STABLE_1_0_0_APPLICATIONS = {
    "pico-pro": (170408, "4e601bfe12e420bc6b50f762de9cda41898ed03d4095e8035cf8be2972f33002"),
    "pico-s": (170392, "5b221f2f52afceab4c9fe7d1c391071b708f439d9e1be8cf883addef584852af"),
    "pico-c-ssd1325": (168496, "e52c444322bd78e58c1bf641fd2d1dc56ba003cfbfff3d1cc761737131bad383"),
    "pico-c-ssd1309": (168400, "02783efd34cba08a19d7fd719db2732b9fa12554997dbac4f9f85961fa55dc31"),
}

# Model-specific applications shipped on master before guarded recipe edits.
PREVIOUS_STABLE_APPLICATIONS = {
    "pico-s": (169304, "d92b1b073ada25ef62ebbb7c501fdf5d1a220393c873d838d480f3e1a59d7801"),
    "pico-pro": (169312, "fe522d90285ab2d48fd3f28b55887192c7661f06a0a2a3037e8c1e2f59dc8a95"),
    "pico-c-ssd1325": (167400, "66af92c03c9f94d6388ae141b596252a8992ee3847919af87d2247c4bfffa112"),
    "pico-c-ssd1309": (167296, "736fdc386e0c9ffd7a4f5bff371cc059f71bd5aceb1a5a77065ea4813809b5e1"),
}

# Pre-release applications can be recognized as USB migration sources.
# Identities were read from the archived release packages at commit 42a22ef.
LEGACY_APPLICATIONS = {
    "pico-s": (
        (165336, "10a19abc15d8dfcc95b753c31548042da2077d4700548648f8fe7e0f9481fa30"),  # 1.2.0
        (166280, "903873fbaa2c6338956c22689afc911c64635dafbc52c0b913e0613d5eff230b"),  # 1.3.0
        (166512, "4cc792a016e34becfbebe41a32e752d80358e60be8c0c7571026551619ee0833"),  # 1.3.1
        (166520, "4b3bc5d6122f696b0866d7e73607bef574208cbcc031009f7a4ccf3df5c858a4"),  # 1.3.2
        (166336, "3ed5d58c072cbb547f94fcc579730394c15c72e1126961491741fc846f91bfa4"),  # 1.3.3
        (166488, "60995c50448305c4265125109931a4ae5fbb0b8b1e960af5b060a4298021bb94"),  # 1.3.4
        (168392, "c6fc043ea1f638c6a2523170f45d1b3f15c7cf43bb8a0a1f724f0229a63a979b"),  # 1.3.5
        (169568, "d4d294e6cc58194ecb1b5d66f47d5a2636d64011bbe37131bc46c0b11fb1d540"),  # 1.3.6
        (169576, "95e8b9ff91f3b4bd101f8569c36c5372c062f7be9b9b03cc26664f531e08b1c6"),  # 1.3.7
        (169528, "c0a96fb35b1927fdafd68557e637cd444fe37b67ef4aac95c532ded0ed73e185"),  # 1.3.8
        (169528, "db924ea93c57a27e7a78539ebf4b76487f100eb0b588bc608ee84bdc8d759e9a"),  # 1.3.9
        (170288, "30e10819a9095e0c41494d24a7a290754f78dd198dbc83371264cfaf9c99ec86"),  # 1.3.10
    ),
    "pico-pro": (
        (165344, "8f40ad9c4eafecddea8bbf5be9feaad6b6f6347487b5148df523c83f001354c8"),  # 1.2.0
        (166288, "29ccc98d45e734edfd5aa259d62fa040395ef366fb48fbc63c9bfe9c91a9e832"),  # 1.3.0
        (166520, "f887663ff2f6e8e4d5936a9c81b2e02cccb5adee89c45050cb2bea9fb4a1e9be"),  # 1.3.1
        (166528, "b0cb331a7b9890c61ab7b66e0ef698da8fbedc026ad563082e24c2741a0b1f90"),  # 1.3.2
        (166344, "3828ec6a147f98bdf2b039ecd4c336bf238d04bf7cec41cdea07a97b9d170f39"),  # 1.3.3
        (166496, "0f0dcdb0c509fab19371967006aeae661a547dcc2f1c1cd1207cee16e8c45b0b"),  # 1.3.4
        (168400, "f0445c1fcf84caae1cbd8f013251b270ffa69510dba989b3ffac102303daf81c"),  # 1.3.5
        (169584, "a36e5873ace849af2f7617610c4232edb63c6a4ac5b3668ece14301d97ba085b"),  # 1.3.6
        (169584, "7919ff13dfe9fa7dde3100d673a2292241dce0188611ea7afdedfb44e7392b45"),  # 1.3.7
        (169536, "611e39b7e666a3e3df4dc4062bd179b05b778930296a65c312a30a3824dfdfbd"),  # 1.3.8
        (169536, "56de7a4798a225149b8f334db816ac7ccb575b2d4bc8c2fbaeea70847efdea59"),  # 1.3.9
        (170296, "955a408d74fb2fac188b30c922e61807c2ef878fe9314ed16ee43af4b91c0f49"),  # 1.3.10
    ),
    "pico-c-ssd1325": (
        (162152, "6c0d5d86e8ecea3e889ddee1c0a8ef816d2a37ed133a504ab107b82935310753"),  # 1.2.0
        (162488, "47c7ae5729c0b19c9e7b6005164771bd97d0eec00d11f017b3eea9447b1f01e6"),  # 1.2.1
        (162488, "c17f98e705e7d6c8ae21c297fe2c00ee987c770ebb29e063b63c84680860d943"),  # 1.2.2
        (162304, "ffe1fe5ba2e28504de897d2829343a003e684e8c4f92b14eda6c2d0fb711b174"),  # 1.2.3
        (164192, "b5b14f098652d1bd42396b22410028af86b8c5cd0d5bf8ac0a50533b40422f9f"),  # 1.2.4
        (164200, "a5d5078c426636b55595040b9e250cdb97821f2dae34e1991e1d66397bc02b4b"),  # 1.2.5
        (164200, "7d474e8f7f0803850d654acdf42b2e38fdba7cc5463b6d9c9af5054fe8a57fa6"),  # 1.2.6
        (164136, "d7c8ad7efe11eda6a009a605f1914132eb59436e56fdf9f97e138e74c439bb9f"),  # 1.2.7
        (164136, "782fa7c5f5cfc9cb3b03df0b1efc564a4c7879ce4fbc1c7a049d3e2a74f51d8a"),  # 1.2.8
        (168384, "f1f34b1f64ba5b060a60c30ed0f9b6c9d99a80c068533d6ed0f2e6ba8c8ca0f3"),  # 1.3.0
    ),
    "pico-c-ssd1309": (
        (162064, "0ff778141225f8e0463d2c3cd07113b0c2240c0338734ad37265a243f302cb9b"),  # 1.2.0
        (162392, "390ab4955ff0489157c9449df6013e205c9f2fd7b11dfdcc99b0dd5c38366641"),  # 1.2.1
        (162408, "49172d22ae8973748f9639398d13c388a46a311836780fc999f6f20545724632"),  # 1.2.2
        (162224, "32bd24c118c18cabd29720894ad10647f465050f6786cd548dce874064a7f1df"),  # 1.2.3
        (164088, "c5fab011051798e6ee93a934b9e0ad512303cf64028400bfda64957de3663424"),  # 1.2.4
        (164112, "f1096e145e485d732ccf55df2efc7d983a740639aed8656f282ad92857bd3bb7"),  # 1.2.5
        (164120, "ee826e15c1146077c78ef76f146174f7f669ae2e82ae8e7cbd0ab98fee22dc2e"),  # 1.2.6
        (164056, "0bbd93aa290347c436cc50b801df06a3d58111dcfc850f2b0d817eb82999424e"),  # 1.2.7
        (164056, "293506900d6fe720afe081cc5b8aff42b5b6101e81876d5c9e82dde098559ffb"),  # 1.2.8
        (168288, "ff2030d4c019b4d7ae3fe6a88036cbacbb5ff04ff35f93ffcac6f7226a0c3930"),  # 1.3.0
    ),
}
# Previously installed standalone versions are approved upgrade sources,
# never alternate payloads accepted by --firmware. Keep these identities fixed
# when the target CANDIDATE_* values change.
PREVIOUS_CANDIDATE_SIZE = 161720
PREVIOUS_CANDIDATE_SHA256 = "a8fc16afce43c4b91b82374a339b86631daefe1a40f08df5f8bdd7a0d0a8d904"
RELEASED_1_0_1_SIZE = 164600
RELEASED_1_0_1_SHA256 = "7f1ce21158b6f0f0297087e37c71cda23926e1a5c4902d938f4f0162929d36ce"
RELEASED_1_0_0_SIZE = 164600
RELEASED_1_0_0_SHA256 = "652f94f941a4551ebb4a2df17e818dec3b3aebd2fc1cf43ec6bcb2e5ec684558"
RELEASED_1_1_0_SIZE = 165656
RELEASED_1_1_0_SHA256 = "a5e7fc51841ad0444e6b3a731af48dcdac214cce65c04b54b373001b543f6df4"
BOOT_INFO = bytes.fromhex("01 3d 00 20 ba dc cd ab")
BOOT_INFO_SHA256 = "59112b9c5cd615ee3427b916c2a35a0a89a40a80250197169a545fb93b8e9435"
APP_NAME = "/sys/mcuimg2.bin"
RECOVERY_NAME = "/sys/mcuimg1.bin"
RECOVERY_FILE = Path(__file__).resolve().parents[1] / "firmware/standalone/pico-hub-recovery.bin"
RECOVERY_IMAGE = {"size_bytes": 43352, "sha256": "ae8e361a323c5c999b72d8c8764d0e5c4227d77d49ca47347c7f9f38c8fe6ed9"}
# The pinned nonmirrored allocation is 48 x 4096 bytes. The CC3200 SDK's
# _sl_GetCreateFsMode rounds this size range to 1024-byte pages, then TI's
# filesystem adds metadata and rounds to 4096-byte blocks (SWRA657 section 4;
# SWRU368C conservatively budgets 512 metadata bytes). A 48-block allocation
# requires a rounded creation maximum of at least 47 blocks. Reserve another
# complete block below that boundary, also covering up to 4096-byte creation
# granularity. This 184 KiB limit is derived conservatively, not a live
# AllocatedLen measurement: the pinned UART helper exposes only FileLen.
APPLICATION_ALLOCATION_BLOCKS = 48
APPLICATION_PAYLOAD_LIMIT = (APPLICATION_ALLOCATION_BLOCKS - 2) * 4096
KEY_NAME = "/local/api.key"
KEY_SIZE = 65
# Shared by the stable Pico S and Pico Pro packages.
WEB_MANIFEST_SHA256 = "0542e44f32d604aa46062ae739490e003dde9ea359f174eb01e539cfbc0a5f16"
WEB_DIRECTORY = Path(__file__).resolve().parents[1] / "firmware/web"
C_FIRMWARE = Path(__file__).resolve().parents[3] / "pico-c/modified/firmware"
C_RECOVERY = {
    "pico-c-ssd1325": {"size_bytes": 43912, "sha256": "5d0b4ed287132e817c768b372b61ed5999eca3865d5615edbf69c03448935990"},
    "pico-c-ssd1309": {"size_bytes": 43912, "sha256": "ec79b06f6805915f93b276ca01310ff86fcd865d56a81d71fbb0c2fe0f2070cf"},
}
LEGACY_RECOVERY = {
    "pico-s-pro": {"size_bytes": 43352, "sha256": "c76c390034d1564d4d80ddeeae0347b2e7171c820878b44d0fce7731c359c9ce"},
    "pico-c-ssd1325": {"size_bytes": 43912, "sha256": "c17c462edf7f9b61ee84dc1c6fb8a33d3a48d8b0007a18bfbc38cb646c387141"},
    "pico-c-ssd1309": {"size_bytes": 43912, "sha256": "46971bb53210fb8a3086b8ff3d2e8a0d0acd2b6680b568ee2b64f504c8bbddd4"},
}
C_STOCK = {
    "pico-c-ssd1325": (156536, "ee732d03a1869985f9353e4208073ebfd1182df11d6d9f0dbf834ee76fcdd45b"),
    "pico-c-ssd1309": (155864, "6f2dedfcd7ab10b5a421b05dc4ee792cd44753d7e302fd8ee645ca4f6556c226"),
}
C_STANDALONE = {
    "pico-c-ssd1325": (167848, "9dbd988506d43f2c1d227638de003bc498a0aa9c11c90de58fa4c8606c45b1bd"),
    "pico-c-ssd1309": (167768, "d424f2bc9f6a7836a2b84d74dbd1d5bcddd83daac18f7b46f6249e0110c2a669"),
}
C_WEB_MANIFEST_SHA256 = "2583ac79cf47c31e258b5d2aa8b8087440c4d7597ff4bd905395838acee34ae9"
UID_NAME = "/sys/uid.txt"
LOADER_NAME = "/sys/mcuimg.bin"
# These counts, flags and mirroring define the supported TI allocation shape.
# The actual block addresses for a newly enrolled brewer come from its backup.
SLOT_GEOMETRY = {
    UID_NAME: (1, False, 0xC),
    LOADER_NAME: (4, True, 0),
    preflight.BOOT_INFO_NAME: (1, True, 8),
    RECOVERY_NAME: (16, False, 0xC),
    APP_NAME: (APPLICATION_ALLOCATION_BLOCKS, False, 0xC),
}
WEB_FILES = (("index.html", "/local/web/i.gz"), ("app.js", "/local/web/a.gz"),
             ("api-explorer.js", "/local/web/e.gz"), ("history.js", "/local/web/h.gz"),
             ("style.css", "/local/web/s.gz"))
WEB_NAMES = frozenset(path for _, path in WEB_FILES)
READ_NAMES = frozenset((*preflight.ALLOWED_FILES, KEY_NAME, *WEB_NAMES))
PROTECTED_NAMES = ("/sys/mcuimg.bin", "/sys/mcuimg1.bin", preflight.BOOT_INFO_NAME)
READ_CHUNK_SIZE = 256
WRITE_CHUNK_SIZE = 4096
LAYOUT_BYTES = 5 * 4096
OPEN_READ = 0
OPEN_EXISTING_WRITE = 0x1000
OPEN_KEY_CREATE = 0x3001
FILE_CLOSE_COMMAND = preflight.FILE_CLOSE_COMMAND
SUCCESS_MARKERS = {
    "provision-key": "SUCCESS: API key provisioning and existing-file write verification completed",
    "install": "SUCCESS: standalone firmware, browser files and API key verified",
    "install-recovery": "SUCCESS: pinned recovery and unchanged active application verified",
    "restore": "SUCCESS: original firmware restored and read back exactly",
    "restore-stock": "SUCCESS: OEM bootloader, boot selection, recovery and application verified; image3 absent",
}


class InstallError(backup.BackupError):
    """Messages must never include key contents, tokens, or raw receive bytes."""


class HandshakeError(InstallError):
    """An actionable ACK failure containing only fixed labels and numbers."""

    def __init__(self, diagnostic):
        self.diagnostic = dict(diagnostic)
        reason = {"timeout": "timed out", "nack": "NACK received",
                  "byte_limit": "receive limit reached without ACK"}[diagnostic["reason"]]
        message = (f"{diagnostic['stage']}: {reason}; "
                   f"received {diagnostic['received_bytes']} bytes")
        if "timeout_seconds" in diagnostic:
            message += f"; wait limit {diagnostic['timeout_seconds']:g}s"
        if "elapsed_seconds" in diagnostic:
            message += f"; elapsed {diagnostic['elapsed_seconds']:.3f}s"
        super().__init__(message)


def _handshake_diagnostic(exc):
    """Decode only the pinned probe's ACK-error templates, never echo RX.

    Keeping this conversion outside the probe preserves the connection code
    that succeeded on the owner's hardware. Unrecognized exceptions remain
    redacted. Hex tokens are counted, not retained in reports or messages.
    """
    original = exc
    for _ in range(3):
        if isinstance(original, probe.ProbeError):
            break
        original = original.__cause__
        if original is None:
            return None
    if not isinstance(original, probe.ProbeError):
        return None
    message = str(original)
    stage_pattern = r"(?P<stage>INITIAL ACK|COMMAND ACK|APPS INITIAL ACK)"
    tokens = r"(?:NO BYTES|[0-9a-f]{2}(?: [0-9a-f]{2}){0,1023})"
    match = re.fullmatch(stage_pattern + r": timeout after "
                         r"(?P<seconds>[0-9]+(?:\.[0-9]+)?(?:e[+-]?[0-9]+)?)s\. RX: "
                         + r"(?P<rx>" + tokens + r")", message)
    if match:
        received = 0 if match["rx"] == "NO BYTES" else (len(match["rx"]) + 1) // 3
        seconds = float(match["seconds"])
        if not 0 < seconds <= 300:
            return None
        return {"stage": match["stage"], "reason": "timeout",
                "received_bytes": received, "timeout_seconds": seconds}
    match = re.fullmatch(stage_pattern + r": NACK received\. RX: (?P<rx>" + tokens + r")", message)
    if match and match["rx"].endswith("00 33"):
        return {"stage": match["stage"], "reason": "nack",
                "received_bytes": (len(match["rx"]) + 1) // 3}
    match = re.fullmatch(stage_pattern + r": received 1024 bytes without an ACK\. "
                         r"First 64: [0-9a-f]{2}(?: [0-9a-f]{2}){63}", message)
    if match:
        return {"stage": match["stage"], "reason": "byte_limit", "received_bytes": 1024}
    return None


def _safe_exception(exc, elapsed_seconds=None):
    if isinstance(exc, InstallError):
        return exc
    diagnostic = _handshake_diagnostic(exc)
    if diagnostic is not None:
        if elapsed_seconds is not None:
            diagnostic["elapsed_seconds"] = round(max(0.0, elapsed_seconds), 3)
        return HandshakeError(diagnostic)
    return InstallError(_safe_error(exc))


def report(message):
    print(message, flush=True)


def _safe_error(exc):
    if isinstance(exc, InstallError):
        return str(exc)
    if isinstance(exc, KeyboardInterrupt):
        return "Interrupted"
    diagnostic = _handshake_diagnostic(exc)
    if diagnostic is not None:
        return str(HandshakeError(diagnostic))
    return f"{type(exc).__name__}: operation failed; device payload omitted"


def _read_regular(path, limit, private=False, owner=None):
    """Read a bounded regular file through one descriptor; reject symlinks."""
    try:
        return private_files.read_regular(path, limit, private=private, owner=owner)
    except private_files.PrivateFileError as exc:
        raise InstallError(str(exc)) from None
    except Exception:
        raise InstallError("Could not safely read the required local input file") from None


def _verified_backup_image(path):
    image = _read_regular(path, BACKUP_SIZE)
    digest = hashlib.sha256(image).hexdigest()
    if len(image) != BACKUP_SIZE or digest != BACKUP_SHA256:
        raise InstallError("The full backup does not match the pinned original 1 MiB backup")
    return image


def _original_application(image):
    header = image[ORIGINAL_OFFSET - 8:ORIGINAL_OFFSET]
    if header[6:8] != b"LS" or int.from_bytes(header[:3], "little") != ORIGINAL_SIZE:
        raise InstallError("The pinned backup has an unexpected original-image header")
    original = image[ORIGINAL_OFFSET:ORIGINAL_OFFSET + ORIGINAL_SIZE]
    if len(original) != ORIGINAL_SIZE or hashlib.sha256(original).hexdigest() != ORIGINAL_SHA256:
        raise InstallError("The original image extracted from the backup did not verify")
    return original, {"size_bytes": len(image), "sha256": BACKUP_SHA256,
                      "original_image_size_bytes": len(original),
                      "original_image_sha256": ORIGINAL_SHA256}


def verify_backup(path):
    return _original_application(_verified_backup_image(path))


def _read_uid_record(record):
    size = int.from_bytes(record[:3], "little")
    data = record[8:8 + size]
    if (record[6:8] != b"LS" or size not in (32, 33) or len(data) != size
            or re.fullmatch(rb"[a-f0-9]{32}\x00?", data) is None):
        raise InstallError("The brewer identity file could not be verified")
    return data[:32]


def load_stock_backup(path):
    image = _verified_backup_image(path)
    original, metadata = _original_application(image)
    offset = ORIGINAL_RECOVERY_OFFSET
    expected = preflight.EXPECTED_FILES[RECOVERY_NAME]
    size = expected["size_bytes"]
    recovery = image[offset:offset + size]
    if (image[offset - 2:offset] != b"LS" or int.from_bytes(image[offset - 8:offset - 5], "little") != size
            or _image_metadata(recovery) != expected):
        raise InstallError("The original recovery image extracted from the backup did not verify")
    uid = _read_uid_record(image[UID_BLOCK * 4096:UID_BLOCK * 4096 + 41])
    return original, metadata, recovery, hashlib.sha256(uid).digest()


def load_stock_directory(directory):
    images = {}
    for name in (APP_NAME, RECOVERY_NAME):
        expected = _expected_public(name)
        path = directory / Path(name).name
        payload = _read_regular(path, expected["size_bytes"])
        if _image_metadata(payload) != expected:
            raise InstallError(f"The raw OEM image {path.name} differs from the profiled image")
        images[name] = payload
    return images


def validate_key(data):
    if not isinstance(data, bytes) or re.fullmatch(rb"[0-9a-f]{64}\n", data) is None:
        raise InstallError("The host key must contain exactly 64 lowercase hexadecimal characters and LF")
    return data


def load_key(path, owner=None):
    return validate_key(_read_regular(path, KEY_SIZE, private=True, owner=owner))


def _validate_application_size(size):
    if not 8 <= size <= APPLICATION_PAYLOAD_LIMIT:
        raise InstallError("Application payload exceeds the conservative existing-allocation capacity limit")


def candidate_identity(model):
    if model == "pico-pro":
        return CANDIDATE_SIZE, CANDIDATE_SHA256
    if model == "pico-s":
        return 169792, "4dd458facba2698d988862e4d74d87cff70b8d62cf3ca95860559e18122f42dd"
    if model in C_STANDALONE:
        return C_STANDALONE[model]
    raise InstallError("Unknown target model")


def load_candidate(path, model="pico-pro"):
    size, digest = candidate_identity(model)
    _validate_application_size(size)
    data = _read_regular(path, size)
    if len(data) != size or hashlib.sha256(data).hexdigest() != digest:
        raise InstallError("The firmware does not match the pinned standalone image")
    return data


def parse_web_manifest(data, model="pico-pro"):
    """Return immutable, exact-path allocation metadata for the pinned bundle."""
    expected_hash = C_WEB_MANIFEST_SHA256 if model in C_STOCK else WEB_MANIFEST_SHA256
    if (not isinstance(data, bytes) or len(data) > 16384
            or hashlib.sha256(data).hexdigest() != expected_hash):
        raise InstallError("The browser manifest does not match the pinned firmware bundle")
    try:
        manifest = json.loads(data)
        if (set(manifest) != {"schema_version", "block_size", "metadata_per_file", "fail_safe", "assets"}
                or manifest["schema_version"] != 1 or manifest["block_size"] != 4096
                or manifest["metadata_per_file"] != 512 or manifest["fail_safe"] is not False
                or not isinstance(manifest["assets"], list)
                or len(manifest["assets"]) != len(WEB_FILES)):
            raise ValueError()
        entries = {}
        fields = {"source", "file_path", "payload", "content_type", "content_encoding", "length",
                  "max_size", "allocation_blocks", "sha256", "source_length", "source_sha256"}
        for item, (source, destination) in zip(manifest["assets"], WEB_FILES):
            if (set(item) != fields or item["source"] != source or item["file_path"] != destination
                    or item["payload"] != source + ".gz" or item["content_encoding"] != "gzip"
                    or any(type(item[field]) is not int for field in
                           ("length", "max_size", "allocation_blocks", "source_length"))
                    or not 1 <= item["length"] <= 65024 or not 1 <= item["source_length"] <= 524288
                    or re.fullmatch(r"[0-9a-f]{64}", item["sha256"]) is None
                    or re.fullmatch(r"[0-9a-f]{64}", item["source_sha256"]) is None):
                raise ValueError()
            blocks = (item["length"] + 512 + 4095) // 4096
            if item["allocation_blocks"] != blocks or item["max_size"] != blocks * 4096 - 512:
                raise ValueError()
            # _sl_GetCreateFsMode in the pinned CC3200 SDK: 256-byte units,
            # CREATE_IF_ABSENT access, no COMMIT/mirroring/security flags.
            entry = dict(item, create_mode=0x3000 | (item["max_size"] // 256))
            entries[destination] = MappingProxyType(entry)
        return MappingProxyType(entries)
    except Exception:
        raise InstallError("The pinned browser manifest has an invalid schema or allocation") from None


def load_web_bundle(directory, model="pico-pro"):
    directory = Path(directory)
    manifest = _read_regular(directory / "manifest.json", 16384)
    entries = parse_web_manifest(manifest, model)
    payloads = {}
    for name, entry in entries.items():
        data = _read_regular(directory / entry["payload"], entry["length"])
        if (len(data) != entry["length"] or hashlib.sha256(data).hexdigest() != entry["sha256"]):
            raise InstallError("A browser file differs from the pinned bundle")
        try:
            raw = gzip.decompress(data)  # Includes gzip footer/CRC validation.
        except Exception:
            raise InstallError("A pinned browser file is not valid gzip") from None
        if (len(raw) != entry["source_length"] or hashlib.sha256(raw).hexdigest() != entry["source_sha256"]):
            raise InstallError("A browser file's expanded contents differ from the pinned bundle")
        payloads[name] = data
    return manifest, payloads


def _image_metadata(data):
    return {"size_bytes": len(data), "sha256": hashlib.sha256(data).hexdigest()}


def _approved_current_applications(mode, model="pico-pro"):
    """Exact live images allowed before mutation; install payload stays pinned."""
    identities = [C_STOCK.get(model, (ORIGINAL_SIZE, ORIGINAL_SHA256))]
    if mode == "install":
        if model not in C_STOCK:
            identities.extend(((162736, "f018cb90820c3cbf9cad201d4e3a95635761695e5cc2fcdee819ed6fa19145a7"),
                               (PREVIOUS_CANDIDATE_SIZE, PREVIOUS_CANDIDATE_SHA256),
                               (RELEASED_1_0_0_SIZE, RELEASED_1_0_0_SHA256),
                               (RELEASED_1_0_1_SIZE, RELEASED_1_0_1_SHA256),
                               (RELEASED_1_1_0_SIZE, RELEASED_1_1_0_SHA256)))
        identities.extend(LEGACY_APPLICATIONS[model])
        identities.extend((STABLE_1_0_0_APPLICATIONS[model], PREVIOUS_STABLE_APPLICATIONS[model]))
        identities.append(candidate_identity(model))
    if mode == "install-recovery":
        identities = [STABLE_1_0_0_APPLICATIONS[model], PREVIOUS_STABLE_APPLICATIONS[model], candidate_identity(model)]
    return tuple({"size_bytes": size, "sha256": digest} for size, digest in identities)


def _expected_public(name, model="pico-pro", profile=None):
    if name == APP_NAME and model in C_STOCK:
        size, digest = C_STOCK[model]
        return {"size_bytes": size, "sha256": digest}
    if name == RECOVERY_NAME and profile is not None:
        return profile.recovery_identity
    if name == preflight.BOOT_INFO_NAME:
        return {"size_bytes": 8, "sha256": BOOT_INFO_SHA256}
    return preflight.EXPECTED_FILES[name]


def _protected_matches(name, observed, model="pico-pro", profile=None):
    return (observed == _expected_public(name, model, profile)
            or (name == RECOVERY_NAME and observed in (C_RECOVERY.get(model, RECOVERY_IMAGE),
                                                       LEGACY_RECOVERY[model if model in C_STOCK else "pico-s-pro"])))


def build_connection(cc, probe_module, port, helper, mode, payloads, journal, web_manifest=None,
                     model="pico-pro", uid_digest=None, stock_profile=None):
    """Create a session whose complete write bytes and destinations are pinned.

    The inherited guard still owns the exact SRAM-helper upload. Only this
    class's fully checked filesystem commands bypass its read-only rejection.
    A protocol failure poisons the session; no command/chunk retry is possible.
    """
    if mode not in SUCCESS_MARKERS:
        raise InstallError("Unknown operation")
    if stock_profile is not None and (stock_profile.model != model or uid_digest != stock_profile.uid_digest):
        raise InstallError("The qualified stock backup and selected brewer disagree")
    if model in C_STOCK and (stock_profile is None or mode not in ("provision-key", "install", "install-recovery", "restore-stock")):
        raise InstallError("Pico C requires its qualified stock backup and a matching display")
    immutable = MappingProxyType(dict(payloads))
    web_assets = parse_web_manifest(web_manifest, model) if mode == "install" else MappingProxyType({})
    expected_names = {APP_NAME} if mode in ("restore", "restore-stock") else {KEY_NAME}
    if mode == "restore-stock":
        expected_names.add(RECOVERY_NAME)
        if (not isinstance(uid_digest, bytes) or len(uid_digest) != 32
                or not isinstance(immutable.get(RECOVERY_NAME), bytes)
                or _image_metadata(immutable[RECOVERY_NAME]) != _expected_public(RECOVERY_NAME, model, stock_profile)):
            raise InstallError("Stock restore requires the profiled brewer identity and verified OEM recovery")
    if mode == "install":
        expected_names.add(APP_NAME)
        expected_names.update(web_assets)
    if mode == "install-recovery":
        expected_names.add(RECOVERY_NAME)
        if not isinstance(immutable.get(RECOVERY_NAME), bytes) or _image_metadata(immutable[RECOVERY_NAME]) != C_RECOVERY.get(model, RECOVERY_IMAGE):
            raise InstallError("Recovery bytes do not match the pinned local recovery image")
    if set(immutable) != expected_names or any(not isinstance(v, bytes) for v in immutable.values()):
        raise InstallError("Write payload registration does not match the selected operation")
    if KEY_NAME in immutable:
        validate_key(immutable[KEY_NAME])
    if APP_NAME in immutable:
        _validate_application_size(len(immutable[APP_NAME]))
        expected = (C_STOCK.get(model, (ORIGINAL_SIZE, ORIGINAL_SHA256)) if mode in ("restore", "restore-stock")
                    else candidate_identity(model))
        if (len(immutable[APP_NAME]), hashlib.sha256(immutable[APP_NAME]).hexdigest()) != expected:
            raise InstallError("Registered application payload is not the pinned image")
    for name, entry in web_assets.items():
        if (len(immutable[name]) != entry["length"]
                or hashlib.sha256(immutable[name]).hexdigest() != entry["sha256"]):
            raise InstallError("Registered browser payload differs from the pinned bundle")
    guarded = backup.guarded_connection(cc, probe_module, port, helper)

    class FileConnection(type(guarded)):
        def __init__(self):
            super().__init__()
            self.operation = mode
            self.target_model = model
            self.stock_profile = stock_profile
            self.web_manifest_sha256 = hashlib.sha256(web_manifest).hexdigest() if mode == "install" else None
            self.requires_profile_uid = uid_digest is not None
            self.protocol_module = cc
            self.journal = journal
            self.file_infos = {}
            self.observed = {}
            self.layout = None
            self.open_file_name = None
            self.open_access = None
            self.offset = 0
            self.phase = "idle"
            self.pending = None
            self.failed = False
            self.armed = None
            self.key_matches = False
            self.key_created = False
            self.key_rewrite_closed = False
            self.key_existing_write_verified = False
            self.application_write_attempted = False
            self.application_verified = False
            self.web_verified = set()
            self.web_write_attempted = set()
            self.web_new_blocks_claimed = 0
            self.web_new_files_claimed = 0
            self.web_space_checked = False
            self.mutation_attempts = []
            self._read_digest = None
            self._key_read = bytearray()

        @property
        def payloads(self):
            return immutable

        @property
        def web_assets(self):
            return web_assets

        def _record(self, event, **fields):
            try:
                self.journal.record(event, **fields)
            except BaseException as exc:
                self.failed = True
                if isinstance(exc, KeyboardInterrupt):
                    raise
                raise InstallError("The durable journal failed; the session is stopped") from None

        def _read_ack(self, timeout=None):
            try:
                seconds = self.port.timeout if timeout is None else timeout
                deadline = time.monotonic() + seconds
                previous = None
                received = 0
                while time.monotonic() < deadline:
                    data = self.port.transport.read(1)
                    if not data:
                        continue
                    value = data[0]
                    received += 1
                    if previous == 0 and value == 0xCC:
                        return True
                    if previous == 0 and value == 0x33:
                        raise InstallError("The device rejected a command acknowledgement")
                    if received >= 1024:
                        raise InstallError("No acknowledgement in the bounded response stream")
                    previous = value
                raise InstallError("Command acknowledgement timed out")
            except BaseException as exc:
                self.failed = True
                if isinstance(exc, KeyboardInterrupt):
                    raise
                # Match the proven scanner's ACK/NACK/deadline behavior, but
                # retain only the previous byte, never a diagnostic RX buffer.
                raise InstallError(_safe_error(exc)) from None

        def _try_breaking(self, tries=5, timeout=1):
            # Delegate the entire BREAK/ACK/release sequence to the physically
            # proven backup implementation. Only failure reporting changes.
            started = time.monotonic()
            try:
                return super()._try_breaking(tries, timeout)
            except BaseException as exc:
                self.failed = True
                if isinstance(exc, KeyboardInterrupt):
                    raise
                raise _safe_exception(exc, time.monotonic() - started) from None

        def _live_state_ok(self):
            if self.flash_size != BACKUP_SIZE or not self.layout:
                raise InstallError("Live geometry and allocation checks have not passed")
            if (not self.layout["application_allocation_verified"]
                    or not self.layout["image3_absent_in_all_fats"]):
                raise InstallError("The live application layout differs from the approved layout")
            if self.requires_profile_uid and not self.layout.get("profile_uid_matches"):
                raise InstallError("Live identity has not matched the profiled brewer")
            for name in PROTECTED_NAMES:
                if name == RECOVERY_NAME and self.operation == "restore-stock" and model not in C_STOCK:
                    # Slot 1 can be repaired after an interrupted write if the
                    # confirmed application slot already holds the OEM image.
                    if self.observed.get(APP_NAME) == _expected_public(APP_NAME, model, stock_profile):
                        continue
                if not _protected_matches(name, self.observed.get(name), model, stock_profile):
                    raise InstallError("Protected firmware or logical boot information has not verified")
            image3 = self.file_infos.get(preflight.OPTIONAL_IMAGE_NAME)
            if image3 is None or image3["exists"]:
                raise InstallError("The additional image slot is not confirmed absent")
            if self.operation not in ("restore", "restore-stock"):
                app = self.observed.get(APP_NAME)
                if app not in _approved_current_applications(self.operation, self.target_model):
                    raise InstallError("Current application is not an approved stock/standalone image; use restore")

        def verify_profile_uid(self, record):
            if not hmac.compare_digest(hashlib.sha256(_read_uid_record(record)).digest(), uid_digest):
                raise InstallError("Connected brewer identity differs from the profiled brewer")

        def arm_write(self, name, flags):
            if self.failed or self.phase != "idle" or self.pending is not None or self.armed is not None:
                raise InstallError("Cannot authorize a write in the current protocol state")
            self._live_state_ok()
            info = self.file_infos.get(name)
            if name == KEY_NAME and self.operation in ("provision-key", "install"):
                if flags == OPEN_KEY_CREATE:
                    if (self.operation != "provision-key" or info is None or info["exists"]
                            or not self.layout["key_absent_in_all_fats"]
                            or self.layout["free_blocks_min"] < 1 or self.layout["free_file_slots_min"] < 1
                            or self.key_created or self.key_rewrite_closed):
                        raise InstallError("Key creation is not justified by the live logical/FAT absence checks")
                    label = "key-create"
                elif flags == OPEN_EXISTING_WRITE:
                    if (info is None or not info["exists"] or not self.key_matches
                            or not self.layout["key_existing_write_compatible"] or self.key_rewrite_closed):
                        raise InstallError("Existing key must exactly match the host key before its rewrite")
                    label = "key-existing-write-proof"
                else:
                    raise InstallError("Unapproved key open mode")
            elif name == RECOVERY_NAME and self.operation == "restore-stock":
                if (flags != OPEN_EXISTING_WRITE or info is None
                        or self.observed.get(APP_NAME) != _expected_public(APP_NAME, model, stock_profile)
                        or self.observed.get(name) == _expected_public(name, model, stock_profile)
                        or not self.layout.get("recovery_allocation_verified")
                        or any(item["target"] == name for item in self.mutation_attempts)):
                    raise InstallError("Stock recovery requires a verified OEM application and recovery allocation")
                label = "recovery-restore"
            elif name == RECOVERY_NAME and self.operation == "install-recovery":
                if (flags != OPEN_EXISTING_WRITE or not self.key_matches or not info or not info["exists"]
                        or self.mutation_attempts or not self.layout.get("recovery_allocation_verified")
                        or self.observed.get(RECOVERY_NAME) != _expected_public(RECOVERY_NAME, model, stock_profile)):
                    raise InstallError("Recovery installation requires the original recovery allocation and verified identity")
                label = "recovery-existing-write"
            elif name == APP_NAME and self.operation in ("install", "restore", "restore-stock"):
                if flags != OPEN_EXISTING_WRITE or self.application_write_attempted:
                    raise InstallError("Only one existing-application write is permitted per session")
                if self.operation == "restore-stock" and (
                        self.observed.get(name) == _expected_public(name, model, stock_profile)
                        or not _protected_matches(RECOVERY_NAME, self.observed.get(RECOVERY_NAME), model, stock_profile)):
                    raise InstallError("Stock application restore requires an intact recovery slot")
                if self.operation == "install" and not self.key_existing_write_verified:
                    raise InstallError("The matching key's existing-file rewrite must verify before installation")
                if self.operation == "install" and self.web_verified != set(self.web_assets):
                    raise InstallError("Every pinned browser file must verify before the application write")
                # Restore need not trust logical existence/length of a damaged
                # file. The raw named allocation is checked independently.
                if info is None or (not info["exists"] and self.operation == "install"):
                    raise InstallError("Application presence has not been checked")
                label = "application-install" if self.operation == "install" else "application-restore"
            elif name in self.web_assets and self.operation == "install":
                if not self.key_existing_write_verified or not self.key_matches:
                    raise InstallError("The matching key's existing-file rewrite must verify before browser files")
                if not self.web_space_checked:
                    raise InstallError("The complete browser bundle's free-space plan has not verified")
                if self.application_write_attempted or name in self.web_write_attempted:
                    raise InstallError("Only one browser write per file is allowed, before the application write")
                allocation = self.layout.get("web_assets", {}).get(name)
                entry = self.web_assets[name]
                if info is None or allocation is None:
                    raise InstallError("Browser file presence and allocation have not been checked")
                if flags == entry["create_mode"]:
                    if info["exists"] or not allocation["absent_in_all_fats"]:
                        raise InstallError("Browser creation requires confirmed logical and FAT absence")
                    if (self.web_new_blocks_claimed + entry["allocation_blocks"] > self.layout["free_blocks_min"]
                            or self.web_new_files_claimed + 1 > self.layout["free_file_slots_min"]):
                        raise InstallError("The verified free space cannot hold the browser bundle")
                    label = "web-create"
                elif flags == OPEN_EXISTING_WRITE:
                    if not info["exists"] or not allocation["existing_write_compatible"]:
                        raise InstallError("Existing browser allocation cannot hold the pinned file; no delete/create fallback")
                    label = "web-existing-write"
                else:
                    raise InstallError("Unapproved browser file open mode")
            else:
                raise InstallError("Blocked a write outside the operation's destination allowlist")
            if name not in self.payloads:
                raise InstallError("No immutable payload is registered for this destination")
            self.armed = (name, flags, label)

        def _send_packet(self, data, timeout=None):
            if self.failed:
                raise InstallError("The session failed; reconnect instead of retrying commands")
            if not data:
                raise InstallError("Blocked an empty command")
            opcode = data[0]
            if opcode not in (0x21, 0x22, 0x23, 0x24, 0x2A, 0x2B):
                if self.phase != "idle" or self.pending is not None or self.armed is not None:
                    raise InstallError("Blocked a command during a filesystem transaction")
                try:
                    return super()._send_packet(data, timeout)
                except BaseException:
                    self.failed = True
                    raise
            if not self.helper_executed or self.flash_size != BACKUP_SIZE or self.pending is not None:
                raise InstallError("Filesystem command is not valid in the current state")
            names = {name.encode("ascii"): name for name in READ_NAMES}
            pending = None
            next_phase = self.phase
            opened = None
            access = None
            write_event = None
            if opcode == 0x2A:
                if self.phase != "idle" or self.armed is not None or len(data) < 5:
                    raise InstallError("Invalid file-info state")
                length = struct.unpack(">I", data[1:5])[0]
                if length != len(data[5:]) or data[5:] not in names:
                    raise InstallError("Blocked an invalid or unapproved file-info name")
                pending = ("info", names[data[5:]])
            elif opcode == 0x21:
                if self.phase != "idle" or len(data) < 12 or not data.endswith(b"\0\0"):
                    raise InstallError("Invalid file-open state or format")
                flags, token = struct.unpack(">II", data[1:9])
                if token != 0 or data[9:-2] not in names:
                    raise InstallError("Blocked a file-open token or filename")
                opened = names[data[9:-2]]
                info = self.file_infos.get(opened)
                if flags == OPEN_READ:
                    if self.armed is not None or not info or not info["exists"]:
                        raise InstallError("File must be confirmed present before reading")
                    bound = (KEY_SIZE if opened == KEY_NAME else
                             self.web_assets[opened]["max_size"] if opened in self.web_assets else BACKUP_SIZE)
                    if not 0 <= info["size_bytes"] <= bound:
                        raise InstallError("Reported file length exceeds its read bound")
                    access = "read"
                else:
                    if self.armed is None or self.armed[:2] != (opened, flags):
                        raise InstallError("Unapproved write/create open was blocked")
                    self._live_state_ok()
                    access = "write"
                    write_event = {"target": opened, "mode": f"0x{flags:04x}",
                                   "size_bytes": len(self.payloads[opened]), "stage": self.armed[2]}
                    # This must be durable BEFORE the command that erases or
                    # creates file content is sent. A journal error blocks it.
                    self._record("before-write-open", **write_event)
                    self.mutation_attempts.append(dict(write_event))
                    if opened == APP_NAME:
                        self.application_write_attempted = True
                    elif opened == KEY_NAME:
                        self.key_matches = False
                    elif opened in self.web_assets:
                        self.web_write_attempted.add(opened)
                        self.web_verified.discard(opened)
                        if flags != OPEN_EXISTING_WRITE:
                            self.web_new_blocks_claimed += self.web_assets[opened]["allocation_blocks"]
                            self.web_new_files_claimed += 1
                    self.observed.pop(opened, None)
                next_phase = "open_token"
            elif opcode == 0x2B:
                if self.phase != "reading" or self.open_access != "read" or len(data) != 9:
                    raise InstallError("Invalid file-read state")
                offset, count = struct.unpack(">II", data[1:])
                size = self.file_infos[self.open_file_name]["size_bytes"]
                if offset != self.offset or not 1 <= count <= READ_CHUNK_SIZE or offset + count > size:
                    raise InstallError("Blocked an out-of-order or out-of-bounds file read")
                pending = ("read", count)
            elif opcode == 0x24:
                if self.phase != "writing" or self.open_access != "write" or len(data) < 6:
                    raise InstallError("File chunk is not authorized in the current state")
                offset = struct.unpack(">I", data[1:5])[0]
                chunk = data[5:]
                approved = self.payloads[self.open_file_name]
                if (offset != self.offset or not 1 <= len(chunk) <= WRITE_CHUNK_SIZE
                        or offset + len(chunk) > len(approved)
                        or not hmac.compare_digest(chunk, approved[offset:offset + len(chunk)])):
                    raise InstallError("Write bytes or offset differ from the immutable approved payload")
                pending = None
                next_phase = "chunk_status"
                self._pending_write_count = len(chunk)
            elif opcode == 0x22:
                if data != FILE_CLOSE_COMMAND or self.phase not in ("reading", "writing"):
                    raise InstallError("Invalid file-close command/state")
                size = (len(self.payloads[self.open_file_name]) if self.open_access == "write"
                        else self.file_infos[self.open_file_name]["size_bytes"])
                if self.offset != size:
                    raise InstallError("Closing an incomplete file transaction is blocked")
                next_phase = "close_status"
            elif opcode == 0x23:
                if data != b"\x23" or self.phase not in ("open_status", "chunk_status", "close_status"):
                    raise InstallError("Status query is not valid in the current state")
                pending = ("status", self.phase)
            try:
                cc.CC3200Connection._send_packet(self, data, timeout)
            except BaseException as exc:
                self.failed = True
                if isinstance(exc, KeyboardInterrupt):
                    raise
                raise InstallError(_safe_error(exc)) from None
            self.pending = pending
            self.phase = next_phase
            if opened is not None:
                self.open_file_name = opened
                self.open_access = access
                self.offset = 0
                self._read_digest = hashlib.sha256() if opened != KEY_NAME else None
                self._key_read.clear()
                self._web_read_matches = True
                if access == "write":
                    self._write_flags = self.armed[1]
                    self.armed = None
                    self._record("write-open-acknowledged", target=opened)

        def _read_packet(self, timeout=None):
            try:
                payload = super()._read_packet(timeout)
                if self.pending is None:
                    return payload
                kind, value = self.pending
                if kind == "info":
                    self.file_infos[value] = preflight.parse_file_info(payload)
                elif kind == "read":
                    if len(payload) != value:
                        raise InstallError("File response length differs from the requested length")
                    if self.open_file_name in self.web_assets:
                        approved = self.payloads[self.open_file_name]
                        self._web_read_matches = (self._web_read_matches and
                            hmac.compare_digest(payload, approved[self.offset:self.offset + value]))
                    self.offset += value
                    if self.open_file_name == KEY_NAME:
                        self._key_read.extend(payload)
                    else:
                        self._read_digest.update(payload)
                elif kind == "status":
                    if payload != b"\x40":
                        if (value == "open_status" and self.open_file_name == APP_NAME
                                and self.operation == "restore" and self.open_access == "write"):
                            raise InstallError(
                                "The filesystem could not reopen the allocated image for restore; "
                                "this state is unsupported, and no create/delete fallback was attempted")
                        raise InstallError("Filesystem operation did not return successful status 0x40")
                    if value == "open_status":
                        self.phase = "writing" if self.open_access == "write" else "reading"
                        if self.open_access == "write":
                            self._record("write-open-status-ok", target=self.open_file_name)
                    elif value == "chunk_status":
                        self.offset += self._pending_write_count
                        self.phase = "writing"
                        self._record("write-chunk-status-ok", target=self.open_file_name,
                                     completed_bytes=self.offset)
                    else:
                        name = self.open_file_name
                        if self.open_access == "write":
                            self._record("write-close-status-ok", target=name, size_bytes=self.offset)
                            self.file_infos.pop(name, None)
                            if name == KEY_NAME:
                                if self._write_flags == OPEN_KEY_CREATE:
                                    self.key_created = True
                                else:
                                    self.key_rewrite_closed = True
                        elif name == KEY_NAME:
                            self.key_matches = (name in self.payloads and hmac.compare_digest(
                                bytes(self._key_read), self.payloads[name]))
                            self._key_read.clear()
                            if self.key_rewrite_closed and self.key_matches:
                                self.key_existing_write_verified = True
                        else:
                            self.observed[name] = {"size_bytes": self.offset,
                                                   "sha256": self._read_digest.hexdigest()}
                            if name in self.web_assets:
                                self.web_verified.discard(name)
                                if self._web_read_matches and self.offset == len(self.payloads[name]):
                                    self.web_verified.add(name)
                        self.open_file_name = None
                        self.open_access = None
                        self.offset = 0
                        self.phase = "idle"
                self.pending = None
                return payload
            except BaseException as exc:
                self.failed = True
                if isinstance(exc, KeyboardInterrupt):
                    raise
                raise InstallError(_safe_error(exc)) from None

        def _open_file(self, filename, slfs_flags):
            try:
                super()._open_file(filename, slfs_flags)
                if self.phase != "open_token":
                    raise InstallError("Unexpected state after consuming the file-open token")
                self.phase = "open_status"
                if not self._get_last_status().is_ok:
                    raise InstallError("File-open status failed")
            except BaseException as exc:
                self.failed = True
                if isinstance(exc, KeyboardInterrupt):
                    raise
                raise InstallError(_safe_error(exc)) from None

    return FileConnection()


def read_file_info(connection, name):
    if name not in READ_NAMES:
        raise InstallError("Blocked a file-info name outside the allowlist")
    encoded = name.encode("ascii")
    connection._send_packet(b"\x2a" + struct.pack(">I", len(encoded)) + encoded)
    parsed = preflight.parse_file_info(connection._read_packet())
    if connection.file_infos.get(name) != parsed:
        raise InstallError("File-info guard and caller disagree")
    return dict(parsed)


def read_file_bytes(connection, name, expected_max=BACKUP_SIZE):
    info = read_file_info(connection, name)
    if not info["exists"]:
        raise InstallError("A required logical file is absent or unreadable")
    bound = min(expected_max, KEY_SIZE if name == KEY_NAME else BACKUP_SIZE)
    if not 0 <= info["size_bytes"] <= bound:
        raise InstallError("A logical file exceeds the permitted read length")
    connection._open_file_for_read(name)
    result = bytearray()
    while len(result) < info["size_bytes"]:
        count = min(READ_CHUNK_SIZE, info["size_bytes"] - len(result))
        connection._send_packet(b"\x2b" + struct.pack(">II", len(result), count))
        payload = connection._read_packet()
        if len(payload) != count:
            raise InstallError("Short logical-file read")
        result.extend(payload)
    connection._close_file()
    return bytes(result)


@contextmanager
def _quiet_protocol_logs(cc):
    # The upstream FAT parser logs arbitrary filenames on malformed overlaps.
    # Those bytes are untrusted metadata and must not reach credential logs.
    previous = cc.log.disabled
    cc.log.disabled = True
    try:
        yield
    finally:
        cc.log.disabled = previous


def _filesystem_views(data, cc):
    """Decode all recognizable FATs, rejecting ambiguous names and overlapping files."""
    storage = cc.CC3x00StorageInfo(4096, 256)
    views = []
    try:
        for index in range(2):
            start = index * 4096
            fat_bytes = bytes(data[start:start + 4096])
            revision, signature = struct.unpack("<HH", fat_bytes[:4])
            if revision == 0xFFFF and signature == 0xFFFF:
                for slot in range(128):
                    allocation = fat_bytes[4 + slot * 4:8 + slot * 4]
                    if allocation not in (b"\xff" * 4, bytes((0xff, slot, 0xff, 0x7f))):
                        raise InstallError("An erased FAT header still has occupied allocation entries")
                continue
            if revision == 0xFFFF or signature != 0x534C:
                raise InstallError("A FAT copy is neither validated nor unequivocally erased")
            metadata_start = 0x774 + start
            metadata = bytes(data[metadata_start:metadata_start + 4096])
            inspection.validate_filename_metadata(fat_bytes, metadata)
            with _quiet_protocol_logs(cc):
                header = cc.CC3x00SffsHeader(index, fat_bytes, storage)
                if not header.is_valid:
                    raise InstallError("A recognized FAT copy failed validation")
                fat = cc.CC3x00SffsInfo(header, storage, metadata, "cc3200")
            names = {}
            occupied = set(range(5))
            for entry in fat.files:
                if (not entry.fname or any(ord(c) < 0x20 or ord(c) >= 0x7F for c in entry.fname)
                        or entry.size_blocks <= 0 or entry.start_block < 5
                        or entry.start_block + entry.total_blocks > 256):
                    raise InstallError("A live FAT allocation/name is malformed")
                name = "/" + entry.fname.lstrip("/").lower()
                blocks = set(range(entry.start_block, entry.start_block + entry.total_blocks))
                if name in names or occupied.intersection(blocks):
                    raise InstallError("Live FAT names or allocations overlap")
                names[name] = entry
                occupied.update(blocks)
            views.append((index, revision, names, occupied))
    except InstallError:
        raise
    except Exception:
        raise InstallError("FAT/name metadata could not be validated") from None
    if not views:
        raise InstallError("No validated FAT view is available")
    return views


def _slot_starts(names):
    starts = {}
    for name, shape in SLOT_GEOMETRY.items():
        entry = names.get(name)
        if entry is None or (entry.size_blocks, bool(entry.mirrored), entry.flags) != shape:
            raise InstallError("A required firmware or identity allocation has an unsupported layout")
        starts[name] = entry.start_block
    if preflight.OPTIONAL_IMAGE_NAME in names:
        raise InstallError("An additional firmware slot is present")
    return starts


def _verify_boot_mirrors(read, starts, allow_previous_trial):
    """Both copies must be erased or recognized; the active logical selector is checked separately."""
    previous = {b"\0" + BOOT_INFO[1:]}
    if allow_previous_trial:
        # Recovery arms slot 2 using selector zero; the loader and new app
        # subsequently write TESTING and then the normal slot-2 record.
        for magic in (0x56788765, 0x12344321):
            previous.add(b"\0" + BOOT_INFO[1:4] + struct.pack("<I", magic))
    for name in (LOADER_NAME, preflight.BOOT_INFO_NAME):
        capacity = SLOT_GEOMETRY[name][0] * 4096
        expected = _expected_public(name)
        found = False
        for mirror in range(2):
            raw = read(starts[name] * 4096 + mirror * capacity, capacity)
            if len(raw) != capacity:
                raise InstallError("Could not read the complete bootloader or boot record allocation")
            if raw == b"\xff" * capacity:
                continue
            size = int.from_bytes(raw[:3], "little")
            if raw[6:8] != b"LS" or size != expected["size_bytes"] or size > capacity - 8:
                raise InstallError("A bootloader or boot record copy is incomplete")
            payload = bytes(raw[8:8 + size])
            if _image_metadata(payload) == expected:
                found = True
            elif name != preflight.BOOT_INFO_NAME or payload not in previous:
                raise InstallError("A bootloader or boot record copy has unrecognized contents")
        if not found:
            raise InstallError("The supported bootloader or normal boot selection is missing")


def _read_flash(connection, offset, length):
    data = bytearray()
    while len(data) < length:
        count = min(READ_CHUNK_SIZE, length - len(data))
        chunk = connection._read_chunk(offset + len(data), count, 2)
        if len(chunk) != count:
            raise InstallError("Short physical flash read")
        data.extend(chunk)
    return data


def _compatible_mutable_file(tables, name, blocks):
    entries = [names[name] for _, _, names, _ in tables if name in names]
    if not entries:
        return False
    first = entries[0]
    if first.size_blocks < blocks or (name == KEY_NAME and first.size_blocks != 1):
        return False
    extent = range(first.start_block, first.start_block + first.size_blocks)
    for _, _, names, occupied in tables:
        entry = names.get(name)
        if entry is None:
            if not occupied.isdisjoint(extent):
                return False
        elif (entry.start_block != first.start_block or entry.size_blocks != first.size_blocks
              or entry.mirrored or entry.flags != 0xC):
            return False
    return True


@dataclass(frozen=True, repr=False)
class StockProfile:
    model: str
    starts: MappingProxyType
    uid_digest: bytes
    application: bytes
    recovery: bytes
    recovery_identity: MappingProxyType
    backup_sha256: str


def inspect_stock(path, model, cc):
    """Inspect a complete original capture without opening a USB connection."""
    if model not in ("pico-s", "pico-pro") and model not in C_STOCK:
        raise InstallError("Unknown stock backup target")
    image = _read_regular(path, BACKUP_SIZE)
    if len(image) != BACKUP_SIZE:
        raise InstallError("The stock backup must contain exactly 1 MiB")
    starts = None
    for _, _, names, _ in _filesystem_views(image, cc):
        observed = _slot_starts(names)
        if starts is not None and starts != observed:
            raise InstallError("The backup FAT copies disagree on the required slots")
        starts = observed
    _verify_boot_mirrors(lambda offset, length: image[offset:offset + length], starts, allow_previous_trial=False)
    _, application = inspection.read_candidate(image, names[APP_NAME], "")
    if application is None or _image_metadata(application) != _expected_public(APP_NAME, model):
        raise InstallError("The stock application does not match the selected brewer and display")
    _, recovery = inspection.read_candidate(image, names[RECOVERY_NAME], "")
    if recovery is None:
        raise InstallError("The backup lacks an intact original recovery slot")
    recovery_identity = _image_metadata(recovery)
    if model in C_STOCK:
        # This slot stays byte-for-byte as captured throughout conversion and
        # restore. Reject corrupt headers, non-applications and our OTA image.
        if len(recovery) < 8 or recovery_identity in [RECOVERY_IMAGE, *C_RECOVERY.values(), *LEGACY_RECOVERY.values()]:
            raise InstallError("Pico C requires its original independent recovery slot")
        stack, reset = struct.unpack_from("<II", recovery)
        if (stack & 3 or not 0x20000000 <= stack <= 0x20040000 or not reset & 1
                or not 0x20004000 <= reset & ~1 < 0x20004000 + len(recovery)):
            raise InstallError("Pico C recovery has unsupported application vectors")
    elif recovery_identity != _expected_public(RECOVERY_NAME):
        raise InstallError("The backup does not contain the verified OEM recovery")
    uid_offset = starts[UID_NAME] * 4096
    uid = _read_uid_record(image[uid_offset:uid_offset + 41])
    return StockProfile(model, MappingProxyType(starts), hashlib.sha256(uid).digest(), application,
                        recovery, MappingProxyType(recovery_identity), hashlib.sha256(image).hexdigest())


def read_live_layout(connection, cc):
    """Prove relevant names/allocations without selecting an active FAT copy."""
    tables = _filesystem_views(_read_flash(connection, 0, LAYOUT_BYTES), cc)
    views = []
    key_absent = True
    image3_absent = True
    web_layout = {name: {"absent_in_all_fats": True, "allocations": []}
                  for name in connection.web_assets}
    free_blocks_min = 256
    free_file_slots_min = 128
    try:
        for index, revision, names, occupied in tables:
            if connection.stock_profile is not None:
                if _slot_starts(names) != connection.stock_profile.starts:
                    raise InstallError("Live firmware or identity allocations differ from this brewer's stock backup")
            else:
                if connection.requires_profile_uid:
                    for name, start, blocks, mirrored, flags in (
                            ("/sys/uid.txt", UID_BLOCK, 1, False, 0xC),
                            ("/sys/mcuimg.bin", 71, 4, True, 0),
                            (preflight.BOOT_INFO_NAME, 79, 1, True, 8)):
                        entry = names.get(name)
                        if entry is None or (entry.start_block, entry.size_blocks, bool(entry.mirrored), entry.flags) != (
                                start, blocks, mirrored, flags):
                            raise InstallError("Firmware or identity allocation differs from the original backup")
                if connection.operation in ("install-recovery", "restore-stock"):
                    recovery = names.get(RECOVERY_NAME)
                    if (recovery is None or recovery.start_block != 214 or recovery.size_blocks != 16
                            or recovery.mirrored or recovery.flags != 0xC):
                        raise InstallError("Recovery allocation differs from the profiled 16-block layout")
                app = names.get(APP_NAME)
                if (app is None or app.start_block != 130 or app.size_blocks != APPLICATION_ALLOCATION_BLOCKS
                        or app.mirrored or app.flags != 0xC):
                    raise InstallError("The existing image2 allocation differs from the approved 48-block allocation")
            key_absent = key_absent and KEY_NAME not in names
            image3_absent = image3_absent and preflight.OPTIONAL_IMAGE_NAME not in names
            free_blocks_min = min(free_blocks_min, 256 - len(occupied))
            free_file_slots_min = min(free_file_slots_min, 128 - len(names))
            for name, allocation in web_layout.items():
                entry = names.get(name)
                if entry is not None:
                    allocation["absent_in_all_fats"] = False
                    allocation["allocations"].append({"fat_index": index,
                        "start_block": entry.start_block, "size_blocks": entry.size_blocks,
                        "mirrored": bool(entry.mirrored), "flags": entry.flags})
            extents = []
            extent_start = None
            for block in range(257):
                if block < 256 and block not in occupied:
                    if extent_start is None:
                        extent_start = block
                elif extent_start is not None:
                    extents.append({"start_block": extent_start, "blocks": block - extent_start})
                    extent_start = None
            known_local = []
            for name, entry in names.items():
                if (name in WEB_NAMES or name in (KEY_NAME, "/restart.txt") or
                        re.fullmatch(r"/local/(?:recipe[0-9]{2}\.[ab]|(?:index|settings|sessions)\.[ab]|"
                                     r"history[0-9]{2}|history\.(?:meta|tail)\.[ab])", name)):
                    known_local.append({"file_path": name, "start_block": entry.start_block,
                        "size_blocks": entry.size_blocks, "total_blocks": entry.total_blocks,
                        "mirrored": bool(entry.mirrored)})
            views.append({"index": index, "revision": revision,
                "allocated_blocks": len(occupied), "free_blocks": 256 - len(occupied),
                "file_count": len(names), "free_extents": extents,
                "known_local_allocations": sorted(known_local, key=lambda item: item["file_path"])})
    except InstallError:
        raise
    except Exception:
        raise InstallError("Live FAT/name metadata could not be validated") from None
    for name, allocation in web_layout.items():
        allocation["existing_write_compatible"] = _compatible_mutable_file(
            tables, name, connection.web_assets[name]["allocation_blocks"])
    result = {"fat_views": views, "application_allocation_verified": True,
              "application_payload_limit_bytes": APPLICATION_PAYLOAD_LIMIT,
              "key_absent_in_all_fats": key_absent,
              "key_existing_write_compatible": _compatible_mutable_file(tables, KEY_NAME, 1),
              "image3_absent_in_all_fats": image3_absent,
              "free_blocks_min": free_blocks_min, "free_file_slots_min": free_file_slots_min,
              "web_assets": web_layout}
    result["recovery_allocation_verified"] = (connection.stock_profile is not None or
                                               connection.operation in ("install-recovery", "restore-stock"))
    if connection.requires_profile_uid:
        block = connection.stock_profile.starts[UID_NAME] if connection.stock_profile is not None else UID_BLOCK
        connection.verify_profile_uid(connection._read_chunk(block * 4096, 41, 2))
        starts = (connection.stock_profile.starts if connection.stock_profile is not None
                  else {LOADER_NAME: 71, preflight.BOOT_INFO_NAME: 79})
        _verify_boot_mirrors(lambda offset, length: _read_flash(connection, offset, length),
                             starts, allow_previous_trial=True)
        result["profile_uid_matches"] = True
    connection.layout = result
    return result


def _check_protected(connection, progress):
    result = {}
    for name in PROTECTED_NAMES:
        data = read_file_bytes(connection, name)
        observed = _image_metadata(data)
        if not _protected_matches(name, observed, connection.target_model, connection.stock_profile):
            raise InstallError("Protected firmware or logical boot information differs from the verified device")
        result[name] = observed
        progress(f"VERIFIED: {name}: {observed['size_bytes']:,} bytes; SHA256 {observed['sha256']}")
    return result


def _verify_key(connection, key_bytes):
    data = read_file_bytes(connection, KEY_NAME, expected_max=KEY_SIZE)
    if not hmac.compare_digest(data, key_bytes) or not connection.key_matches:
        raise InstallError("The device key differs from the supplied host key; no replacement is authorized")


def _write_registered(connection, name, flags):
    connection.arm_write(name, flags)
    connection._open_file(name, flags)
    payload = connection.payloads[name]
    offset = 0
    while offset < len(payload):
        chunk = payload[offset:offset + WRITE_CHUNK_SIZE]
        connection._send_packet(b"\x24" + struct.pack(">I", offset) + chunk)
        if not connection._get_last_status().is_ok:
            raise InstallError("A file-write chunk failed")
        offset += len(chunk)
    connection._close_file()


def _plan_web_bundle(connection, details):
    """Prove destinations and whole-file holes before any install mutation."""
    plans = []
    new_blocks = new_files = 0
    for name, entry in connection.web_assets.items():
        info = read_file_info(connection, name)
        allocation = connection.layout["web_assets"][name]
        if info["exists"]:
            if not allocation["existing_write_compatible"]:
                raise InstallError("An existing browser allocation is incompatible; no erase/delete fallback is allowed")
            flags = OPEN_EXISTING_WRITE
        else:
            if not allocation["absent_in_all_fats"]:
                raise InstallError("An unreadable browser file still has a FAT allocation; creation is blocked")
            flags = entry["create_mode"]
            new_blocks += entry["allocation_blocks"]
            new_files += 1
        plans.append((name, flags, info))
    if (new_blocks > connection.layout["free_blocks_min"]
            or new_files > connection.layout["free_file_slots_min"]):
        raise InstallError("The verified filesystem lacks room for the complete browser bundle")
    for view in connection.layout["fat_views"]:
        holes = [extent["blocks"] for extent in view["free_extents"]]
        for name, flags, _ in plans:
            if flags == OPEN_EXISTING_WRITE:
                continue
            needed = connection.web_assets[name]["allocation_blocks"]
            for index, available in enumerate(holes):
                if available >= needed:
                    holes[index] -= needed
                    break
            else:
                raise InstallError("Free flash is fragmented; the complete browser bundle does not fit the verified holes")
    details["web_bundle"] = {"manifest_sha256": connection.web_manifest_sha256,
        "files": [], "new_allocation_blocks": new_blocks,
        "maximum_allocation_blocks": sum(entry["allocation_blocks"] for entry in connection.web_assets.values()),
        "verified": False}
    connection.web_space_checked = True
    connection._record("web-plan-verified", new_allocation_blocks=new_blocks,
                       new_files=new_files, manifest_sha256=connection.web_manifest_sha256)
    return tuple(plans)


def _install_web_bundle(connection, details, progress, plans):
    """Install the prepared destinations; image2 stays intact until all verify."""
    for name, flags, info in plans:
        entry = connection.web_assets[name]
        already_matches = False
        if info["exists"] and info["size_bytes"] == len(connection.payloads[name]):
            current = read_file_bytes(connection, name, expected_max=entry["max_size"])
            already_matches = hmac.compare_digest(current, connection.payloads[name])
        if not already_matches:
            progress("BROWSER: " + ("creating " if flags != OPEN_EXISTING_WRITE else "rewriting ") + name)
            _write_registered(connection, name, flags)
            current = read_file_bytes(connection, name, expected_max=entry["max_size"])
            if not hmac.compare_digest(current, connection.payloads[name]):
                raise InstallError("Browser readback differs from the complete pinned file")
        if name not in connection.web_verified:
            raise InstallError("Browser file byte-for-byte readback did not verify")
        metadata = {"file_path": name, "size_bytes": entry["length"], "sha256": entry["sha256"],
                    "already_matches": already_matches, "allocation_blocks": entry["allocation_blocks"]}
        details["web_bundle"]["files"].append(metadata)
        connection._record("web-readback-verified", **metadata)
    if connection.web_verified != set(connection.web_assets):
        raise InstallError("The complete browser bundle has not verified")
    details["web_bundle"]["verified"] = True
    connection._record("web-bundle-verified", manifest_sha256=connection.web_manifest_sha256)


def _restore_stock(connection, progress, details):
    model, profile = connection.target_model, connection.stock_profile
    before = details["live_preflight"]
    if (not before["layout"]["image3_absent_in_all_fats"]
            or read_file_info(connection, preflight.OPTIONAL_IMAGE_NAME)["exists"]):
        raise InstallError("Stock restore requires the original empty third slot")
    for name in ("/sys/mcuimg.bin", preflight.BOOT_INFO_NAME):
        expected = _expected_public(name, model, profile)
        if _image_metadata(read_file_bytes(connection, name, expected["size_bytes"])) != expected:
            raise InstallError("Bootloader or boot selection differs from the original backup")
    before["slots"] = {}
    for name, limit in ((APP_NAME, APPLICATION_PAYLOAD_LIMIT), (RECOVERY_NAME, 16 * 4096)):
        info = read_file_info(connection, name)
        before["slots"][name] = (_image_metadata(read_file_bytes(connection, name, limit))
                                 if info["exists"] else {"exists": False})
    connection._live_state_ok()
    connection._record("live-preflight-passed", mode="restore-stock")

    # The original boot record selects slot 2. Verify it before overwriting
    # the independent recovery, even when resuming a partially written slot.
    for name in (APP_NAME, RECOVERY_NAME):
        expected = _expected_public(name, model, profile)
        if before["slots"][name] != expected:
            progress(f"Restoring {name} from the verified OEM image")
            _write_registered(connection, name, OPEN_EXISTING_WRITE)
        if _image_metadata(read_file_bytes(connection, name, expected["size_bytes"])) != expected:
            raise InstallError("OEM firmware slot failed full readback")
        if name == APP_NAME:
            connection.application_verified = True
        connection._record("stock-slot-readback-verified", target=name)

    after = details["postcheck"] = {}
    after["protected"] = _check_protected(connection, progress)
    after["application"] = _image_metadata(read_file_bytes(connection, APP_NAME, APPLICATION_PAYLOAD_LIMIT))
    after["layout"] = read_live_layout(connection, connection.protocol_module)
    if (after["protected"][RECOVERY_NAME] != _expected_public(RECOVERY_NAME, model, profile)
            or after["application"] != _expected_public(APP_NAME, model, profile)
            or not after["layout"]["image3_absent_in_all_fats"]
            or read_file_info(connection, preflight.OPTIONAL_IMAGE_NAME)["exists"]):
        raise InstallError("OEM firmware slots failed the final check")
    details["operation_verified"] = True
    connection._record("operation-verified", mode="restore-stock")
    return details


def run_operation(connection, mode, key_bytes, candidate_bytes, original_bytes, journal,
                  progress=report, details=None):
    if details is None:
        details = {}
    if mode != connection.operation:
        raise InstallError("Operation and guarded connection disagree")
    if mode not in ("restore", "restore-stock") and key_bytes != connection.payloads.get(KEY_NAME):
        raise InstallError("The registered key payload changed")
    expected_app = original_bytes if mode in ("restore", "restore-stock") else candidate_bytes
    if mode in ("install", "restore", "restore-stock") and expected_app != connection.payloads.get(APP_NAME):
        raise InstallError("The registered application payload changed")
    details["live_preflight"] = before = {}
    before["flash"] = backup.detect_flash(connection)
    if before["flash"] != preflight.EXPECTED_FLASH:
        raise InstallError("Flash geometry differs from the verified 1 MiB device")
    # The exact installed protocol module is retained by the caller, so this
    # read-only parser uses the same version as the guarded connection.
    cc = connection.protocol_module
    before["layout"] = read_live_layout(connection, cc)
    if mode == "restore-stock":
        return _restore_stock(connection, progress, details)
    before["protected"] = _check_protected(connection, progress)
    optional = read_file_info(connection, preflight.OPTIONAL_IMAGE_NAME)
    if optional["exists"] or not before["layout"]["image3_absent_in_all_fats"]:
        raise InstallError("An unexpected image3 is present")
    app_info = read_file_info(connection, APP_NAME)
    if mode == "restore":
        before["application"] = {"logical_info": app_info, "content_read_required": False}
    else:
        data = read_file_bytes(connection, APP_NAME)
        observed = _image_metadata(data)
        before["application"] = observed
        if observed not in _approved_current_applications(mode, connection.target_model):
            raise InstallError("Current application is not an approved stock/standalone image; use restore")
        progress(f"VERIFIED: {APP_NAME}: {len(data):,} bytes; SHA256 {observed['sha256']}")
    connection._live_state_ok()
    connection._record("live-preflight-passed", mode=mode)

    if mode == "install-recovery":
        _verify_key(connection, key_bytes)
        expected_recovery = C_RECOVERY.get(connection.target_model, RECOVERY_IMAGE)
        if before["protected"][RECOVERY_NAME] != expected_recovery:
            progress("RECOVERY: installing the pinned local recovery in its existing allocation")
            _write_registered(connection, RECOVERY_NAME, OPEN_EXISTING_WRITE)
        after = _check_protected(connection, progress)
        if (after[RECOVERY_NAME] != expected_recovery or
                _image_metadata(read_file_bytes(connection, APP_NAME)) != before["application"]):
            raise InstallError("Recovery or active application failed post-install readback")
        read_live_layout(connection, cc)
        _verify_key(connection, key_bytes)
        details["postcheck"] = {"protected": after, "application": before["application"]}
        details["operation_verified"] = True
        connection._record("operation-verified", mode=mode)
        return details

    if mode != "restore":
        key_info = read_file_info(connection, KEY_NAME)
        if key_info["exists"]:
            _verify_key(connection, key_bytes)
        else:
            if mode != "provision-key":
                raise InstallError("Provision and verify the host key before installing firmware")
            if not before["layout"]["key_absent_in_all_fats"]:
                raise InstallError("The key has a FAT entry but is logically unreadable; creation is blocked")
            progress("KEY: creating the confirmed-absent private key file")
            _write_registered(connection, KEY_NAME, OPEN_KEY_CREATE)
            _verify_key(connection, key_bytes)
            connection._record("key-create-readback-verified")
            # Creating the key adds a new allocation. Verify how the device
            # actually allocated it before opening the named file for rewrite.
            read_live_layout(connection, cc)
        if mode == "install":
            web_plans = _plan_web_bundle(connection, details)
        progress("KEY: proving an existing-file rewrite of the matching private key")
        _write_registered(connection, KEY_NAME, OPEN_EXISTING_WRITE)
        _verify_key(connection, key_bytes)
        if not connection.key_existing_write_verified:
            raise InstallError("The key's existing-file rewrite did not verify")
        connection._record("key-existing-write-readback-verified")
        details["key"] = {"created": connection.key_created, "matches_host": True,
                          "existing_write_verified": True, "size_bytes": KEY_SIZE}

    if mode == "install":
        _install_web_bundle(connection, details, progress, web_plans)
        already = before["application"] == _image_metadata(connection.payloads[APP_NAME])
        details["application_already_installed"] = already
        if not already:
            progress("APPLICATION: rewriting the existing image2 allocation with the pinned standalone image")
            _write_registered(connection, APP_NAME, OPEN_EXISTING_WRITE)
    elif mode == "restore":
        progress("APPLICATION: attempting an existing-allocation restore of the exact original image")
        _write_registered(connection, APP_NAME, OPEN_EXISTING_WRITE)

    details["postcheck"] = after = {}
    expected = original_bytes if mode in ("restore", "provision-key") else candidate_bytes
    observed_app = read_file_bytes(connection, APP_NAME)
    if not hmac.compare_digest(observed_app, expected):
        raise InstallError("Application readback differs from the intended complete image")
    after["application"] = _image_metadata(observed_app)
    connection.application_verified = True
    connection._record("application-readback-verified", **after["application"])
    after["protected"] = _check_protected(connection, progress)
    if mode != "restore":
        _verify_key(connection, key_bytes)
        after["key_matches_host"] = True
    after["layout"] = read_live_layout(connection, cc)
    if mode == "install" and not all(item["existing_write_compatible"]
                                    for item in after["layout"]["web_assets"].values()):
        raise InstallError("Installed browser allocations failed the final filesystem check")
    if not after["layout"]["image3_absent_in_all_fats"]:
        raise InstallError("An unexpected image3 appeared during the operation")
    if read_file_info(connection, preflight.OPTIONAL_IMAGE_NAME)["exists"]:
        raise InstallError("An unexpected logical image3 appeared during the operation")
    details["operation_verified"] = True
    connection._record("operation-verified", mode=mode)
    return details


class InstallFiles:
    """Private append-only journal plus a no-replacement final JSON report."""

    def __init__(self, output, owner):
        self.output = Path(output)
        if not self.output.is_absolute() or any(c in str(self.output) for c in "\r\n"):
            raise InstallError("--output must be an absolute path without newlines")
        if not self.output.parent.is_dir():
            raise InstallError("Create the report's output directory before starting")
        self.partial = Path(str(self.output) + ".partial")
        self.journal_path = Path(str(self.output) + ".journal.jsonl")
        for path in (self.output, self.partial, self.journal_path):
            if os.path.lexists(path):
                raise InstallError("An output/report/journal already exists; choose a new --output path")
        self.owner = owner
        self.handle = None
        self.journal_handle = None
        self.published = False

    def __enter__(self):
        self.handle = backup.new_private_file(self.partial, self.owner)
        try:
            self.journal_handle = backup.new_private_file(self.journal_path, self.owner)
        except BaseException:
            self.handle.close()
            raise
        return self

    def record(self, event, **fields):
        if self.journal_handle is None:
            raise InstallError("The durable journal is not open")
        item = {"event": event, "utc": datetime.now(timezone.utc).isoformat(), **fields}
        data = (json.dumps(item, sort_keys=True) + "\n").encode()
        try:
            if self.journal_handle.write(data) != len(data):
                raise OSError("short write")
            self.journal_handle.flush()
            os.fsync(self.journal_handle.fileno())
        except Exception:
            raise InstallError("The durable journal could not be updated; further device commands are blocked") from None

    def publish(self, details):
        if self.handle is None or self.published:
            raise InstallError("The report is unavailable for publication")
        data = (json.dumps(details, indent=2, sort_keys=True) + "\n").encode()
        if self.handle.write(data) != len(data):
            raise InstallError("Short report write")
        self.handle.flush()
        os.fsync(self.handle.fileno())
        private_files.publish(self.handle, self.partial, self.output)
        self.published = True

    def __exit__(self, exc_type, exc, traceback):
        for handle in (self.handle, self.journal_handle):
            if handle is not None:
                handle.close()


def _usb_summary(details):
    """Exclude raw failure/control buffers from credential-capable reports."""
    result = {name: details[name] for name in (
        "usb_id", "bus", "address", "chip_version", "requested_baud", "calculated_baud",
    ) if name in details}
    result["cleanup_error_count"] = len(details.get("cleanup_errors", []))
    if "receive_queue" in details:
        queue = details["receive_queue"]
        result["receive_queue"] = {
            key: value for key, value in queue.items()
            if isinstance(value, (int, float, bool)) or value is None
        }
        result["receive_queue"]["error_present"] = bool(queue.get("error"))
        result["receive_queue"]["cleanup_error_count"] = len(queue.get("cleanup_errors", []))
    return result


def _load_hub_recovery(model="pico-pro"):
    identity = C_RECOVERY.get(model, RECOVERY_IMAGE)
    image = (C_FIRMWARE / "standalone/pico-c" / model.removeprefix("pico-c-") / "pico-c-recovery.bin"
             if model in C_STOCK else RECOVERY_FILE)
    payload = _read_regular(image, identity["size_bytes"])
    if _image_metadata(payload) != identity:
        raise InstallError("Packaged recovery differs from the pinned recovery image")
    return payload


def main(argv=None, *, transport_factory=None, progress=None):
    emit = progress or report
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="mode", required=True)
    prepare = commands.add_parser("prepare-install", help="Verify the backup and local installation files offline")
    prepare.add_argument("--backup", required=True, type=Path)
    inspect = commands.add_parser("inspect-stock", help="Identify a full OEM stock backup offline")
    inspect.add_argument("--backup", required=True, type=Path)

    def add_target(command, required=False):
        command.add_argument("--model", choices=("pico-s", "pico-pro", "pico-c"), required=required)
        command.add_argument("--display", choices=("ssd1325", "ssd1309"),
                             help="Required for Pico C. Must match the original application in the backup.")

    add_target(prepare, required=True)
    add_target(inspect, required=True)
    prepare.add_argument("--stock-profile", action="store_true")
    for name in SUCCESS_MARKERS:
        command = commands.add_parser(name)
        if name == "restore-stock":
            source = command.add_mutually_exclusive_group(required=True)
            source.add_argument("--backup", type=Path, help="Exact original full-flash backup")
            source.add_argument("--oem-directory", type=Path, help="Directory containing the two pinned raw OEM images")
        else:
            command.add_argument("--backup", required=True, type=Path, help="Exact original full-flash backup")
        command.add_argument("--output", required=True, type=Path, help="New absolute private JSON report path")
        command.add_argument("--power-wait", type=float, default=60)
        if name not in ("restore", "restore-stock"):
            command.add_argument("--key-file", required=True, type=Path, help="Existing private host key file")
            command.add_argument("--verify-backup-uid", action="store_true",
                                 help="Require the live brewer identity to match the pinned original backup")
        add_target(command)
        command.add_argument("--stock-profile", action="store_true",
                             help="Use this brewer's original full USB backup, matched to the live identity and slots")
        if name == "install":
            command.add_argument("--firmware", required=True, type=Path, help="Pinned standalone binary")
            command.add_argument("--web-directory", type=Path,
                                 help="Matching pinned browser bundle (defaults to repository firmware/web)")
    args = parser.parse_args(argv)
    profiled = args.mode == "inspect-stock" or args.stock_profile
    if profiled and (args.model is None or not args.backup):
        parser.error("A per-brewer profile requires --model and an original --backup")
    if (args.model == "pico-c") != (args.display is not None):
        parser.error("Specify --display exactly when selecting Pico C")
    if args.model == "pico-c" and (not profiled or args.mode == "restore"):
        parser.error("Pico C requires its per-brewer stock backup and restore-stock for restoration")
    model = ("pico-c-" + args.display) if args.display else (args.model or "pico-pro")
    web_directory = C_FIRMWARE / "web" if args.display else WEB_DIRECTORY
    stock_profile = None
    dependencies = None
    if profiled:
        try:
            dependencies = backup.load_dependencies()
            stock_profile = inspect_stock(args.backup, model, dependencies[1])
        except Exception as exc:
            print("STOPPED: " + _safe_error(exc), file=sys.stderr, flush=True)
            return 1
    if args.mode == "inspect-stock":
        print(json.dumps({"backup_sha256": stock_profile.backup_sha256,
                          "uid_sha256": stock_profile.uid_digest.hex(),
                          "model": args.model, "display_variant": args.display or ""}))
        return 0
    if args.mode == "prepare-install":
        try:
            if stock_profile is None:
                load_stock_backup(args.backup)
            firmware_dir = ((C_FIRMWARE / "standalone/pico-c" / args.display) if args.display
                            else (RECOVERY_FILE.parent / args.model))
            load_candidate(firmware_dir / (args.model + "-standalone.bin"), model)
            load_web_bundle(web_directory, model)
            _load_hub_recovery(model)
            if stock_profile is None:
                backup.load_dependencies()
        except Exception as exc:
            emit("STOPPED: " + _safe_error(exc))
            return 1
        emit("Verified the stock backup, standalone image, browser files and USB dependencies offline.")
        return 0
    if not 1 <= args.power_wait <= 300:
        parser.error("--power-wait must be between 1 and 300 seconds")
    files = None
    transport = None
    connection = None
    phase = "local validation"
    details = {"install_revision": INSTALL_REVISION, "mode": args.mode,
               "started_utc": datetime.now(timezone.utc).isoformat(),
               "success": False, "operation_verified": False, "warnings": [],
               "mutation_attempts": [], "application_write_attempted": False,
               "application_readback_verified": False, "persistent_write_attempted": False,
               "helper_uploaded_bytes": 0, "helper_executed": False}
    try:
        owner = backup.sudo_owner()
        recovery = uid_digest = None
        if stock_profile is not None:
            original, recovery, uid_digest = stock_profile.application, stock_profile.recovery, stock_profile.uid_digest
            details["backup"] = {"size_bytes": BACKUP_SIZE, "sha256": stock_profile.backup_sha256,
                                 "original_image_sha256": _image_metadata(original)["sha256"]}
        elif getattr(args, "oem_directory", None) is not None:
            stock = load_stock_directory(args.oem_directory)
            original, recovery = stock[APP_NAME], stock[RECOVERY_NAME]
            uid_digest = bytes.fromhex(PROFILE_UID_SHA256)
            details["oem_images"] = {name: _image_metadata(data) for name, data in stock.items()}
        elif args.mode == "restore-stock" or getattr(args, "verify_backup_uid", False):
            original, details["backup"], recovery, uid_digest = load_stock_backup(args.backup)
        else:
            original, details["backup"] = verify_backup(args.backup)
        key = load_key(args.key_file, owner) if hasattr(args, "key_file") else None
        if args.model is not None or args.mode in ("install", "install-recovery"):
            details["target_model"] = args.model or "pico-pro"
        if args.display:
            details["display_variant"] = args.display
        candidate = load_candidate(args.firmware, model) if args.mode == "install" else None
        web_manifest, web_payloads = (load_web_bundle(args.web_directory or web_directory, model)
                                      if args.mode == "install" else (None, {}))
        if candidate is not None:
            details["candidate"] = _image_metadata(candidate)
        probe_module, cc, helper, version = dependencies or backup.load_dependencies()
        files = InstallFiles(args.output, owner)
        details["cc3200tool_version"] = version
        details["helper"] = {"name": backup.HELPER_NAME, "size_bytes": len(helper),
                             "sha256": backup.HELPER_SHA256, "destination": "SRAM only"}
        payloads = {APP_NAME: original} if args.mode in ("restore", "restore-stock") else {KEY_NAME: key}
        if args.mode == "restore-stock":
            payloads[RECOVERY_NAME] = recovery
        if args.mode == "install-recovery":
            payloads[RECOVERY_NAME] = _load_hub_recovery(model)
        if args.mode == "install":
            payloads[APP_NAME] = candidate
            payloads.update(web_payloads)
        if "oem_images" in details:
            emit("LOCAL OEM IMAGES: original application and recovery verified")
        else:
            emit("LOCAL BACKUP: exact original full-flash backup and stock image verified")
        emit("OPERATION: " + args.mode)
        emit("REPORT: " + str(files.output))
        emit("JOURNAL: " + str(files.journal_path))
        result = 1
        with files:
            source = ({"backup_sha256": details["backup"]["sha256"]} if "backup" in details
                      else {"oem_images": details["oem_images"]})
            files.record("session-start", mode=args.mode, **source)
            try:
                transport = (transport_factory or probe_module.CH340USBTransport)()
                try:
                    with backup.USBSession(transport) as raw:
                        try:
                            emit("TRANSPORT: " + raw.description)
                            phase = "initial connection"

                            def connection_report(line):
                                if line.startswith(("BREAK asserted.", "Listening for ",
                                                    "Bootloader version:")):
                                    emit(line)
                                elif line.startswith("SUCCESS: complete GET_VERSION"):
                                    emit("CONNECTION: ROM version response verified")

                            payload = probe_module.probe(raw, args.power_wait, report=connection_report)
                            port = backup.USBSerialPort(raw)
                            connection = build_connection(cc, probe_module, port, helper,
                                                          args.mode, payloads, files,
                                                          web_manifest=web_manifest, model=model,
                                                          uid_digest=uid_digest, stock_profile=stock_profile)
                            connection.vinfo = cc.CC3x00VersionInfo.from_packet(payload)
                            if (not connection.vinfo.is_cc3200
                                    or connection.vinfo.bootloader[1] != 4):
                                raise InstallError("Unexpected device or ROM-loader version")
                            details["bootloader_version"] = list(connection.vinfo.bootloader)
                            phase = "RAM loader setup"
                            emit("LOADER: loading only the pinned temporary SRAM helper")
                            connection.switch_to_nwp_bootloader()
                            if not connection.helper_executed:
                                raise InstallError("The pinned RAM helper did not execute")
                            phase = "queued filesystem operation"
                            with backup.receive_channel(raw) as receiver:
                                port.transport = receiver
                                emit("USB RX: receive channel ready for filesystem operations")
                                run_operation(connection, args.mode, key, candidate, original, files,
                                              progress=emit, details=details)
                            port.transport = raw
                        except BaseException as exc:
                            if isinstance(exc, KeyboardInterrupt):
                                raise
                            # USBSession records the escaping exception. Only
                            # sanitized messages may reach that error buffer.
                            raise _safe_exception(exc) from None
                except probe_module.CH340USBError:
                    if not details["operation_verified"] or not transport.details.get("cleanup_errors"):
                        raise
                    details["warnings"].append("USB cleanup needs a cable reconnect")
                details["success"] = True
                files.record("session-success", mode=args.mode)
                result = 0
            except KeyboardInterrupt:
                details["error"] = {"phase": phase, "type": "KeyboardInterrupt", "message": "Interrupted"}
                result = 130
            except Exception as exc:
                details["error"] = {"phase": phase, "type": type(exc).__name__, "message": _safe_error(exc)}
                if isinstance(exc, HandshakeError):
                    details["error"]["handshake"] = dict(exc.diagnostic)
                result = 1
            finally:
                if "error" in details:
                    details["success"] = False
                if connection is not None:
                    details["mutation_attempts"] = connection.mutation_attempts
                    details["application_write_attempted"] = connection.application_write_attempted
                    details["application_readback_verified"] = connection.application_verified
                    details["persistent_write_attempted"] = bool(connection.mutation_attempts)
                    details["helper_uploaded_bytes"] = connection.helper_sent
                    details["helper_executed"] = connection.helper_executed
                if transport is not None:
                    details["usb"] = _usb_summary(transport.details)
                details["completed_utc"] = datetime.now(timezone.utc).isoformat()
            files.publish(details)
        if details["success"]:
            if model in C_STOCK and args.mode == "restore-stock":
                emit("SUCCESS: Pico C stock application verified and recovery matches the original backup")
            else:
                emit(SUCCESS_MARKERS[args.mode])
        else:
            error = details.get("error", {})
            emit("STOPPED during " + error.get("phase", phase) + ": "
                   + error.get("message", "The operation did not verify"))
            if not details["persistent_write_attempted"]:
                emit("No Pico flash or file write was attempted")
            if args.mode == "restore-stock" and details.get("persistent_write_attempted"):
                emit("Keep the restore source and report. Retry restore-stock in a fresh USB session before booting.")
            elif details.get("application_write_attempted") and not details.get("application_readback_verified"):
                emit("APPLICATION: image2 may be incomplete; use a fresh restore session with the original backup")
            emit("No retries or automatic recovery writes were attempted")
        emit("REPORT: " + str(files.output))
        emit("JOURNAL: " + str(files.journal_path))
        emit("The application was not started; no boot-information, erase, delete, or format command was sent")
        if transport is not None and transport.details.get("cleanup_errors"):
            emit("USB CLEANUP: reconnect USB before further use")
        return result
    except KeyboardInterrupt:
        emit("STOPPED: interrupted")
        return 130
    except Exception as exc:
        emit("STOPPED: " + _safe_error(exc))
        if files is not None:
            emit("Keep the private journal and any partial report for recovery")
        return 1


if __name__ == "__main__":
    exit_code = main()
    if any(getattr(receiver.raw, "_queued_receive_cleanup_blocked", False)
           for receiver in backup._RETAINED_USB_RECEIVERS):
        sys.stdout.flush()
        sys.stderr.flush()
        os._exit(exit_code or 1)
    sys.exit(exit_code)
