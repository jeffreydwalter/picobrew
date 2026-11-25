/* Pico C reconstruction, adapted from the archived project S/Pro typed source.
 * Original C entry and data correspondence: analysis/function-correspondence.json.
 * Behavioral coverage is reported separately; no physical qualification is implied.
 */
#include "pico_c/recipe.h"
#include "pico_c/local_control.h"
#if PICO_STANDALONE
#include "pico_c/local_history.h"
#endif
#include <limits.h>
#include <stdio.h>

static uint32_t clock_ms(PicoRecipeRuntime *r)
{
    pico_local_poll();
    return r->phase->control->ops->millis(r->phase->control->context);
}
static void delay_ms(PicoRecipeRuntime *r, uint32_t milliseconds)
{
#if PICO_STANDALONE
    (void)r;
    pico_local_control_delay(milliseconds);
#else
    r->phase->actuators->ops->delay_milliseconds(r->phase->actuators->context, milliseconds);
#endif
}
static void show(PicoRecipeRuntime *r, PicoRecipeScreen screen, uint32_t argument, const char *text)
{
    r->ops->screen(r->context, screen, argument, text);
}
static int prompt(PicoRecipeRuntime *r, PicoRecipePrompt kind, uint32_t argument)
{
    return r->ops->prompt(r->context, kind, argument, NULL);
}
static void move_to(PicoRecipeRuntime *r, uint32_t compartment)
{
    r->phase->ops->move_to_compartment(r->phase->context, compartment);
}
static void center(PicoRecipeRuntime *r, uint16_t row, const char *text)
{
    r->ops->center_text(r->context, row, text);
}
static void timer(PicoRecipeRuntime *r, uint16_t row, uint32_t start, uint32_t seconds)
{
    r->ops->timer(r->context, row, start, seconds);
}
static void temperature_columns(PicoRecipeRuntime *r, uint16_t row)
{
    r->ops->temperature_columns(r->context, row, r->state->controller.wort_fahrenheit,
                                r->state->controller.thermoblock_fahrenheit, 0.0);
}
static int32_t trunc_i32(double value)
{
    if (value != value)
        return 0;
    if (value >= 2147483647.0)
        return INT32_MAX;
    if (value <= -2147483648.0)
        return INT32_MIN;
    return (int32_t)value;
}

/* C main 0x2000c550 / alt 0x2000c538. The return value is always zero, including a resumed
 * filter-removal pause; callers retain that behavior rather than inferring it. */
bool pico_recipe_handle_filter_removal(PicoRecipeRuntime *r)
{
    if (pico_local_cancelled())
        return false;
    if (!pico_filter_is_inserted(r->phase->actuators)) {
        pico_control_stop(r->phase->control);
        pico_actuators_steam_and_drain_off(r->phase->actuators);
        r->phase->actuators->ops->prompt_insert_filter(r->phase->actuators->context);
        pico_phase_set(r->phase, r->state->controller.phase);
    }
    return false;
}

/* Extracted from the C setup procedure at main 0x200102bc / alt 0x200102a4.
 * The OEM compiler inlined this phase; it has no separate C entry point. */
bool pico_recipe_prime_steam(PicoRecipeRuntime *r, const char *title)
{
    if (pico_local_cancelled())
        return false;
    PicoActuators *a = r->phase->actuators;
    if (!pico_filter_is_inserted(a)) {
        show(r, PICO_RECIPE_CLEAR, 0, NULL);
        r->phase->actuators->ops->prompt_insert_filter(r->phase->actuators->context);
    }
    show(r, PICO_RECIPE_CLEAR, 0, NULL);
    show(r, PICO_RECIPE_TITLED_FRAME, 0, title);
    center(r, 1, "Priming steam");
    show(r, PICO_RECIPE_FLUSH, 0, NULL);
    move_to(r, 2);
    r->state->controller.compartment = 2;
    pico_phase_start_circulation(r->phase);
    bool heated = r->ops->check_thermoblock_heating(r->context);
    if (heated) {
        show(r, PICO_RECIPE_CLEAR, 0, NULL);
        show(r, PICO_RECIPE_TITLED_FRAME, 0, title);
        center(r, 1, "Priming pump");
        show(r, PICO_RECIPE_FLUSH, 0, NULL);
        delay_ms(r, 10000);
        pico_actuator_steam_off(a);
        pico_actuator_drain_off(a);
    }
    return heated;
}

/* C main 0x200103a8 / alt 0x20010390. The nonzero calibration byte is returned
 * literally, not normalized, after a successful thermal check. */
