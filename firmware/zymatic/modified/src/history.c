#include "zymatic_history.h"
#include <stddef.h>
#include <string.h>
#ifdef __AVR__
#include <avr/io.h>
#include <avr/pgmspace.h>
#include <avr/interrupt.h>
#include <avr/wdt.h>
#define OEM(address, type, args) ((type(*) args)(uintptr_t)((address) / 2))
extern uint32_t oem_millis(void);
#define millis_now oem_millis
#define stock_cleanup OEM(0x28e8, void, (void))
#define stock_program OEM(0xf6fe, void, (uint8_t))
#define stock_clear OEM(0x19492, void, (void *, uint16_t, uint8_t))
#define memory(address) ((volatile uint8_t *)(uintptr_t)(address))
#define watchdog() wdt_reset()
static uint8_t ready(void) { return !(EECR & _BV(EEPE)); }
static uint8_t read_byte(uint16_t addr)
{
    uint8_t s = SREG;
    cli();
    EEAR = addr;
    EECR |= _BV(EERE);
    uint8_t v = EEDR;
    SREG = s;
    return v;
}
static uint8_t write_byte(uint16_t addr, uint8_t value)
{
    uint8_t s = SREG;
    cli();
    /* poll() only starts a write when the previous operation has finished.
     * An OEM ISR can own the EEPROM between polls. Retry if it just began one. */
    uint8_t available = ready();
    if (available && read_byte(addr) != value)
        stock_clear(NULL, addr, value);
    SREG = s;
    return available;
}
#else
extern uint8_t zh_test_ready(void), zh_test_read(uint16_t);
extern uint8_t zh_test_write(uint16_t, uint8_t);
extern void zh_test_program(uint8_t), zh_test_cleanup(void);
extern uint8_t zh_test_sram[];
extern uint32_t zh_test_millis(void);
#define ready zh_test_ready
#define read_byte zh_test_read
#define write_byte zh_test_write
#define stock_cleanup zh_test_cleanup
#define stock_program zh_test_program
#define stock_clear(object, addr, val) ((void)(object), write_byte(addr, val))
#define memory(address) (&zh_test_sram[address])
#define millis_now zh_test_millis
#define watchdog() ((void)0)
#endif

/* [0x100,0xff0) is between the 1.1.14 settings/session bytes and factory stamp.
 * Only the 16-byte sequence allocation survives an explicit custom factory reset.
 * The normal factory routine clears every session label, sample and state. */
enum {
    BASE = 0x100,
    HEADER = BASE + 16,
    DATA = HEADER + ZH_SLOTS * 32,
    END = DATA + ZH_ROWS * 16,
    COUNTER_TAG = 0x93,
    META_TAG = 0x5c,
    ROW_TAG = 0xa7,
    COMPLETE = 0xb4,
    TRUNC = 0x4a,
    ABORTED = 0x38,
    ABORTED_TRUNC = 0x54
};
_Static_assert(END == 0xff0, "history EEPROM layout");
typedef struct __attribute__((packed)) {
    uint8_t tag;
    uint32_t id;
    uint8_t reserved;
    uint16_t check;
} Anchor;
typedef struct __attribute__((packed)) {
    uint8_t tag;
    uint32_t id;
    uint8_t first;
    char name[21];
    uint16_t check;
    uint8_t state;
} Header;
typedef struct __attribute__((packed)) {
    uint8_t tag;
    uint32_t id;
    uint16_t seconds, wort, heat;
    uint8_t target, location, step;
    uint16_t check;
} Sample;
_Static_assert(sizeof(Anchor) == 8 && sizeof(Header) == 30 && sizeof(Sample) == 16,
               "history record packing");

enum { IDLE, OPEN, HEADER_WRITE, LOGGING, SAMPLE_WRITE, EVICT, CLOSING };
static struct {
    uint32_t latest, started, last, next_started;
    uint8_t enabled, valid, cursor, first[ZH_SLOTS], count[ZH_SLOTS];
    uint8_t phase, aborted, index, laststep, final, truncated, closing;
    uint8_t has_next, next_index, next_aborted, next_closed;
    uint16_t addr;
    uint8_t len, pos, care, written;
    union {
        Anchor a;
        Header h;
        Sample r;
        uint8_t raw[32];
    } tx;
} h;

