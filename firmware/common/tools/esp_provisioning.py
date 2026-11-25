"""Place one device key into the release's verified empty SPIFFS image."""
import hashlib
import re
import struct

import spiffs_format


# Exact empty images from the release formatters. Refuse to modify a populated
# or differently formatted filesystem, even if its logical directory is empty.
EMPTY = {
    "esp32-z-recovery": "c63d34b5dfda648f125d1c8f75f01bb089ec569b6622f72a79df193cbba4fa91",
    "esp8266-2m": "18a484702761fe942d97a59918fabc46d3f375c6e7c33ad252fbf612797aff48",
    "esp32-4m": "5ab1eab8391a2fb3e71c21c348b2c283e356cad0bcb7881367860279357909a6",
}


def stage(empty, profile, key):
    if not isinstance(key, str) or not re.fullmatch("[0-9a-f]{64}", key) or key in ("0"*64, "f"*64):
        raise ValueError("USB setup needs a private, non-default device key")
    if hashlib.sha256(empty).hexdigest() != EMPTY.get(profile):
        raise ValueError("The release filesystem is not the verified empty image for this machine")
    image = bytearray(empty)
    value = key.encode("ascii")
    # The pinned formatter assigns object 1 its header in page 1 and its
    # first span in page 2. ESP32's mtime update finalizes a new object header
    # in page 3 and retires page 1. The other blocks retain their release bytes.
    page = 256
    file_id = 0x8001
    esp32 = profile.startswith("esp32")
    if esp32:
        struct.pack_into("<HHH", image, 0, 0, 1, file_id)
    else:
        struct.pack_into("<HH", image, 0, file_id, 1)
    def header(number, flags, metadata):
        start = page * number
        struct.pack_into("<HHBxxxIB32s", image, start,
                         file_id, 0, flags, len(value), 1, b"/pb/api.key")
        image[start+45:start+45+len(metadata)] = metadata
        struct.pack_into("<H", image, start+45+len(metadata), 2)
    if esp32:
        header(1, 0x78, b"\xff"*4)
        header(3, 0xf8, bytes(4))
    else:
        header(1, 0xf8, bytes(1))
    struct.pack_into("<HHB", image, 2*page, 1, 0, 0xfc)
    image[2*page+5:2*page+5+len(value)] = value
    result = bytes(image)
    spiffs_format.inspect_image(result, profile, {"/pb/api.key": value})
    return result


