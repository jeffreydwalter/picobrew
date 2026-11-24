/* Reuse the complete native request path and its modeled W5100 receive buffer. */
#define main existing_api_tests
#include "tests/zymatic_api_native.c"
#undef main

int main(void)
{
    existing_api_tests();
    ZymaticNetwork net;
    const uint8_t paused = 4;
    char request[384];
    for (unsigned i = 0; i < 3; ++i) {
        const char *routes[] = {"request", "claim", "grant"};
        const char *body = "{\"nonce\":\"1111111122222222333333334444444455555555666666667777777788888888\"}";
        snprintf(request, sizeof(request), "POST /api/v1/pairing/%s HTTP/1.0\r\n%sContent-Length: %zu\r\n\r\n%s",
                 routes[i], auth, strlen(body), body);
        setup(&net); program_active = paused;
        req(&net, request);
        assert(strstr(sent, "405 Method Not Allowed") && !strstr(sent, "\"api_key\""));
    }
    zymatic_test_encoder[11] = 1;
    const unsigned long revision = display_token(paused);
    snprintf(request, sizeof(request), "{\"command\":\"encoder\",\"press\":true,\"display_revision\":%lu}", revision);
    command_request(request, paused, 1, "405 Method Not Allowed");
    assert(!zymatic_panel_take_press());
    snprintf(request, sizeof(request), "GET /api/v1/status HTTP/1.0\r\n%s\r\n", auth);
    setup(&net); program_active=paused; req(&net,request);
    assert(strstr(sent,"\"state\":\"recipe_pause\",\"busy\":true"));
    snprintf(request, sizeof(request), "GET /api/v1/display HTTP/1.0\r\n%s\r\n", auth);
    setup(&net); program_active=paused; req(&net,request);
    assert(strstr(sent,"\"dial\":\"off\""));
    puts("Read-only recipe pause reported the active program, served its display and rejected all command and pairing writes.");
    return 0;
}
