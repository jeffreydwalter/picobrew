/* C menu headings, footers and prompt composition. These intentionally use
 * the C two-row/font/scrolling contracts instead of the S/Pro framed layout. */
#include "ui_internal.h"
#include "error_text.inc"

void pico_ui_draw_error(int code)
{
    char text[204];
    unsigned index = (unsigned)code < 20 ? (unsigned)code : code == 99 ? 20 : 0;
    unsigned language = pico_ui_active->network->settings->language;
    if (language > 2)
        language = 0;
    pico_display_clear();
    snprintf(text, sizeof text, error_text[index][language], code);
    pico_display_start_scrolling(text, NULL);
}

void pico_ui_draw_subheader(const char *text) { pico_display_center_text(0, text); }
void pico_ui_draw_header(const char *text) { pico_display_center_text(0, text); }
void pico_ui_draw_header_at_row(const char *text, short row) { pico_display_center_text(row, text); }

void pico_label_first_rinse(char *text)
{ strcpy(text, ui_language_text("FIRST RINSE: ", "PRIMER LAVADO: ", "PREMIER RINCER: ")); }
void pico_label_rack_beer(char *text)
{ strcpy(text, ui_language_text("RACK BEER: ", "ESTANTE CERVEZA: ", "TRANSFERT BIERE: ")); }
void pico_label_factory_reset(char *text)
{ strcpy(text, ui_language_text("FACTORY RESET: ", "REINICIO DE LA PLANTA: ", "REINITIALISATION: ")); }
void pico_label_descale(char *text) { strcpy(text, "DESCALE"); }

void pico_ui_draw_ok_footer(void)
{
    char text[32];
    pico_label_ok(text);
    pico_ui_draw_footer_single(text);
}
void pico_ui_draw_start_flush_footer(void)
{ pico_ui_draw_footer_single(ui_language_text("Start Flush", "Comience", "Demarrer")); }
void pico_ui_draw_start_transfer_footer(void)
{ pico_ui_draw_footer_single(ui_language_text("Start Transfer", "Iniciar proceso", "Debut")); }
void pico_ui_draw_start_rinse_footer(void)
{ pico_ui_draw_footer_single(ui_language_text("Start Rinse", "Comience lavar", "Commencer")); }
void pico_ui_draw_start_clean_footer(void)
{ pico_ui_draw_footer_single(ui_language_text("Start Clean", "Comience a limpiar", "Commencer")); }

void pico_ui_draw_stop_footer(void)
{
    char text[32];
    pico_label_done(text);
    pico_ui_draw_footer_single(text);
}

void pico_ui_draw_done_footer(void)
{
    char text[32];
    pico_label_done(text);
    pico_ui_draw_footer_single(text);
}

void pico_ui_draw_confirm_footer(void)
{
    char text[32];
    pico_label_confirm(text);
    pico_ui_draw_footer_single(text);
}

void pico_ui_draw_continue_footer(void)
{
    char text[32];
    pico_label_continue(text);
    pico_ui_draw_footer_single(text);
}

void pico_ui_draw_heater_malfunction(void)
{
    pico_display_start_scrolling("HEATER MALFUNCTION: ERROR CODE 24 UNPLUG PICO IMMEDIATELY. CHECK HEATER BEFORE RESTART.", NULL);
    ui_interrupts_enable();
}

void pico_ui_draw_language_menu(void)
{
    pico_ui_draw_subheader(ui_language_text("SET LANGUAGE", "CONFIGURE IDIOMA", "CHOISIR LANGUE"));
}

uint32_t pico_ui_draw_units_menu(void)
{
    pico_display_clear_row(1);
    strcpy(pico_ui_active->menu_labels[0], "Imperial");
    strcpy(pico_ui_active->menu_labels[1], ui_language_text("Metric", "Metrico", "Metrique"));
    pico_ui_draw_subheader(ui_language_text("SET UNITS", "UNIDADES", "L'UNITE"));
    return 2;
}

void pico_ui_draw_drain_filter(void)
{
    pico_ui_draw_subheader(ui_language_text("DRAIN STEP FILTER", "FILTRO DEL DRENAJE", "VIDANGE DU FILTRE"));
}

void pico_ui_draw_drain_reservoir(void)
{
    pico_ui_draw_subheader(ui_language_text("DRAIN RESERVOIR", "DRENE RESERVORIO", "VIDANGER RESERVOIR"));
}