uint32_t pico_recipe_reprime_steam(PicoRecipeRuntime *r, const char *title, const char *step,
                                   bool logging, bool restart_circulation)
{
    if (pico_local_cancelled())
        return 0;
    PicoActuators *a = r->phase->actuators;
    PicoBrewController *c = &r->state->controller;
    if (!pico_filter_is_inserted(a)) {
        show(r, PICO_RECIPE_CLEAR, 0, NULL);
        r->phase->actuators->ops->prompt_insert_filter(r->phase->actuators->context);
    }
    if (restart_circulation)
        pico_phase_start_circulation(r->phase);
    show(r, PICO_RECIPE_CLEAR, 0, NULL);
    show(r, PICO_RECIPE_TITLED_FRAME, 0, title);
    center(r, 1, "Priming pump");
    show(r, PICO_RECIPE_FLUSH, 0, NULL);
    uint32_t heated = r->ops->check_thermoblock_heating(r->context);
    if (!heated)
        return 0;
    uint32_t last_log = clock_ms(r);
    pico_actuator_shuttle_on(a);
    unsigned intervals = 16;
    while (c->thermoblock_fahrenheit > 230.0) {
        pico_local_poll();
        if (pico_local_cancelled())
            return 0;
        if (logging && clock_ms(r) - last_log >= 25001u) {
            r->ops->log_progress(r->context, step);
            last_log = clock_ms(r);
            if (--intervals == 0)
                break;
        }
    }
    pico_actuator_shuttle_off(a);
    if (restart_circulation) {
        pico_phase_start_circulation(r->phase);
        delay_ms(r, 5000);
    }
    show(r, PICO_RECIPE_CLEAR, 0, NULL);
    if (c->shuttle_calibration_complete)
        return c->shuttle_calibration_complete;
    c->active_shuttle_scale = 0.2;
    c->candidate_shuttle_scale = 0.2;
    return heated;
}

/* C main 0x2000c6c4 / alt 0x2000c6ac. */
void pico_recipe_restore_idle(PicoRecipeRuntime *r)
{
    if (pico_local_cancelled())
        return;
    show(r, PICO_RECIPE_ENDING_PROCESS, 0, NULL);
    r->ops->move_home(r->context);
}

/* C main 0x2000e200 / alt 0x2000e1e8. The old positional arguments
 * survive in callers, but the C implementation does not rotate the valve. */
void pico_recipe_vacuum(PicoRecipeRuntime *r, uint32_t compartment, uint32_t first)
{
    if (pico_local_cancelled())
        return;
    (void)compartment;
    (void)first;
    PicoActuators *a = r->phase->actuators;
    pico_actuators_all_off(a);
    bool accepted = prompt(r, PICO_RECIPE_VACUUM, 0) != 0;
    show(r, PICO_RECIPE_STOP_SCROLLING, 0, NULL);
    if (accepted) {
        pico_actuator_steam_on(a);
        prompt(r, PICO_RECIPE_VACUUM, 1);
        clock_ms(r);
        for (;;) {
            pico_local_poll();
            if (pico_local_cancelled())
                goto vacuum_finished;
            clock_ms(r);
            for (;;) {
                pico_local_poll();
                if (pico_local_cancelled())
                    goto vacuum_finished;
                if (r->ops->take_click(r->context))
                    goto vacuum_finished;
                clock_ms(r);
                if (pico_actuators_pause_for_missing_filter(a, true, false, false)) {
                    prompt(r, PICO_RECIPE_VACUUM, 1);
                    break;
                }
            }
        }
vacuum_finished:
        pico_actuator_steam_off(a);
        pico_actuator_drain_off(a);
    }
    show(r, PICO_RECIPE_CLEAR, 0, NULL);
    pico_recipe_restore_idle(r);
}

/* C main 0x2000c734 / alt 0x2000c71c. */
void pico_recipe_circulate(PicoRecipeRuntime *r)
{
    if (pico_local_cancelled())
        return;
    PicoActuators *a = r->phase->actuators;
    show(r, PICO_RECIPE_PROGRAM_NOTICE, 0, NULL);
    move_to(r, 2);
    pico_actuators_start_drain_and_steam(a);
    uint32_t next_update = 0, elapsed = 0;
    while (!r->ops->take_click(r->context)) {
        pico_local_poll();
        if (pico_local_cancelled())
            return;
        if (!pico_filter_is_inserted(a)) {
            pico_actuators_steam_and_drain_off(a);
            r->phase->actuators->ops->prompt_insert_filter(r->phase->actuators->context);
            pico_actuators_start_drain_and_steam(a);
        }
        uint32_t now = clock_ms(r);
        if (next_update < now) {
            r->ops->circulation_status(r->context, r->state->controller.wort_fahrenheit, elapsed);
            next_update = now + 1000;
            ++elapsed;
        }
    }
    pico_actuators_steam_and_drain_off(a);
    pico_recipe_restore_idle(r);
}