static uint16_t crc(const void *data, uint8_t size)
{
    const uint8_t *p = data;
    uint16_t c = 0xffff;
    while (size--) {
        c ^= *p++;
        for (uint8_t i = 0; i < 8; i++)
            c = (c >> 1) ^ ((c & 1) ? 0xa001u : 0);
    }
    return c;
}
static uint8_t read_at(uint16_t a, void *data, uint8_t size)
{
#ifdef __AVR__
    uint8_t saved = SREG;
    cli();
#endif
    uint8_t available = ready();
    uint8_t *p = data;
    if (available)
        while (size--)
            *p++ = read_byte(a++);
#ifdef __AVR__
    SREG = saved;
#endif
    return available;
}
/* The AVR linker would otherwise duplicate the EEPROM address and queue helpers
 * in multiple recording paths, consuming the local website's flash allowance. */
static uint8_t slot(uint32_t id) { return (uint8_t)(id - 1u) & 7u; }
static __attribute__((noinline)) uint16_t meta_addr(uint8_t s) { return HEADER + s * 32u; }
static __attribute__((noinline)) uint16_t row_addr(uint8_t n) { return DATA + n * 16u; }
static __attribute__((noinline)) uint8_t plus(uint8_t pos, uint8_t count)
{
    uint16_t n = (uint16_t)pos + count;
    return (uint8_t)(n >= ZH_ROWS ? n - ZH_ROWS : n);
}
static uint8_t anchor_ok(Anchor *a)
{
    return a->tag == COUNTER_TAG && a->id && a->check == crc(&a->id, 5);
}
static uint8_t header_ok(Header *v)
{
    return v->id && v->first < ZH_ROWS && !v->name[20] && v->check == crc(&v->id, 26);
}
static uint8_t row_ok(Sample *v) { return v->check == crc(&v->id, 13); }
__attribute__((noinline)) void zymatic_history_init(void)
{
    memset(&h, 0, sizeof(h));
    while (!ready())
        watchdog();
    Anchor a;
    for (uint8_t i = 0; i < 2; i++) {
        read_at(BASE + i * 8, &a, sizeof(a));
        if (anchor_ok(&a) && a.id > h.latest) {
            h.latest = a.id;
            h.care = a.reserved & 7;
        }
    }
    if (!h.latest) {
        /* Never claim an unknown EEPROM allocation or silently reinitialize
         * a damaged counter on top of records with existing identities. */
        Anchor initial = {.id = 1};
        initial.check = crc(&initial.id, 5);
        for (uint16_t p = BASE; p < END; p++) {
            uint8_t b = read_byte(p);
            if (!b || b == 0xff)
                continue;
            /* A reset during first-ever enrollment may leave part of ID 1
             * before its tag. Strictly recognize its exact possible bytes. */
            if (p <= BASE + 8 || p >= HEADER || b != ((const uint8_t *)&initial)[p - BASE - 8])
                return;
        }
    } else {
        uint32_t newest = 0;
        for (uint8_t i = 0; i < ZH_SLOTS; i++) {
            Header v;
            read_at(meta_addr(i), &v, sizeof(v));
            if (v.tag != META_TAG)
                continue;
            if (!header_ok(&v) || v.id > h.latest || slot(v.id) != i)
                return;
            if (v.id == h.latest && (v.state & 0xf0) == 0xd0)
                h.care = v.state & 7;
            h.first[i] = v.first;
            uint8_t n = 0;
            while (n < ZH_ROWS) {
                Sample row;
                read_at(row_addr(plus(v.first, n)), &row, sizeof(row));
                if (row.tag != ROW_TAG || row.id != v.id)
                    break;
                if (!row_ok(&row))
                    return;
                ++n;
                watchdog();
            }
            h.valid |= (uint8_t)(1u << i);
            h.count[i] = n;
            if (v.id > newest) {
                newest = v.id;
                h.cursor = plus(v.first, n);
            }
        }
    }
    h.enabled = 1;
}

/* Factory commissioning does its own complete pass, even on an untouched
 * board. Only a known monotonic counter area is reserved on an enrolled unit. */
void zymatic_factory_clear_byte(void *object, uint16_t address, uint8_t value)
{
    if (h.latest && address >= BASE && address < HEADER)
        return;
    stock_clear(object, address, value);
    if (address == END - 1)
        zymatic_history_init();
}

static __attribute__((noinline)) void queue(uint16_t addr, uint8_t len)
{
    h.addr = addr;
    h.len = len;
    h.pos = 0;
}
/* One EEPROM byte per service, committed tag last. The old tag is invalidated
 * first, so interrupted application updates cannot validate a mixture of rows. */