def matches_stored_esp32_key(image, key):
    """Read the live one-page key the device would select, without modifying SPIFFS."""
    if len(image) not in (0x16f000, 0x200000) or not isinstance(key, str) or not re.fullmatch("[0-9a-f]{64}", key):
        return False
    page_size, block_size = 256, 4096
    count = len(image) // block_size
    found = {}
    for block in range(count):
        base = block * block_size
        if struct.unpack_from("<H", image, base + 252)[0] != (0x529 ^ page_size ^ (count - block)):
            return False
        for entry in range(15):
            object_id = struct.unpack_from("<H", image, base + 2*entry)[0]
            if object_id in (0, 0xffff) or not object_id & 0x8000:
                continue
            offset = base + page_size * (entry + 1)
            owner, span, flags = struct.unpack_from("<HHB", image, offset)
            if owner != object_id or span or flags != 0xf8:
                continue
            name = bytes(image[offset+13:offset+45]).split(b"\0", 1)[0]
            if name not in (b"/pb/api.key", b"/pb/api.key.bak"):
                continue
            if name in found:
                return False
            size = struct.unpack_from("<I", image, offset+8)[0]
            page = struct.unpack_from("<H", image, offset+49)[0]
            if size not in (64, 65, 66) or page >= len(image)//page_size or page % 16 == 0 or image[offset+12] != 1:
                return False
            reference = page*page_size
            lookup = (page//16) * block_size + 2*(page % 16 - 1)
            if (struct.unpack_from("<HHB", image, reference) != (owner & 0x7fff, 0, 0xfc)
                    or struct.unpack_from("<H", image, lookup)[0] != (owner & 0x7fff)):
                return False
            found[name] = image[reference+5:reference+5+size]
    record = found.get(b"/pb/api.key", found.get(b"/pb/api.key.bak"))
    value = key.encode("ascii")
    return record in (value, value+b"\n", value+b"\r\n")


def _read_migration_image(image):
    contents = {"files": {}, "metadata": {}, "references": {}}
    spiffs_format.inspect_image(image, "esp32-z-recovery", None, relocation=contents)
    return contents


def _stage_board_seed(initial, seed):
    """Create one read-only 120-byte OEM settings file alongside the optional device key."""
    if seed is None or seed == b"\xff" * 120:
        return initial
    if (len(seed) != 120 or int.from_bytes(seed[:4], "little") != 0 or max(seed[4:8]) > 1 or
            int.from_bytes(seed[8:12], "little") > 2 or b"\0" not in seed[12:44] or b"\0" not in seed[44:108] or
            any(int.from_bytes(seed[n:n+4], "little") > limit for n, limit in ((108,2),(112,2),(116,1)))):
        raise ValueError("Unrecognized Z board settings in the original EEPROM partition")
    contents = _read_migration_image(initial)
    name = "/pb/zboard.seed"
    if name in contents["files"]:
        raise ValueError("The Z board settings seed already exists")
    dest = bytearray(initial)
    # initial is either the verified empty filesystem or its single device-key variant.
    free = [n for n in range(1,16) if struct.unpack_from("<H",dest,2*(n-1))[0] == 0xffff]
    header, data = free[:2]
    owner = 2
    struct.pack_into("<H", dest, 2*(header-1), owner|0x8000)
    struct.pack_into("<H", dest, 2*(data-1), owner)
    struct.pack_into("<HHBxxxIB32s", dest, 256*header, owner|0x8000, 0, 0xf8, len(seed), 1, name.encode())
    struct.pack_into("<IH", dest, 256*header+45, 0, data)
    struct.pack_into("<HHB", dest, 256*data, owner, 0, 0xfc)
    dest[256*data+5:256*data+5+len(seed)] = seed
    result = bytes(dest)
    spiffs_format.inspect_image(result,"esp32-z-recovery",{**contents["files"],name:seed})
    return result


def migrate_z_files(source, empty, key, *, paired, board_seed=None):
    """Repack all live ESP32 files, including transactional backups and metadata.

    SPIFFS stores absolute page numbers in index pages. Relocate every live page
    and referenced index, then decode and compare the entire logical directory
    before returning any data to the USB writer.
    """
    profile = "esp32-z-recovery"
    if hashlib.sha256(empty).hexdigest() != EMPTY[profile]:
        raise ValueError("The target filesystem is not the pinned, empty Z layout")
    if not source or source == b"\xff" * len(source):
        if paired: raise ValueError("The paired Z filesystem is missing")
        return _stage_board_seed(stage(empty, profile, key) if key else empty, board_seed)
    old = _read_migration_image(source)
    files, meta = old["files"].copy(), old["metadata"].copy()
    existing_key = files.get("/pb/api.key", files.get("/pb/api.key.bak"))
    if paired or existing_key is not None:
        if not key or existing_key not in (key.encode(), (key+"\n").encode(), (key+"\r\n").encode()):
            raise ValueError("The existing Z device key does not match its Hub pairing")
    # Avoid merging an ambiguous, partially initialized key into a new identity.
    if existing_key is None and "/pb/api.key.tmp" in files:
        raise ValueError("An unfinished API key transaction requires review")
    initial = stage(empty, profile, key) if key and existing_key is None else empty
    if board_seed is not None and "/pb/zboard.seed" in files:
        raise ValueError("An existing Z board settings seed conflicts with the stock EEPROM")
    initial = _stage_board_seed(initial, board_seed)
    dest = bytearray(initial)
    reserved = _read_migration_image(initial)
    files.update(reserved["files"])
    meta.update(reserved["metadata"])
    pages = old["pages"]
    # Leave three full erase blocks for SPIFFS garbage collection.
    available = [p for p in range(1,(len(dest)//4096-3)*16) if p%16 and p not in reserved["pages"]
                 and struct.unpack_from("<H", initial, p//16*4096+2*(p%16-1))[0] == 0xffff]
    if len(pages) > len(available):
        raise ValueError("The existing files exceed the new Z filesystem capacity")
    mapping = dict(zip(sorted(pages),available))
    used = {owner & 0x7fff for owner,_,_ in reserved["pages"].values()}
    objects = {}
    next_owner = 1
    for old_page, new_page in mapping.items():
        owner = pages[old_page][0] & 0x7fff
        if owner not in objects:
            while next_owner in used: next_owner += 1
            objects[owner] = next_owner
            next_owner += 1
        new_id = objects[owner] | (pages[old_page][0] & 0x8000)
        content = bytearray(source[old_page*256:(old_page+1)*256])
        struct.pack_into("<H",content,0,new_id)
        for index, data_page in old["references"].get(old_page, ()):
            struct.pack_into("<H",content,index,mapping[data_page])
        dest[new_page*256:(new_page+1)*256] = content
        struct.pack_into("<H",dest,new_page//16*4096+2*(new_page%16-1),new_id)
    result = bytes(dest)
    check = _read_migration_image(result)
    if check["files"] != files or check["metadata"] != meta:
        raise ValueError("Z filesystem migration readback failed")
    return result
