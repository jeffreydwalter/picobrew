"""Generate the equivalent table-driven HTTP route lookup in the temporary AVR candidate."""
from hashlib import sha256
from pathlib import Path
import re


def stage(runtime):
    path = Path(runtime) / "zymatic_api.c"
    source = path.read_text()
    start = source.index('    if (equal(line, CONSTANT("POST")')
    end = source.index('        uint32_t offset, revision;', start)
    if sha256(source[start:end].encode()).hexdigest() != "85e99ceaa1803f849bf6e85cedfec529052ddee03e7ffc1a5af762cebeb0cc0e":
        raise ValueError("Review the new HTTP routes before optimizing the firmware")
    enum = re.search(r'enum \{ (UNKNOWN, INDEX, .*?) \};', source)[1].split(", ")
    def table(entries):
        return '"' + ''.join('\\%03o%s\\000' % (enum.index(route), name) for name, route in entries) + '"'
    post, get = source[start:end].split('} else if (!equal(line, CONSTANT("GET"), 0))')
    items = re.findall(r'equal\(path, CONSTANT\("([^"]*)"\), 0\)\) n->route = (\w+);', get)
    api = "/api/v1"
    tables = {
        "POST_ROUTES": table([("/commands", "ENCODER")] + [("/pairing/" + name, route)
            for name, route in re.findall(r'equal\(stage, CONSTANT\("([^"]+)"\), 0\)\) n->route = (\w+);', post)]),
        "GET_ROUTES": table([(name[len(api):], route) for name, route in items if name.startswith(api)]),
        "SITE_ROUTES": table([(name, route) for name, route in items if not name.startswith(api)]),
    }
    dispatch = r'''    const char *relative = path;
    uint8_t is_api = prefix(&relative, CONSTANT("/api/v1"));
    if (equal(line, CONSTANT("POST"), 0)) {
        if (is_api) n->route = match_route(relative, CONSTANT(POST_ROUTES));
        if (!n->route) n->http_error = METHOD;
    } else if (!equal(line, CONSTANT("GET"), 0)) n->http_error = METHOD;
    else if (!is_api) n->route = match_route(path, CONSTANT(SITE_ROUTES));
    else if (!(n->route = match_route(relative, CONSTANT(GET_ROUTES)))) {
'''
    for marker, data in tables.items():
        dispatch = dispatch.replace(marker, data)
    source = source[:start] + dispatch + source[end:]
    original = ('        const char *p = path;\n',
                'prefix(&p, CONSTANT("/api/v1/history/samples/"))',
                'prefix(&p, CONSTANT("/api/v1/display/"))')
    replacement = ('        const char *p = relative;\n', 'prefix(&p, CONSTANT("/history/samples/"))',
                   'prefix(&p, CONSTANT("/display/"))')
    for old, new in zip(original, replacement, strict=True):
        if source.count(old) != 1:
            raise ValueError("Unexpected dynamic route handler")
        source = source.replace(old, new)
    matcher = r'''static uint8_t match_route(const char *path, FlashAddress table)
{
    uint8_t route, expected;
    while ((route = READ(table++)) != 0) {
        const char *p = path;
        do {
            expected = READ(table++);
            if (expected != (uint8_t)*p++) break;
            if (!expected) return route;
        } while (1);
        while (expected) expected = READ(table++);
    }
    return UNKNOWN;
}

'''
    path.write_text(source.replace("static void parse_request(", matcher + "static void parse_request("))