/* C main 0x2000c790 / alt 0x2000c778. */
void pico_recipe_drain_reservoir(PicoRecipeRuntime *r)
{
    if (pico_local_cancelled())
        return;
    PicoActuators *a = r->phase->actuators;
    show(r, PICO_RECIPE_CLEAR, 0, NULL);
    clock_ms(r);
    show(r, PICO_RECIPE_DRAIN_RESERVOIR, 0, NULL);
    show(r, PICO_RECIPE_FLUSH, 0, NULL);
    move_to(r, 2);
    pico_actuator_shuttle_on(a);
    delay_ms(r, 500);
    pico_actuator_steam_on(a);
    for (;;) {
        pico_local_poll();
        if (pico_local_cancelled())
            return;
        show(r, PICO_RECIPE_STOP_FOOTER, 0, NULL);
        show(r, PICO_RECIPE_FLUSH, 0, NULL);
        for (;;) {
            pico_local_poll();
            if (pico_local_cancelled())
                return;
            if (r->ops->take_click(r->context)) {
                pico_actuator_shuttle_off(a);
                delay_ms(r, 500);
                pico_actuator_steam_off(a);
                pico_recipe_restore_idle(r);
                return;
            }
            if (!pico_filter_is_inserted(a))
                break;
        }
        pico_actuator_shuttle_off(a);
        pico_actuator_steam_off(a);
        r->phase->actuators->ops->prompt_insert_filter(r->phase->actuators->context);
        pico_actuator_steam_on(a);
        delay_ms(r, 500);
        pico_actuator_shuttle_on(a);
        show(r, PICO_RECIPE_CLEAR, 0, NULL);
        show(r, PICO_RECIPE_DRAIN_RESERVOIR, 0, NULL);
    }
}

/* C main 0x2000c948 / alt 0x2000c930. completed_cycles deliberately retains byte wraparound. */
void pico_recipe_flush_compartments(PicoRecipeRuntime *r, uint32_t minimum_cycles, bool automatic)
{
    if (pico_local_cancelled())
        return;
    PicoActuators *a = r->phase->actuators;
    show(r, PICO_RECIPE_CLEAR, 0, NULL);
    show(r, PICO_RECIPE_BUILTIN_FONT, 0, NULL);
    clock_ms(r);
    show(r, PICO_RECIPE_HEADER, 0, "TEST ROTARY VALVE");
    show(r, PICO_RECIPE_FLUSH, 0, NULL);
    pico_actuator_drain_on(a);
    move_to(r, 0);
    uint8_t completed = 0;
    for (;;) {
        pico_local_poll();
        if (pico_local_cancelled())
            return;
        if (minimum_cycles <= completed) {
            if (automatic)
                break;
            show(r, PICO_RECIPE_CONTINUE_HINT, 0, NULL);
            show(r, PICO_RECIPE_FLUSH, 0, NULL);
        }
        for (uint32_t compartment = 1; compartment != 7; ++compartment) {
            move_to(r, compartment);
            uint32_t started = clock_ms(r);
            r->ops->valve_position(r->context, 1, compartment);
            center(r, 2, "(Keg Pump Active)");
            show(r, PICO_RECIPE_FLUSH, 0, NULL);
            pico_actuator_steam_on(a);
            while (clock_ms(r) - started < 3000) {
                pico_local_poll();
                if (pico_local_cancelled())
                    return;
                if (r->ops->take_click(r->context) && minimum_cycles <= completed)
                    goto finished;
            }
            pico_actuator_steam_off(a);
            r->ops->clear_row(r->context, 2, 0);
            show(r, PICO_RECIPE_FLUSH, 0, NULL);
        }
        ++completed;
    }
finished:
    show(r, PICO_RECIPE_CLEAR, 0, NULL);
    show(r, PICO_RECIPE_CUSTOM_FONT, 0, NULL);
    pico_actuators_steam_and_drain_off(a);
    pico_recipe_restore_idle(r);
}

/* C main 0x2000caa4 / alt 0x2000ca8c. */
void pico_recipe_flush_until_press(PicoRecipeRuntime *r)
{
    if (pico_local_cancelled())
        return;
    pico_recipe_flush_compartments(r, 0, false);
}