void pico_ui_draw_choose_network(void)
{
    pico_ui_draw_subheader(ui_language_text("CHOOSE NETWORK", "ELIJA LA RED", "CHOISIR UN RESEAU"));
}

void pico_ui_draw_wifi_test_heading(void)
{
    pico_ui_draw_subheader(ui_language_text("TEST WIFI", "VERIFIQUE EL WIFI", "TEST SANS FIL"));
}

void pico_ui_draw_utilities_heading(void)
{
    pico_ui_draw_subheader(ui_language_text("UTILITIES", "HERRAMIENTAS", "SERVICES"));
}

void pico_ui_draw_settings_heading(void)
{
    pico_ui_draw_subheader(ui_language_text("SETTINGS", "CONFIGURACION", "PARAMETRES"));
}

void pico_ui_draw_network_heading(void)
{
    pico_ui_draw_subheader(ui_language_text("NETWORK", "RED", "RESEAU"));
}

void pico_ui_draw_password_heading(void)
{
    pico_display_clear();
    pico_display_center_text(0, ui_language_text("ENTER PASSWORD", "CONTRASENA", "MOT DE PASSE"));
}

void pico_ui_draw_firmware_reset_question(void)
{
    char text[156], title[64];
    pico_label_factory_reset(title);
    snprintf(text, sizeof text, "%s%s", title, ui_language_text("Reset Factory firmware?",
        "Reiniciar Pico al firmware de la Planta?", "Reinitialiser au micrologiciel de Pico par defaut?"));
    pico_display_start_scrolling(text, NULL);
}

void pico_ui_draw_clear_wifi_question(void)
{
    /* French OEM text is 82 bytes including NUL, exceeding its 76-byte
     * stack buffer and replacing the saved return address. This bounded
     * reconstruction keeps all text and returns normally. */
    char text[156], title[64];
    pico_label_factory_reset(title);
    snprintf(text, sizeof text, "%s%s", title, ui_language_text("Remove stored WiFi credentials?",
        "Retirar las credenciales de Wi-Fi almacenadas?",
        "Supprimer les informations d'identification sans fil archivees?"));
    pico_display_start_scrolling(text, NULL);
}

void pico_ui_draw_skip_step_confirmation(void)
{
    pico_display_clear();
    pico_display_start_scrolling(ui_language_text("Are you sure you want to skip current step?",
        "Esta seguro de que desea saltarse el paso actual?",
        "Etes-vous certain de vouloir sauter cette etape?"), NULL);
}

void pico_ui_draw_stop_confirmation(void)
{
    char text[156];
    pico_display_clear();
    snprintf(text, sizeof text, "%s%s?", ui_language_text("Are you sure you want to stop ",
        "Esta seguro para detener: ", "Etes-vous certain de vouloir arreter"),
        pico_ui_active->app->recipe.program->state_label);
    pico_display_start_scrolling(text, NULL);
}

void pico_ui_draw_insert_picopak(void)
{
    pico_display_clear();
    pico_display_center_text(1, ui_language_text("Insert PicoPak", "Inserte PicoPak", "Inserer PicoPak"));
    pico_display_center_text(0, ui_language_text("BREW", "CERVEZA", "BRASSER"));
}

void pico_ui_draw_abv_adjustment(void)
{
    pico_display_center_text(0, ui_language_text("ADJUST ABV", "AJUSTE EL ABV", "AJUSTEZ LE ABV"));
    pico_display_text_at_cell(1, 0, " <              > ");
}

void pico_ui_draw_bitterness_adjustment(void)
{
    pico_display_center_text(0, ui_language_text("ADJUST IBU", "AJUSTAR IBU", "AJUSTER IBU"));
    pico_display_text_at_cell(1, 0, " <              > ");
}

void pico_ui_draw_picopak_selection(void)
{
#if PICO_STANDALONE
    pico_display_center_text(0, ui_language_text("Select recipe", "Elegir receta", "Choisir recette"));
#else
    pico_display_center_text(0, ui_language_text("Select PicoPak", "Seleccione PicoPak", "Choisir le PicoPak"));
#endif
}

void pico_ui_draw_registration_code(const char *code)
{
    if (pico_ui_active->scrolling.enabled)
        return;
    pico_display_clear();
    (void)code;
    pico_display_start_scrolling(ui_language_text("LOCAL CONTROL: Connect to WiFi and open this Pico's local IP in your browser.",
        "CONTROL LOCAL: Conecte WiFi y abra la IP local del Pico en su navegador.",
        "CONTROLE LOCAL: Connectez le WiFi et ouvrez l'adresse IP locale du Pico."), NULL);
}

