"""Apply the current status page to the pinned, smaller AVR runtime at build time."""
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def stage(runtime):
    api = runtime / "zymatic_api.c"
    code = api.read_text()
    changes = {
        '#include "site/zymatic_site.h"': '#include "site/zymatic_compact_site.h"',
        "sizeof(zymatic_site)-1": "sizeof(zymatic_compact_site)",
        "sizeof(zymatic_site)": "sizeof(zymatic_compact_site)",
        "ADDRESS(zymatic_site)": "ADDRESS(zymatic_compact_site)",
        r"text/html; charset=utf-8\r\n\r\n": r"text/html; charset=utf-8\r\nContent-Encoding: gzip\r\n\r\n",
    }
    for before, after in changes.items():
        if code.count(before) != 1:
            raise ValueError("Review the pinned compact HTTP page response")
        code = code.replace(before, after)
    api.write_text(code)
    header = ROOT / "site/zymatic_compact_site.h"
    (runtime / "site" / header.name).write_bytes(header.read_bytes())