/* C main 0x2000ccc0 / alt 0x2000cca8. r1 was padding before the AAPCS double. */
void pico_recipe_heat_flush_compartments(PicoRecipeRuntime *r, bool steam, double target)
{
    if (pico_local_cancelled())
        return;
    PicoActuators *a = r->phase->actuators;
    PicoBrewController *c = &r->state->controller;
    pico_actuator_drain_on(a);
    show(r, PICO_RECIPE_BUILTIN_FONT, 0, NULL);
    for (uint32_t compartment = 1; compartment != 7; ++compartment) {
        show(r, PICO_RECIPE_CLEAR, 0, NULL);
        show(r, PICO_RECIPE_HEADER, 0, "RV Pressure Test");
        center(r, 1, "Moving Squid");
        show(r, PICO_RECIPE_FLUSH, 0, NULL);
        move_to(r, compartment);
        r->ops->valve_position(r->context, 3, compartment);
        r->ops->clear_row(r->context, 1, 0);
        char label[40];
        int32_t integer_target = trunc_i32(target);
        snprintf(label, sizeof label, "Heating to%ld",
                 (long)trunc_i32((double)integer_target - 20.0));
        center(r, 1, label);
        uint32_t started = clock_ms(r);
        double preheat = target - 20.0;
        if (c->thermoblock_fahrenheit < preheat) {
            pico_actuator_heater_on(a);
            while (c->thermoblock_fahrenheit < preheat) {
                pico_local_poll();
                if (pico_local_cancelled())
                    return;
                if (clock_ms(r) - started > 15000 && c->thermoblock_fahrenheit < 90.0) {
                    pico_actuators_off_except_drain(a);
                    r->ops->error(r->context, 19, false, false);
                    show(r, PICO_RECIPE_CUSTOM_FONT, 0, NULL);
                    return;
                }
                temperature_columns(r, 2);
                show(r, PICO_RECIPE_FLUSH, 0, NULL);
            }
            pico_actuator_heater_off(a);
        }
        r->ops->clear_row(r->context, 1, 0);
        snprintf(label, sizeof label, "Heating to%ld", (long)integer_target);
        center(r, 1, label);
        started = clock_ms(r);
        while (c->thermoblock_fahrenheit < target && clock_ms(r) - started < 5000) {
            pico_local_poll();
            if (pico_local_cancelled())
                return;
            temperature_columns(r, 2);
            show(r, PICO_RECIPE_FLUSH, 0, NULL);
        }
        r->ops->clear_row(r->context, 1, 0);
        center(r, 1, "SHUTTLE ON");
        if (steam)
            pico_actuator_steam_on(a);
        pico_actuator_shuttle_on(a);
        clock_ms(r);
        double threshold = target;
        for (unsigned stage = 0; stage != 4; ++stage) {
            clock_ms(r);
            while (c->thermoblock_fahrenheit > threshold) {
                pico_local_poll();
                if (pico_local_cancelled())
                    return;
                temperature_columns(r, 2);
                show(r, PICO_RECIPE_FLUSH, 0, NULL);
            }
            clock_ms(r);
            threshold = target - (double)((stage + 1) * 10);
        }
        pico_actuator_shuttle_off(a);
        pico_actuator_steam_off(a);
    }
    pico_actuator_drain_off(a);
    show(r, PICO_RECIPE_CLEAR, 0, NULL);
    show(r, PICO_RECIPE_CUSTOM_FONT, 0, NULL);
    pico_recipe_restore_idle(r);
}

/* C main 0x2000dfb8 / alt 0x2000dfa0. This procedure has no S/Pro
 * counterpart. It performs a shuttle-only sweep, heats the thermoblock,
 * makes a short heated sweep, then waits with the shuttle running. */
