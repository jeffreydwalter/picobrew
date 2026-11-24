#include "http.h"
#include "recovery.h"
#include <assert.h>
#include <string.h>

static PicoRecovery core;
enum { TEST_FILE_CAPACITY = 188416 };
static uint8_t files[PICO_RECOVERY_FILE_COUNT][TEST_FILE_CAPACITY], boot_record[8];
static PicoRecoveryFileInfo info[PICO_RECOVERY_FILE_COUNT];
static bool present[PICO_RECOVERY_FILE_COUNT];
static unsigned writes, boot_writes, write_opens;
static int open_file = -1;
static bool write_mode;
static char arena[PICO_HTTP_ARENA_CAPACITY];

static int stat_file(void *unused, PicoRecoveryFile file, PicoRecoveryFileInfo *out)
{
    (void)unused; assert(file < PICO_RECOVERY_FILE_COUNT);
    *out = info[file]; return present[file] ? 0 : 1;
}
static int32_t read_open(void *unused, PicoRecoveryFile file)
{
    (void)unused; assert(file < PICO_RECOVERY_FILE_COUNT);
    if (open_file != -1 || !present[file]) return -1;
    open_file = file; write_mode = false; return 0;
}
static int32_t write_open(void *unused, PicoRecoveryFile file)
{
    (void)unused; assert(file < PICO_RECOVERY_FILE_COUNT);
    if (open_file != -1 || !present[file]) return -1;
    open_file = file; write_mode = true; ++write_opens; info[file].size = 0;
    memset(files[file], 0xff, sizeof files[file]); return 0;
}
static int32_t create_open(void *unused, PicoRecoveryFile file, uint32_t allocated)
{
    assert(file >= PICO_RECOVERY_WEB_I && file < PICO_RECOVERY_FILE_COUNT);
    if (present[file] || open_file != -1) return -1;
    present[file] = true; info[file].allocated = allocated;
    return write_open(unused, file);
}
static int32_t read_bytes(void *unused, int32_t handle, uint32_t offset, uint8_t *out, size_t length)
{
    (void)unused; assert(handle == 0 && open_file >= 0 && !write_mode);
    if ((uint64_t)offset + length > info[open_file].size) return -1;
    memcpy(out, files[open_file] + offset, length); return (int32_t)length;
}
static int32_t write_bytes(void *unused, int32_t handle, uint32_t offset, const uint8_t *data, size_t length)
{
    (void)unused; assert(handle == 0 && open_file >= 0 && write_mode);
    assert((uint64_t)offset + length <= sizeof files[open_file]);
    memcpy(files[open_file] + offset, data, length);
    info[open_file].size = offset + (uint32_t)length; ++writes; return (int32_t)length;
}
static int close_file(void *unused, int32_t handle)
{
    (void)unused; assert(handle == 0 && open_file >= 0); open_file = -1; return 0;
}
static int boot_read(void *unused, uint8_t out[8])
{
    (void)unused; memcpy(out, boot_record, 8); return 0;
}
static int boot_write(void *unused, const uint8_t data[8])
{
    (void)unused; memcpy(boot_record, data, 8); ++boot_writes; return 0;
}
static const PicoRecoveryOps ops = {
    stat_file, read_open, write_open, create_open, read_bytes, write_bytes, close_file, boot_read, boot_write
};

void recovery_http_reset(void)
{
    memset(files, 0, sizeof files); memset(info, 0, sizeof info); memset(present, 0, sizeof present);
    static const uint8_t initial[] = {0, 0x3d, 0, 0x20, 0xba, 0xdc, 0xcd, 0xab};
    memcpy(boot_record, initial, 8); present[PICO_RECOVERY_IMAGE2] = true;
    info[PICO_RECOVERY_IMAGE2].allocated = TEST_FILE_CAPACITY;
    writes = boot_writes = write_opens = 0; open_file = -1;
    pico_recovery_init(&core, &ops, NULL, 77);
}
int recovery_http_call(int method, const char *path, const char *body, size_t length, int auth, int configured)
{
    PicoHttpRequest request = {0}; PicoHttpResponse response;
    assert(strlen(path) < sizeof request.path);
    request.method = (PicoHttpMethod)method; strcpy(request.path, path);
    request.authenticated = auth != 0; request.auth_configured = configured != 0;
    if (length >= sizeof arena) { request.body = body; request.body_length = length; }
    else { memcpy(arena, body, length); arena[length] = 0; request.body = arena; request.body_length = length; }
    if (!pico_recovery_http_handler(&request, &response, arena, sizeof arena, &core)) return -1;
    assert(response.body == (uint8_t *)arena && response.body_length < sizeof arena);
    arena[response.body_length] = 0; return (int)response.status;
}
const char *recovery_http_response(void) { return arena; }
unsigned recovery_http_writes(void) { return writes; }
unsigned recovery_http_write_opens(void) { return write_opens; }
unsigned recovery_http_boot_writes(void) { return boot_writes; }
const uint8_t *recovery_http_boot(void) { return boot_record; }
void recovery_http_boot_selector(unsigned selector) { boot_record[0] = (uint8_t)selector; }

#ifdef RECOVERY_HTTP_FUZZ_MAIN
#include <stdio.h>
int main(void)
{
    recovery_http_reset();
    static const char valid[] = "{\"slot\":2,\"size\":1032,\"sha256\":\"0000000000000000000000000000000000000000000000000000000000000000\",\"version\":\"1.0.0\",\"web\":false}";
    char input[4096]; uint32_t random = 0x3200;
    for (size_t i = 0; i < sizeof valid - 1; ++i)
        assert(recovery_http_call(PICO_HTTP_POST, "/api/v1/recovery/plan", valid, i, 1, 1) == 400);
    for (size_t position = 0; position < sizeof valid - 1; ++position)
        for (unsigned byte = 0; byte < 256; ++byte) {
            memcpy(input, valid, sizeof valid); input[position] = (char)byte;
            int status = recovery_http_call(PICO_HTTP_POST, "/api/v1/recovery/plan", input, sizeof valid - 1, 1, 1);
            assert(status == 200 || status == 400 || status == 409);
        }
    for (unsigned trial = 0; trial < 5000; ++trial) {
        random = random * 1664525u + 1013904223u;
        size_t length = random % sizeof input;
        for (size_t i = 0; i < length; ++i) {
            random = random * 1664525u + 1013904223u; input[i] = (char)(random >> 24);
        }
        assert(recovery_http_call(PICO_HTTP_POST, "/api/v1/recovery/plan", input, length, 1, 1) == 400);
    }
    assert(writes == 0 && boot_writes == 0 && write_opens == 0);
    puts("Recovery HTTP ASan/UBSan: truncations, every byte substitution and 5000 arbitrary bodies passed.");
    return 0;
}
#endif
