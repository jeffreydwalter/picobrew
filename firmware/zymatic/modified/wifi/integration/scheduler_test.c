#include <assert.h>
#include <string.h>
#include "scheduler.h"

struct zym_cc3000 candidate_radio;
struct zym_cc3000_broker candidate_broker;
uint8_t test_wired, test_wifi, test_clock=1;
static unsigned ethernet, radio_polls, wired_abandons, starts, loans, resumes, restarts, failures;
static bool allow_bind, allow_loan, lose_on_poll;
static uint8_t last_mode;
static ZymaticNetwork http;
static const uint8_t key[32];

/* Execute the existing wired driver down to its hardware boundary. Any
 * modeled W5100 register access during a Wi-Fi loan hard-fails the test. */
uint8_t zymatic_test_network_selected(void) { return test_wired == 1; }
uint8_t zymatic_test_register(uint8_t write, uint16_t address, uint8_t *value)
{
    (void)write;
    assert(test_wired == 1 && !zym_wifi_quarantined && address == 0x701);
    ++ethernet;
    *value = 1;
    return 1;
}
void zym_wired_abandon(ZymaticNetwork *n)
{
    ++wired_abandons;
    memset(n, 0, sizeof(*n));
    n->phase = 8;
}
uint8_t zymatic_api_feed(ZymaticNetwork *n, uint8_t byte, const uint8_t k[32])
{ (void)n; (void)byte; (void)k; assert(0); return 0; }
void zymatic_api_reply(ZymaticNetwork *n, const uint8_t k[32], const char *uid, uint32_t now, uint8_t mode)
{ (void)n; (void)k; (void)uid; (void)now; (void)mode; assert(0); }
int8_t zymatic_api_more(ZymaticNetwork *n) { (void)n; assert(0); return 0; }
uint8_t zymatic_api_reply_byte(const ZymaticNetwork *n, uint16_t i) { (void)n; (void)i; assert(0); return 0; }

bool zym_cc3000_board_begin(struct zym_cc3000_broker *b, struct zym_cc3000 *r, uint16_t port)
{
    assert(b == &candidate_broker && r == &candidate_radio && port == 80);
    ++starts;
    if (allow_bind) b->owner = ZYM_RADIO_FREE;
    return allow_bind;
}
void zym_cc3000_board_poll(struct zym_cc3000_broker *b, struct zym_cc3000 *r, ZymaticNetwork *n,
                           const uint8_t k[32], const char *uid, uint32_t now, uint8_t mode)
{
    assert(b == &candidate_broker && r == &candidate_radio && n == &http && k == key && uid && now);
    assert(b->owner != ZYM_RADIO_OEM);
    ++radio_polls;
    last_mode = mode;
    if (lose_on_poll) b->owner = ZYM_RADIO_LOST;
}
bool zym_spi_yield_to_stock(struct zym_cc3000_broker *b)
{
    ++loans;
    if (!allow_loan) return false;
    b->owner = ZYM_RADIO_OEM;
    return true;
}
bool zym_spi_resume_from_stock(struct zym_cc3000_broker *b)
{
    assert(b->owner == ZYM_RADIO_OEM);
    ++resumes;
    b->owner = ZYM_RADIO_FREE;
    b->oem_queued = false;
    return true;
}
void zym_spi_stock_restart(struct zym_cc3000_broker *b)
{ assert(b->owner == ZYM_RADIO_OEM); ++restarts; b->owner = ZYM_RADIO_LOST; }
void zym_broker_link_lost(struct zym_cc3000_broker *b) { b->owner = ZYM_RADIO_LOST; }
void zym_spi_close_if_owned(void) { ++failures; }

static void tick(uint8_t mode) { zym_wifi_network_poll(&http, key, "test", 1200, mode); }

int main(int argc, char **argv)
{
    assert(argc == 2);
    test_wifi = 1;
    zym_wifi_home_enter(); /* unavailable radio: allow all original input */
    assert(starts == 1 && zym_wifi_home_choice(3, 42) == 3);
    tick(ZYMATIC_MENU);
    assert(!ethernet && !radio_polls && !loans);
    allow_bind = true;
    if (!strcmp(argv[1], "rewire")) {
        test_wired = 1;
        tick(ZYMATIC_MENU); assert(ethernet == 1);
        http.flags = 0xff;
        test_wired = 0;
        tick(ZYMATIC_OUTER); zym_wifi_home_enter(); tick(ZYMATIC_MENU);
        assert(!http.flags && radio_polls == 1 && ethernet == 1);
        allow_loan = true;
        assert(zym_wifi_home_choice(2, 100) == 2);
        zym_wifi_before_restart(); assert(restarts == 1);
        test_wired = 1; tick(ZYMATIC_MENU); tick(ZYMATIC_MENU);
        assert(wired_abandons == 1 && ethernet == 3 && radio_polls == 1);
        return 0;
    }
    zym_wifi_home_enter();
    tick(ZYMATIC_MENU);
    assert(radio_polls == 1 && last_mode == ZYMATIC_MENU && !ethernet);
    if (!strcmp(argv[1], "disconnected")) {
        lose_on_poll = true;
        tick(ZYMATIC_MENU); assert(failures == 1 && !test_wifi);
        assert(zym_wifi_home_choice(0,42) == 0 && !loans);
        zym_wifi_home_enter(); assert(zym_wifi_home_choice(3,200) == 3);
        test_wired = 1; tick(ZYMATIC_PROGRAM);
        assert(zym_wifi_quarantined && !ethernet && radio_polls == 2);
        return 0;
    }
    if (!strcmp(argv[1], "clock-stopped")) {
        test_clock = 0;
        assert(zym_wifi_home_choice(4,1234) == 4 && !zym_wifi_pending());
        assert(zym_wifi_quarantined && failures == 1 && !ethernet);
        return 0;
    }
    const uint32_t press = 0xfffffffa;
    assert(zym_wifi_home_choice(5,press) == 0xff);
    assert(zym_wifi_pending() && candidate_broker.oem_queued);
    tick(ZYMATIC_MENU); assert(last_mode == ZYMATIC_OUTER);
    if (!strcmp(argv[1], "timeout")) {
        assert(zym_wifi_home_choice(0,press+2999) == 0xff);
        assert(zym_wifi_home_choice(1,press+3000) == 5);
        assert(zym_wifi_quarantined && !zym_wifi_pending() && failures == 1);
        test_wired = 1; tick(ZYMATIC_MENU);
        assert(!ethernet && radio_polls == 2);
        return 0;
    }
    assert(!strcmp(argv[1], "normal"));
    allow_loan = true;
    assert(zym_wifi_home_choice(0,press+20) == 5 && !zym_wifi_pending());
    tick(ZYMATIC_PROGRAM); tick(ZYMATIC_DIALOG);
    assert(radio_polls == 2 && !ethernet);
    zym_wifi_home_enter(); assert(resumes == 1);
    tick(ZYMATIC_MENU); assert(last_mode == ZYMATIC_MENU && radio_polls == 3);
    /* The OEM can reconfigure the radio in a stock loan. Resume only if a
     * complete fresh stock generation passes the board's bind guards. */
    assert(zym_wifi_home_choice(2,43) == 2);
    zym_wifi_before_restart(); allow_bind = false;
    zym_wifi_home_enter(); tick(ZYMATIC_MENU);
    assert(radio_polls == 3 && restarts == 1 && resumes == 1);
    allow_bind = true; tick(ZYMATIC_MENU); /* recover without another dial action */
    assert(radio_polls == 4 && !ethernet);
    return 0;
}
