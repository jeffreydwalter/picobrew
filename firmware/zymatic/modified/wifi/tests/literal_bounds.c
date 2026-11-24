/* Included in the test copy of the generated writer, with original/packed pairs. */
#include <stdio.h>

void test_route_lookup(void)
{
    static const char *const paths[] = {
        "/", "/pair.js", "/favicon.ico", "/api/v1", "/api/v1/health", "/api/v1/overview",
        "/api/v1/status", "/api/v1/history", "/api/v1/display", "/api/v1/commands",
        "/api/v1/pairing/request", "/api/v1/pairing/claim", "/api/v1/pairing/grant",
        "/api/v1/history/samples/0?id=1", "/api/v1/display/128?revision=0",
        "/api/v1/history/samples/65536?id=2", "/api/v1/history/samples/0?id=0",
        "/api/v1/display/127?revision=1", "/api/v1/display/1024?revision=1",
        "/api/v1/display/128?revision=4294967296", "/health", "/api/v1/", "/api/v10",
        "", "/api/v1/pairing", "/api/v1/pairing/", "/API/v1/status"
    };
    static const char *const methods[] = {"GET", "POST", "PUT"};
    static const char *const versions[] = {"HTTP/1.0", "HTTP/1.1", "HTTP/1.2"};
    for (size_t i = 0; i < sizeof(paths) / sizeof(paths[0]); ++i) {
        for (size_t m = 0; m < 3; ++m) {
            for (size_t v = 0; v < 3; ++v) {
                char baseline[128], candidate[128];
                ZymaticNetwork before = {0}, after = {0};
                snprintf(baseline, sizeof(baseline), "%s %s %s", methods[m], paths[i], versions[v]);
                strcpy(candidate, baseline);
                original_request(&before, baseline);
                parse_request(&after, candidate);
                assert(!memcmp(&before, &after, sizeof(after)));
            }
        }
    }
}

void test_literal_bounds(void)
{
    ZymaticNetwork expected, actual;
    for (size_t i = 0; i < sizeof(cases) / sizeof(cases[0]); ++i) {
        for (unsigned start = 0; start <= sizeof(actual.buffer.reply) + 1; ++start) {
            memset(&expected, 0x5a, sizeof(expected));
            expected.response_bytes = start;
            memcpy(&actual, &expected, sizeof(actual));
            for (const char *p = cases[i][0]; *p; ++p) append(&expected, *p);
            literal(&actual, CONSTANT(cases[i][1]));
            assert(!memcmp(&actual, &expected, sizeof(actual)));
        }
    }
}