static uint8_t pump(void)
{
    if (!h.len)
        return 1;
    if (!ready())
        return 0;
    if (h.pos) {
        uint8_t before = h.pos - 1;
        uint16_t p = before < h.len ? h.addr + before : h.addr;
        uint8_t check;
        if (!read_at(p, &check, 1))
            return 0;
        if (check != h.written) {
            h.enabled = 0;
            return 0;
        }
        }
    if (h.pos > h.len) {
        h.len = 0;
        return 1;
    }
    uint8_t k = h.pos;
    uint16_t p = k < h.len ? h.addr + k : h.addr;
    uint8_t v = k == 0 ? 0 : (k == h.len ? h.tx.raw[0] : h.tx.raw[k]);
    if (h.len == 1) {
        p = h.addr;
        v = h.tx.raw[0];
        k = 1;
    }
    if (!write_byte(p, v))
        return 0;
    h.pos = k + 1;
    h.written = v;
    return 0;
}
static uint8_t step_index(uint8_t index)
{
    uint16_t step = *memory(0x23c0) | ((uint16_t)*memory(0x23c1) << 8);
    if (index >= 10 || step >= 25 || step >= *memory(0x16be + 172u * index + 21))
        return 0xff;
    return (uint8_t)step;
}
static uint16_t tenths(uint16_t address)
{
#ifdef __AVR__
    uint8_t s = SREG;
    cli();
#endif
    uint32_t bits = *(volatile uint32_t *)memory(address);
#ifdef __AVR__
    SREG = s;
#endif
    return zymatic_temperature_tenths(bits);
}

uint16_t zymatic_temperature_tenths(uint32_t bits)
{
    if (!bits || bits > UINT32_C(0x43c80000))
        return 0xffff;
    if (bits < UINT32_C(0x3d000000))
        return 0;
    uint8_t shift = (uint8_t)(150 - (bits >> 23));
    uint32_t v = ((bits & 0x7fffffUL) | 0x800000UL) * 10;
    return (v + (UINT32_C(1) << (shift - 1))) >> shift;
}
static void sample(uint32_t now)
{
    Sample *r = &h.tx.r;
    uint32_t elapsed = (now - h.started) / 1000u;
    if (h.count[slot(h.latest)] && elapsed == (h.last - h.started) / 1000u)
        return;
    if (elapsed > 65535u || h.count[slot(h.latest)] == ZH_ROWS) {
        h.truncated = 1;
        return;
    }
    /* Invalidate each evicted session before reusing any of its sample pages.
     * Older session records never become visible under a new identity. */
    for (uint8_t i = 0; i < ZH_SLOTS; i++) {
        if (i != slot(h.latest) && (h.valid & (1u << i)) && h.count[i] && h.first[i] == h.cursor) {
            h.valid &= (uint8_t)~(1u << i);
            h.tx.raw[0] = 0;
            queue(meta_addr(i), 1);
            h.phase = EVICT;
            return;
        }
    }
    r->tag = ROW_TAG;
    r->id = h.latest;
    r->seconds = (uint16_t)elapsed;
    r->wort = tenths(0x23dc);
    r->heat = tenths(0x23e0);
    r->step = h.closing ? 0xff : step_index(h.index);
    r->target = r->location = 0xff;
    if (r->step != 0xff) {
        volatile uint8_t *s = memory(0x16be + 172u * h.index + 22u + 6u * r->step);
        r->target = s[2];
        r->location = s[4] <= 6 ? s[4] : 0xff;
    }
    r->check = crc(&r->id, 13);
    h.last = now;
    h.laststep = r->step;
    queue(row_addr(h.cursor), sizeof(*r));
    h.phase = SAMPLE_WRITE;
}
static void begin_session(uint8_t index, uint32_t now)
{
    ++h.latest;
    h.index = index;
    h.started = now;
    h.aborted = h.final = h.closing = h.truncated = 0;
    Anchor *a = &h.tx.a;
    a->tag = COUNTER_TAG;
    a->id = h.latest;
    a->reserved = h.care;
    a->check = crc(&a->id, 5);
    queue(BASE + ((uint8_t)h.latest & 1u) * 8, sizeof(*a));
    h.phase = OPEN;
}
/* Exact four routed bytes per stock step (temperature, time, valve, drain).
 * The original recipe name and step-name pointers are not identities. Only
 * the Clean/Rinse selection sets OEM SRAM[0x1ec4] before the run at 0xffea. */