void pico_recipe_c_drain_sequence(PicoRecipeRuntime *r, uint8_t *next, uint32_t first)
{
    if (pico_local_cancelled())
        return;
    PicoActuators *a = r->phase->actuators;
    PicoBrewController *c = &r->state->controller;
    pico_actuators_all_off(a);
    uint32_t last_move = clock_ms(r);
    uint32_t started = clock_ms(r);
    pico_actuator_shuttle_on(a);
    prompt(r, PICO_RECIPE_VACUUM, 2);
    for (;;) {
        pico_local_poll();
        if (pico_local_cancelled())
            return;
        uint32_t before_pause = clock_ms(r);
        if (pico_actuators_pause_for_missing_filter(a, false, false, true)) {
            prompt(r, PICO_RECIPE_VACUUM, 2);
            last_move = last_move - before_pause + clock_ms(r);
            started = started - before_pause + clock_ms(r);
        }
        if (clock_ms(r) - last_move > 20000u) {
            pico_actuator_shuttle_off(a);
            move_to(r, *next);
            pico_actuator_shuttle_on(a);
            last_move = clock_ms(r);
            *next = (uint8_t)(*next + 1u);
            if (*next > 6u)
                break;
        }
        timer(r, 5, started, 900);
    }
    *next = (uint8_t)first;
    pico_actuator_shuttle_off(a);
    show(r, PICO_RECIPE_CLEAR, 0, NULL);
    prompt(r, PICO_RECIPE_VACUUM, 2);
    uint8_t compartment = *next;
    *next = (uint8_t)(*next + 1u);
    move_to(r, compartment);
    pico_actuator_heater_on(a);
    uint32_t heat_started = clock_ms(r);
    while (c->thermoblock_fahrenheit < 210.0) {
        pico_local_poll();
        if (pico_local_cancelled())
            return;
        if (clock_ms(r) - heat_started > 15000u && c->thermoblock_fahrenheit < 90.0) {
            pico_actuators_off_except_drain(a);
            r->ops->error(r->context, 19, false, false);
            show(r, PICO_RECIPE_CUSTOM_FONT, 0, NULL);
            return;
        }
        timer(r, 5, started, 900);
    }
    started = clock_ms(r);
    pico_actuator_shuttle_on(a);
    last_move = started;
    while (*next < 7u) {
        pico_local_poll();
        if (pico_local_cancelled())
            return;
        double temperature = c->thermoblock_fahrenheit;
        if (temperature > 230.0)
            pico_actuator_heater_off(a);
        else if (temperature < 220.0)
            pico_actuator_heater_on(a);
        if (clock_ms(r) - last_move > 2000u) {
            pico_actuator_heater_off(a);
            pico_actuator_shuttle_off(a);
            compartment = *next;
            *next = (uint8_t)(*next + 1u);
            move_to(r, compartment);
            pico_actuator_shuttle_on(a);
            last_move = clock_ms(r);
        }
        timer(r, 5, started, 720);
    }
    pico_actuator_shuttle_off(a);
    pico_actuator_heater_off(a);
    *next = (uint8_t)first;
    move_to(r, first);
    prompt(r, PICO_RECIPE_VACUUM, 1);
    uint32_t last_page = clock_ms(r);
    pico_actuator_shuttle_on(a);
    bool timer_page = false;
    started = last_page;
    while (clock_ms(r) - started <= 599999u && !r->ops->take_click(r->context)) {
        pico_local_poll();
        if (pico_local_cancelled())
            return;
        uint32_t before_pause = clock_ms(r);
        if (pico_actuators_pause_for_missing_filter(a, false, false, true)) {
            prompt(r, PICO_RECIPE_VACUUM, 1);
            started = started - before_pause + clock_ms(r);
            last_page = last_page - before_pause + clock_ms(r);
        }
        if (clock_ms(r) - last_page >= 15001u) {
            last_page = clock_ms(r);
            bool previous = timer_page;
            timer_page = !timer_page;
            if (previous)
                prompt(r, PICO_RECIPE_VACUUM, 1);
        }
        if (timer_page) {
            timer(r, 0, started, 600);
            show(r, PICO_RECIPE_DONE_FOOTER, 0, NULL);
        }
    }
    pico_actuator_shuttle_off(a);
}

/* C main 0x2000e270 / alt 0x2000e258. The displayed 1200-second countdown
 * intentionally retains the OEM mismatch with its 120-second steam loop. */