void pico_ui_draw_brew_complete(const char *title)
{
    char text[156];
    pico_display_clear();
    snprintf(text, sizeof text, "%s BREWING COMPLETE: Disconnect hoses, place fermentation seal, and remove cozy. Cool keg to 68 F then pitch yeast.", title);
    pico_display_start_scrolling(text, pico_ui_draw_stop_footer);
}

int pico_ui_draw_recipe_choices(const char *title, int adjustable)
{
    pico_label_start_brewing(pico_ui_active->menu_labels[0]);
    unsigned count = 1;
    if (adjustable)
        pico_label_adjust_recipe(pico_ui_active->menu_labels[count++]);
    pico_label_cancel(pico_ui_active->menu_labels[count++]);
    pico_display_center_text(0, title);
    return count;
}

void pico_ui_draw_paused(void)
{
    char label[32];
    pico_display_clear();
    pico_display_center_text(0, ui_language_text("PAUSED", "PAUSADO", "EN PAUSE"));
    if (pico_filter_is_inserted(pico_ui_active->actuators))
        pico_label_continue(label);
    else
        pico_label_insert_step_filter(label);
    pico_ui_draw_footer_single(label);
    pico_display_flush();
}

void pico_ui_draw_current_program_heading(void)
{
    pico_display_clear();
    strcpy(pico_ui_active->menu_labels[0], ui_language_text("Resume", "Curriculum", "CV"));
    pico_label_exit(pico_ui_active->menu_labels[1]);
    pico_ui_draw_subheader(pico_ui_active->app->session.interrupted_text);
}

/* These C prompts use the scrolling line; no S/Pro frame is drawn. */
void pico_ui_draw_deep_clean_confirmation(void)
{
    pico_display_start_scrolling("CLEAN ALERT: Pico Needs to be Deep Cleaned. Run Deep Clean Now?", NULL);
}

void pico_ui_draw_descale_preparation(void)
{
    pico_display_start_scrolling("DESCALE ALERT: If you DO NOT use distilled water in reservoir Pico needs to be descaled. Run Descale Now?", NULL);
}

void pico_ui_draw_descale_rinse_preparation(void)
{
    pico_display_start_scrolling("Use distilled water in the reservoir. Run Descale when maintenance is due.", NULL);
}

void pico_ui_draw_descale_heading(void)
{
    pico_display_center_text(0, "DESCALE");
}

/* Main 0x2000e63c: a one-item menu, distinct from the direct footer wait. */
void pico_ui_wait_for_label_choice(const char *label)
{
    ui_copy(pico_ui_active->menu_labels[0], PICO_MENU_LABEL_LENGTH, label);
    pico_menu_choose(7, 1, 0);
}

void pico_ui_show_wifi_test_result(uint32_t stage)
{
    const char *line;
    pico_display_clear();
    pico_ui_draw_wifi_test_heading();
    switch (stage) {
    case 0:
        line = ui_language_text("Testing connect", "Prueba conectar", "Test connexion");
        break;
    case 1:
        pico_display_clear();
        pico_display_center_text(0, ui_language_text("DNS UNRESOLVED", "DNS NO RESUELTO", "DNS NON RESOLU"));
        pico_ui_wait_for_continue();
        return;
    case 2:
        pico_display_clear();
        pico_display_center_text(0, ui_language_text("NO LOCAL IP", "NO HAY IP LOCAL", "AUCUN IP LOCAL"));
        pico_ui_wait_for_continue();
        return;
    case 3:
        line = ui_language_text("Ping test", "Prueba de ping", "Ping test");
        break;
    case 4:
        pico_display_clear();
        pico_display_center_text(0, ui_language_text("Checking TCP", "Prueba de TCP", "Test deTCP pour"));
        line = ui_language_text("to web services", "a servicios web", "lesServices Web");
        break;
    case 5:
        pico_display_clear();
        pico_display_center_text(0, ui_language_text("Internet: YES", "Internet: SI", "Internet: OUI"));
        pico_display_center_text(1, ui_language_text("PicoBrew: YES", "PicoBrew: SI", "PicoBrew: OUI"));
        return;
    default:
        return;
    }
    pico_display_center_text(1, line);
    pico_display_flush();
}