#ifdef __AVR__
#define CARE_DATA __attribute__((section(".low_care"),used))
#define care_byte(p) pgm_read_byte_near(p)
#define LOW_CODE __attribute__((section(".low_functions"),noinline))
#else
#define CARE_DATA
#define care_byte(p) (*(p))
#define LOW_CODE
#endif
enum { CARE_OTHER = 1, CARE_BREW, CARE_RINSE, CARE_DEEP };
static const uint8_t cleaning_v1[] CARE_DATA = {
    152, 0, 0, 0, 152, 15, 1, 5, 152, 0, 0, 0,
    152, 3, 2, 1, 152, 2, 3, 1, 152, 2, 4, 1,
    152, 2, 5, 1, 197, 0, 0, 0, 197, 10, 1, 0,
    197, 2, 1, 0, 197, 2, 2, 0, 120, 10, 0, 2,
};
static const uint8_t clean_v6[] CARE_DATA = {
    0, 5, 1, 0, 0, 5, 5, 0, 120, 0, 0, 0,
    120, 20, 5, 0, 140, 0, 0, 0, 140, 35, 5, 0,
    0, 5, 1, 0, 0, 3, 0, 0, 140, 5, 5, 0,
    160, 5, 5, 0, 175, 5, 5, 0, 197, 0, 0, 0,
    197, 19, 5, 0, 0, 3, 1, 0, 0, 3, 0, 0,
    120, 3, 5, 0, 0, 5, 1, 8, 0, 0, 6, 0,
    0, 12, 5, 0,
};
static const uint8_t rinse_v3[] CARE_DATA = {
    0, 2, 0, 5,
};

static LOW_CODE uint8_t care_kind(uint8_t index)
{
    if (!*memory(0x1ec4)) return CARE_BREW;
    volatile uint8_t *program = memory(0x16be + 172u * index + 21);
    uint8_t count = *program++;
    const uint8_t *routes;
    uint8_t kind = CARE_DEEP;
    switch (count) {
    case sizeof(cleaning_v1)/4: routes = cleaning_v1; break;
    case sizeof(clean_v6)/4: routes = clean_v6; break;
    case sizeof(rinse_v3)/4: routes = rinse_v3; kind = CARE_RINSE; break;
    default: return CARE_OTHER;
    }
    while (count--) {
        program += 2; /* OEM SRAM step-name pointer */
        for (uint8_t field = 0; field < 4; ++field)
            if (*program++ != care_byte(routes++)) return CARE_OTHER;
    }
    return kind;
}
/* Bits 0–1 count completed brews, saturating at the manual's third brew.
 * Bit 2 requests a rinse and the associated manual inline-filter inspection. */
static __attribute__((noinline)) uint8_t after_care(uint8_t before, uint8_t kind)
{
    if (kind == CARE_RINSE) return before & 3;
    if (kind == CARE_DEEP) return 4;
    if (kind == CARE_BREW) return (before | 4) + ((before & 3) != 3);
    return before;
}

void zymatic_history_poll(uint32_t now)
{
    if (!h.enabled || !ready() || !pump())
        return;
    uint8_t i = slot(h.latest);
    switch (h.phase) {
    case IDLE:
        return;
    case OPEN: {
        Header *v = &h.tx.h;
        memset(v, 0, sizeof(*v));
        v->tag = META_TAG;
        v->id = h.latest;
        v->first = h.cursor;
        volatile uint8_t *name = memory(0x16be + 172u * h.index);
        for (uint8_t j = 0; j < 20 && name[j]; j++) {
            uint8_t c = name[j];
            v->name[j] = (c < 32 || c > 126) ? '?' : (char)c;
        }
        v->check = crc(&v->id, 26);
        h.valid &= (uint8_t)~(1u << i);
        h.count[i] = 0;
        h.first[i] = h.cursor;
        queue(meta_addr(i), sizeof(*v));
        h.phase = HEADER_WRITE;
        return;
    }
    case HEADER_WRITE:
        h.valid |= (uint8_t)(1u << i);
        h.phase = LOGGING;
        break;
    case SAMPLE_WRITE:
        ++h.count[i];
        h.cursor = plus(h.cursor, 1);
        h.phase = LOGGING;
        break;
    case EVICT:
        h.phase = LOGGING;
        break;
    case CLOSING:
        h.phase = IDLE;
        if (h.has_next && h.latest != UINT32_MAX) {
            h.has_next = 0;
            begin_session(h.next_index, h.next_started);
            h.aborted = h.next_aborted;
            h.closing = h.next_closed;
        }
        return;
    }
    if (h.closing && h.final) {
        if (h.aborted)
            h.tx.raw[0] = h.truncated ? ABORTED_TRUNC : ABORTED;
        else {
            h.care = after_care(h.care, h.closing);
            h.tx.raw[0] = 0xd0 | (h.truncated ? 8 : 0) | h.care;
        }
        queue(meta_addr(i) + offsetof(Header, state), 1);
        h.phase = CLOSING;
        return;
    }
    if (h.closing || !h.count[i] || h.laststep != step_index(h.index) ||
        (uint32_t)(now - h.last) >= ZH_INTERVAL) {
        if (!h.truncated)
            sample(now);
        if (h.closing && h.phase != EVICT)
            h.final = 1;
    }
}
/* At 0xfc70 the runner branches on 0x2288 for the canceled run. At 0xfce0
 * the stock cleanup clears that bit and the current step. Capture before it. */