void pico_recipe_rinse(PicoRecipeRuntime *r)
{
#if PICO_STANDALONE
    /* Only explicit stand-alone rinses are group starts. The automatic
     * post-brew rinse and cancelled preparation never invite another brewer. */
    bool group = r->state->group_armed;
    r->state->group_armed = false;
#endif
    if (pico_local_cancelled())
        return;
    static const uint32_t instructions[] = {21, 13};
    PicoActuators *a = r->phase->actuators;
    show(r, PICO_RECIPE_CLEAR, 0, NULL);
    r->state->device_state = 6;
    uint32_t first = *r->legacy_compartment;
    const char *title = r->ops->title_label(r->context, 1, *r->language);
    if (r->ops->instruction_sequence(r->context, instructions, 2, 0, 0, title, false) != 23) {
        r->ops->log(r->context, "W", "RCTL", "Rinse setup aborted by user");
        r->state->device_state = 2;
        return;
    }
    show(r, PICO_RECIPE_WAIT_FILTER_INSERTION, 0, NULL);
    show(r, PICO_RECIPE_INSTRUCTION_HEADER, 23, title);
    r->ops->report_state(r->context);
#if PICO_STANDALONE
    uint32_t rinse_start = clock_ms(r);
    if (group) {
        r->state->group_rinse = true;
        r->state->group_started_ms = rinse_start;
    }
#else
    clock_ms(r);
#endif
    uint8_t next = 1;
    move_to(r, next);
    show(r, PICO_RECIPE_CLEAR, 0, NULL);
    show(r, PICO_RECIPE_INSTRUCTION_HEADER, 23, title);
    pico_actuator_steam_on(a);
    uint32_t started = clock_ms(r);
    while (clock_ms(r) - started <= 119999u) {
        pico_local_poll();
        if (pico_local_cancelled())
            return;
        uint32_t now = clock_ms(r);
        if (pico_actuators_pause_for_missing_filter(a, true, false, false)) {
            show(r, PICO_RECIPE_INSTRUCTION_HEADER, 23, title);
            started = started - now + clock_ms(r);
        }
        timer(r, 5, started, 1200);
    }
    pico_actuator_drain_off(a);
    pico_actuator_steam_off(a);
    pico_recipe_c_drain_sequence(r, &next, first);
    pico_recipe_vacuum(r, next, first);
    r->state->device_state = 2;
    r->ops->report_state(r->context);
#if PICO_STANDALONE
    if (!pico_local_cancelled()) pico_care_completed(PICO_CARE_RINSE);
#endif
    r->ops->instruction_screen(r->context, 24, 0, 0, title, false);
}

/* C main 0x2000c5dc / alt 0x2000c5c4. Disassembly confirms r3 points at the caller's int32
 * next-compartment counter; the first three original register values are unused. */
bool pico_recipe_first_rinse_cool(PicoRecipeRuntime *r, int32_t *next_compartment)
{
    if (pico_local_cancelled())
        return false;
    PicoActuators *a = r->phase->actuators;
    double initial = r->state->controller.thermoblock_fahrenheit;
    show(r, PICO_RECIPE_FIRST_RINSE_STEP, 5, NULL);
    pico_actuator_steam_off(a);
    pico_actuator_shuttle_on(a);
    pico_actuator_drain_on(a);
    uint32_t started = clock_ms(r), last_move = started;
    while (clock_ms(r) - started < 120000) {
        pico_local_poll();
        if (pico_local_cancelled())
            return false;
        uint32_t now = clock_ms(r);
        if (pico_actuators_pause_for_missing_filter(a, false, true, true)) {
            show(r, PICO_RECIPE_FIRST_RINSE_STEP, 5, NULL);
            started = started - now + clock_ms(r);
            last_move = last_move - now + clock_ms(r);
        }
        if (clock_ms(r) - last_move > 10000) {
            move_to(r, (uint8_t)*next_compartment);
            last_move = clock_ms(r);
            int32_t next = *next_compartment + 1;
            *next_compartment = next > 6 ? 1 : next;
            timer(r, 6, started, 120);
        }
        timer(r, 6, started, 120);
    }
    return r->state->controller.thermoblock_fahrenheit < initial - 85.0;
}

/* C main 0x20009f98 / alt 0x20009f80. */
bool pico_recipe_first_rinse_heat(PicoRecipeRuntime *r)
{
    if (pico_local_cancelled())
        return false;
    PicoActuators *a = r->phase->actuators;
    PicoBrewController *c = &r->state->controller;
    clock_ms(r);
    pico_actuator_heater_on(a);
    show(r, PICO_RECIPE_FLUSH, 0, NULL);
    uint32_t started = clock_ms(r);
    bool success;
    for (;;) {
        pico_local_poll();
        if (pico_local_cancelled())
            return false;
        if (!(c->thermoblock_fahrenheit < 230.0)) {
            success = true;
            break;
        }
        if (clock_ms(r) - started > 30000 && c->thermoblock_fahrenheit < 130.0) {
            success = false;
            break;
        }
        timer(r, 6, started, 0);
    }
    pico_actuator_heater_off(a);
    started = clock_ms(r);
    show(r, PICO_RECIPE_FIRST_RINSE_STEP, 3, NULL);
    while (!pico_local_cancelled() && clock_ms(r) - started < 30000)
        timer(r, 6, started, 30);
    return success;
}

/* C main 0x2000e6cc / alt 0x2000e6b4. */
void pico_recipe_drain_filter(PicoRecipeRuntime *r)
{
    if (pico_local_cancelled())
        return;
    show(r, PICO_RECIPE_CLEAR, 0, NULL);
    show(r, PICO_RECIPE_DRAIN_FILTER, 0, NULL);
    pico_actuator_drain_on(r->phase->actuators);
    show(r, PICO_RECIPE_WAIT_STOP, 0, NULL);
    pico_actuator_drain_off(r->phase->actuators);
}

/* C main 0x2000e6e8 / alt 0x2000e6d0. */
void pico_recipe_first_rinse(PicoRecipeRuntime *r)
{
    if (pico_local_cancelled())
        return;
    PicoActuators *a = r->phase->actuators;
    PicoBrewController *c = &r->state->controller;
    show(r, PICO_RECIPE_CLEAR, 0, NULL);
    show(r, PICO_RECIPE_FIRST_RINSE_STEP, 0, NULL);
    if (!pico_filter_is_inserted(a)) {
        show(r, PICO_RECIPE_FIRST_RINSE_STEP, 1, NULL);
        r->ops->wait_for_filter(r->context);
    }
    show(r, PICO_RECIPE_FIRST_RINSE_STEP, 10, NULL);
    show(r, PICO_RECIPE_FIRST_RINSE_STEP, 2, NULL);
    r->state->device_state = 6;
    r->ops->report_state(r->context);
    int32_t next = 2;
    move_to(r, 1);
    bool heat_ok = pico_recipe_first_rinse_heat(r);
    show(r, PICO_RECIPE_FIRST_RINSE_STEP, 4, NULL);
    pico_actuator_steam_on(a);
    pico_actuator_shuttle_on(a);
    pico_actuator_drain_on(a);
    show(r, PICO_RECIPE_FLUSH, 0, NULL);
    uint32_t started = clock_ms(r), last_move = started;
    while (clock_ms(r) - started < 130000) {
        pico_local_poll();
        if (pico_local_cancelled())
            return;
        uint32_t now = clock_ms(r);
        if (pico_actuators_pause_for_missing_filter(a, true, false, true)) {
            show(r, PICO_RECIPE_FIRST_RINSE_STEP, 4, NULL);
            started = started - now + clock_ms(r);
            last_move = last_move - now + clock_ms(r);
        }
        if (c->thermoblock_fahrenheit < 225.0)
            pico_actuator_heater_on(a);
        else if (c->thermoblock_fahrenheit > 230.0)
            pico_actuator_heater_off(a);
        if (clock_ms(r) - last_move > 10000) {
            pico_actuator_heater_off(a);
            pico_actuator_steam_off(a);
            pico_actuator_shuttle_off(a);
            move_to(r, (uint8_t)next);
            pico_actuator_steam_on(a);
            pico_actuator_shuttle_on(a);
            last_move = clock_ms(r);
            if (++next > 6)
                next = 1;
            timer(r, 6, started, 130);
        }
        r->ops->temperature_summary(r->context, c->wort_fahrenheit, c->thermoblock_fahrenheit, 0.0,
                                    5);
        timer(r, 6, started, 130);
    }
    pico_actuator_heater_off(a);
    bool cool_ok = pico_recipe_first_rinse_cool(r, &next);
    if (!cool_ok) {
        pico_actuator_shuttle_off(a);
        pico_actuator_steam_off(a);
        pico_actuator_drain_off(a);
        pico_recipe_first_rinse_heat(r);
        cool_ok = pico_recipe_first_rinse_cool(r, &next);
    }
    show(r, PICO_RECIPE_ENDING_FIRST_RINSE, 0, NULL);
    pico_actuator_shuttle_off(a);
    pico_actuator_drain_off(a);
    pico_recipe_vacuum(r, (uint8_t)next, 1);
    if (!cool_ok) {
        show(r, PICO_RECIPE_FIRST_RINSE_STEP, 6, NULL);
        r->ops->report_error(r->context, 22);
        if (!heat_ok)
            r->ops->report_error(r->context, 19);
        show(r, PICO_RECIPE_WAIT_STOP, 0, NULL);
        show(r, PICO_RECIPE_FIRST_RINSE_STEP, 9, NULL);
    }
    if (!heat_ok) {
        show(r, PICO_RECIPE_FIRST_RINSE_STEP, 7, NULL);
        r->ops->report_error(r->context, 19);
        show(r, PICO_RECIPE_WAIT_STOP, 0, NULL);
        show(r, PICO_RECIPE_FIRST_RINSE_STEP, 9, NULL);
    }
    if (!cool_ok || !heat_ok)
        show(r, PICO_RECIPE_WAIT_CLICK, 0, NULL);
    show(r, PICO_RECIPE_FLUSH, 0, NULL);
    pico_recipe_restore_idle(r);
    show(r, PICO_RECIPE_FIRST_RINSE_STEP, 8, NULL);
    show(r, PICO_RECIPE_FLUSH, 0, NULL);
    r->state->device_state = 2;
    r->ops->report_state(r->context);
    show(r, PICO_RECIPE_WAIT_STOP, 0, NULL);
    show(r, PICO_RECIPE_STOP_SCROLLING, 0, NULL);
    r->ops->save_first_rinse_complete(r->context);
}