void zymatic_program_cleanup(void)
{
    if (h.has_next)
        h.next_aborted = *memory(0x2288) != 0;
    else if (h.phase != IDLE)
        h.aborted = *memory(0x2288) != 0;
    stock_cleanup();
}
void zymatic_run_program(uint8_t index)
{
    /* Both OEM call sites pass the recipe bank index in r24. The runner
     * remains responsible for route selection, interlocks and physical exits. */
    uint8_t record = h.enabled && index < 10 && h.latest != UINT32_MAX;
    if (record) {
        if (h.phase == IDLE) {
            begin_session(index, millis_now());
            zymatic_history_poll(h.started);
        } else if (!h.has_next && h.closing) {
            /* A second program can begin while the last row is committing.
             * Retain its actual start time without blocking the stock runner. */
            h.has_next = 1;
            h.next_index = index;
            h.next_started = millis_now();
            h.next_aborted = h.next_closed = 0;
        } else
            record = 0;
    }
    uint8_t kind = record ? care_kind(index) : CARE_OTHER;
    stock_program(index);
    if (record) {
        if (h.has_next)
            h.next_closed = kind;
        else
            h.closing = kind;
        zymatic_history_poll(millis_now());
    }
}
uint8_t zymatic_maintenance(void)
{
    if (!h.enabled) return 0xff;
    /* Closing and the EEPROM final marker are asynchronous. The operator has
     * already left the stock runner, so the main menu can show the new notice. */
    return h.closing && h.phase != IDLE && h.phase != CLOSING && !h.aborted
        ? after_care(h.care, h.closing) : h.care;
}
uint32_t zymatic_history_latest(void) { return h.latest; }
uint8_t zymatic_history_ready(void) { return h.enabled && ready(); }
uint8_t zymatic_history_session(uint32_t id, ZymaticHistorySession *out)
{
    if (!id || !zymatic_history_ready() || !(h.valid & (1u << slot(id))))
        return 0;
    Header v;
    if (!read_at(meta_addr(slot(id)), &v, sizeof(v)))
        return 0;
    if (v.tag != META_TAG || !header_ok(&v)) {
        h.enabled = 0;
        return 0;
    }
    if (v.id != id)
        return 0;
    out->id = id;
    memcpy(out->name, v.name, sizeof(v.name));
    out->count = h.count[slot(id)];
    out->flags = 0;
    if (h.phase != IDLE && id == h.latest && v.state == 0)
        out->flags |= ZH_ACTIVE;
    if (v.state == COMPLETE || v.state == TRUNC || (v.state & 0xf0) == 0xd0)
        out->flags |= ZH_COMPLETE;
    if (v.state == ABORTED || v.state == ABORTED_TRUNC)
        out->flags |= ZH_ABORTED;
    if (v.state == TRUNC || v.state == ABORTED_TRUNC || (v.state & 0xf8) == 0xd8 || (id == h.latest && h.truncated))
        out->flags |= ZH_TRUNCATED;
    out->first_elapsed = out->duration = 0;
    if (out->count) {
        ZymaticHistoryRow row;
        if (!zymatic_history_row(id, 0, &row))
            return 0;
        out->first_elapsed = row.seconds;
        if (out->count > 1 && !zymatic_history_row(id, out->count - 1, &row))
            return 0;
        out->duration = row.seconds;
    }
    return 1;
}
uint8_t zymatic_history_row(uint32_t id, uint16_t offset, ZymaticHistoryRow *out)
{
    uint8_t i = slot(id);
    if (!id || !zymatic_history_ready() || !(h.valid & (1u << i)) || offset >= h.count[i])
        return 0;
    Sample r;
    if (!read_at(row_addr(plus(h.first[i], (uint8_t)offset)), &r, sizeof(r)))
        return 0;
    if (r.tag != ROW_TAG || !row_ok(&r)) {
        h.enabled = 0;
        return 0;
    }
    if (r.id != id) return 0; /* This slot may have been reused by a newer session. */
    out->seconds = r.seconds;
    out->wort = r.wort;
    out->heat = r.heat;
    out->target = r.target;
    out->location = r.location;
    out->step = r.step;
    return 1;
}