/* C main 0x2000c21c / alt 0x2000c204. */
void pico_recipe_rack_beer(PicoRecipeRuntime *r)
{
    if (pico_local_cancelled())
        return;
    PicoActuators *a = r->phase->actuators;
    show(r, PICO_RECIPE_CLEAR, 0, NULL);
    show(r, PICO_RECIPE_RACK_BEER_STEP, 0, NULL);
    pico_actuator_drain_on(a);
    show(r, PICO_RECIPE_RACK_BEER_STEP, 1, NULL);
    pico_actuator_drain_off(a);
    for (uint32_t stage = 2; stage != 6; ++stage)
        show(r, PICO_RECIPE_RACK_BEER_STEP, stage, NULL);
    pico_actuator_drain_on(a);
    show(r, PICO_RECIPE_RACK_BEER_STEP, 6, NULL);
    pico_actuator_drain_off(a);
    show(r, PICO_RECIPE_RACK_BEER_STEP, 7, NULL);
    pico_actuator_drain_on(a);
    show(r, PICO_RECIPE_RACK_BEER_STEP, 8, NULL);
    pico_actuator_drain_off(a);
}

/* C main 0x20010488 / alt 0x20010470. */
void pico_recipe_descale(PicoRecipeRuntime *r)
{
    if (pico_local_cancelled())
        return;
    PicoActuators *a = r->phase->actuators;
    PicoBrewController *c = &r->state->controller;
    prompt(r, PICO_RECIPE_DESCALE_STEP, 0);
    prompt(r, PICO_RECIPE_DESCALE_STEP, 7);
    while (!pico_local_cancelled() && !pico_filter_is_inserted(a))
        prompt(r, PICO_RECIPE_DESCALE_STEP, 1);
    prompt(r, PICO_RECIPE_DESCALE_STEP, 6);
    move_to(r, 1);
    pico_recipe_reprime_steam(r, "PRIME", "Descaling", false, false);
    pico_actuators_all_off(a);
    prompt(r, PICO_RECIPE_DESCALE_STEP, 2);
    for (unsigned cycle = 0; cycle != 2; ++cycle) {
        clock_ms(r);
        prompt(r, PICO_RECIPE_DESCALE_STEP, 3);
        for (;;) {
            pico_local_poll();
            if (pico_local_cancelled())
                return;
            pico_actuator_shuttle_on(a);
            if (!pico_filter_is_inserted(a)) {
                clock_ms(r);
                pico_actuator_shuttle_off(a);
                pico_actuator_heater_off(a);
                while (!pico_local_cancelled() && !pico_filter_is_inserted(a))
                    prompt(r, PICO_RECIPE_DESCALE_STEP, 1);
                prompt(r, PICO_RECIPE_DESCALE_STEP, 3);
                clock_ms(r);
            }
            temperature_columns(r, 0);
            show(r, PICO_RECIPE_FLUSH, 0, NULL);
            if (c->thermoblock_fahrenheit > 130.0)
                pico_actuator_heater_off(a);
            else if (c->thermoblock_fahrenheit < 120.0)
                pico_actuator_heater_on(a);
            if (!r->ops->take_click(r->context))
                continue;
            clock_ms(r);
            pico_actuator_shuttle_off(a);
            pico_actuator_heater_off(a);
            if (prompt(r, PICO_RECIPE_DESCALE_STEP, 4))
                break;
            prompt(r, PICO_RECIPE_DESCALE_STEP, 3);
            clock_ms(r);
        }
        clock_ms(r);
        pico_actuators_all_off(a);
        clock_ms(r);
        if (cycle == 0)
            prompt(r, PICO_RECIPE_DESCALE_STEP, 5);
    }
    pico_recipe_restore_idle(r);
    pico_actuators_all_off(a);
    r->state->device_state = 2;
    r->ops->report_state(r->context);
#if PICO_STANDALONE
    if (!pico_local_cancelled()) pico_care_completed(PICO_CARE_DESCALE);
#endif
}
