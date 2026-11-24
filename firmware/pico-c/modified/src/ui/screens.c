/* Compact C screen layouts and scrolling prompts recovered from the OEM image. */
#include "ui_internal.h"

void pico_screen_water_error_help(void)
{
    pico_display_start_scrolling(ui_language_text(
        "ERROR: Fill water reservoir if empty. Otherwise select exit and check the water path before restarting.",
        "ERROR: Llene el reservorio si esta vacio. Si no, seleccione salir y revise el circuito de agua.",
        "ERREUR: Remplissez le reservoir vide. Sinon, quittez et verifiez le circuit d'eau."), NULL);
}

void pico_screen_thermal_recovery_help(void)
{
    pico_display_start_scrolling("ERROR: Securely attach hoses to keg posts, check dip tube, and continue. Otherwise select exit and check the water path.", NULL);
}

void pico_screen_still_setup_step(uint8_t *step)
{
    static const char *const text[] = {
        "Add 2 inches of water into the Step Filter",
        "Insert the Step Filter and Lid into Pico",
        "Attach the IN and OUT hoses to PicoStill",
        "Fill water reservoir if empty"
    };
    pico_display_clear();
    /* The original passes an uninitialized stack string for invalid steps.
     * All callers use 0..3; reject other values instead of reading past NUL. */
    if (*step < 4)
        pico_display_start_scrolling(text[*step], NULL);
}

void pico_screen_ending_first_rinse(void)
{
    pico_display_clear();
    pico_display_logo();
    pico_display_center_text(1, ui_language_text("Ending FirstRinse", "Finalizacion", "Fin du nettoyage"));
    pico_display_flush();
}

void pico_screen_updating_firmware(void)
{
    pico_display_clear_row(1);
    pico_display_center_text(1, ui_language_text("Updating...", "Actualizacion", "Mise a jour"));
}

void pico_screen_downloading_firmware(void)
{
    /* The historical adapter name denotes the post-download restart screen. */
    pico_display_clear_row(1);
    pico_display_center_text(1, ui_language_text("Restarting", "Reiniciando", "Redemarrage"));
}

void pico_screen_firmware_update_failed(void)
{
    if (pico_ui_active->network->settings->language == 1)
        pico_display_clear();
    pico_display_center_text(0, ui_language_text("Pico C", "Error de", "Echec de la "));
    pico_ui_wait_for_label_choice(ui_language_text("Failed update", "actualizacion", "mise a jour"));
}

void pico_screen_device_information(void)
{
    uint8_t mac[6];
    char line[156], version[52];
    PicoUi *ui = pico_ui_active;
    ui->network->platform->mac_address(ui->network->platform_context, mac);
    pico_display_clear();
    pico_copy_firmware_version(version);
    snprintf(line, sizeof line, "PicoC %s", version);
    pico_display_text_at_cell(0, 0, line);
    pico_ui_draw_continue_footer();
    pico_display_flush();
    pico_display_wait_for_click();
    pico_display_clear();
    snprintf(line, sizeof line, "ID:%s", ui->app->device_uid);
    pico_ui_show_scrolling_message(line, pico_ui_draw_continue_footer);
    snprintf(line, sizeof line, "%02x:%02x:%02x:%02x:%02x:%02x",
        mac[0], mac[1], mac[2], mac[3], mac[4], mac[5]);
    pico_display_clear();
    pico_display_center_text(0, line);
    pico_ui_wait_for_footer_press("MAC ADDRESS");
}

static void progress(unsigned first, unsigned last)
{
    PicoControlSignals *s = &pico_ui_active->app->control_signals;
    s->progress_row = 1;
    s->progress_start_column = (uint8_t)first;
    s->progress_end_column = (uint8_t)last;
    s->progress_glyph_index = 0;
    s->progress_active = 1;
}

void pico_screen_still_connecting(const char *text)
{
    pico_display_clear();
    pico_display_center_text(0, text);
    pico_display_flush();
    progress(5, 16);
}

void pico_screen_still_exiting(void)
{
    pico_display_clear();
    pico_display_center_text(0, "Exiting Still");
    progress(5, 16);
}

void pico_screen_wifi_searching(void)
{
    pico_display_start_scrolling(ui_language_text("Searching for WiFi networks",
        "la busqueda de redes WiFi", "Recherche de sans fil"), NULL);
}

void pico_screen_ending_process(void)
{
    pico_display_clear();
    pico_display_center_text(1, ui_language_text("Ending Process", "Fin del proceso", "Fin du processus"));
    pico_display_flush();
}

void pico_screen_draw_program_notice(void)
{
    unsigned language = pico_ui_active->network->settings->language;
    pico_display_center_text(0, pico_title_label(3, language));
    pico_display_center_text(1, pico_title_label(4, language));
}

void pico_screen_show_program_notice(void)
{
    pico_display_clear();
    pico_screen_draw_program_notice();
    pico_display_flush();
}

void pico_screen_preheating_steam(short row)
{
    (void)row; /* C always uses the two fixed rows. */
    bool celsius = pico_ui_active->network->settings->temperature_unit_celsius != 0;
    pico_display_clear();
    pico_display_center_text(0, ui_language_text("Preheating", "Precalentamiento", "Prechauffage"));
    pico_display_center_text(1, celsius ? ui_language_text("steam - 99C", "vapor - 99C", "vapeur - 99C")
        : ui_language_text("steam - 210F", "vapor - 210F", "vapeur - 210F"));
}

void pico_screen_reinsert_step_filter(void)
{
    char label[32];
    pico_display_clear();
    pico_label_insert_step_filter(label);
    pico_display_draw_titled_frame(ui_language_text("ERROR", "ERROR", "ERREUR"));
    pico_display_center_text(1, label);
}

void pico_screen_changing_step(void)
{
    pico_display_clear();
    pico_display_center_text(1, ui_language_text("Changing step", "Cambiar paso", "Changement d'etape"));
    pico_display_draw_titled_frame(pico_ui_active->app->screen.recipe_name);
}

void pico_screen_setting_up_step(const char *title)
{
    pico_display_clear();
    pico_display_center_text(1, ui_language_text("Setting up step", "Configurar paso", "Mettre en place"));
    pico_display_draw_titled_frame(title);
}

void pico_screen_ending_step(const char *title)
{
    pico_display_clear();
    pico_display_center_text(1, ui_language_text("Ending step", "Finalizar paso", "Terminer l'etape"));
    pico_display_draw_titled_frame(title);
}

bool pico_screen_loading(int picopak)
{
#if PICO_STANDALONE
    const char *text = picopak ? ui_language_text("Loading recipe", "Cargando receta", "Chargement recette")
                               : ui_language_text("Loading", "Cargando", "Remplissage");
#else
    char text[32];
    snprintf(text, sizeof text, "%s%s", ui_language_text("Loading", "Cargando", "Remplissage"),
             picopak ? " PicoPak" : "");
#endif
    ui_interrupts_disable();
    pico_display_center_text(0, text);
    pico_display_flush();
    progress(1, 17);
    return ui_interrupts_enable();
}

bool pico_screen_loading_picopak(void)
{
    return pico_screen_loading(1);
}

void pico_screen_help_address(void)
{
    pico_display_center_text(0, ui_language_text("Go to", "Contacte", "Consultez"));
    pico_display_center_text(1, "Open Pico local IP");
    pico_display_flush();
}

void pico_screen_show_help_temporarily(void)
{
    pico_display_clear();
    pico_screen_help_address();
    pico_display_flush();
    ui_delay(5000);
}

uint32_t pico_screen_fill_reservoir_and_start(const char *title)
{
    (void)title; /* C uses only the configured program name in the menu. */
    pico_display_clear();
    snprintf(pico_ui_active->menu_labels[0], PICO_MENU_LABEL_LENGTH, "%s%s",
             ui_language_text("Start ", "Inicio ", "Demarrer "),
             pico_ui_active->app->recipe.program->name);
    pico_label_exit(pico_ui_active->menu_labels[1]);
    pico_display_start_scrolling("Fill reservoir with distilled water and select start", NULL);
    return 2;
}

void pico_screen_starting_program(const char *title)
{
    char text[64];
    pico_display_clear();
    pico_display_draw_titled_frame(title);
    snprintf(text, sizeof text, "%s%s", ui_language_text("Starting ", "Iniciar ", "Demarrer "),
             pico_ui_active->app->recipe.program->name);
    pico_display_center_text(1, text);
}

void pico_screen_ending_program(const char *title)
{
    char text[64];
    pico_display_clear();
    snprintf(text, sizeof text, "%s%s", ui_language_text("Ending ", "Finalizar ", "Terminer "),
             pico_ui_active->app->recipe.program->name);
    pico_display_center_text(1, text);
    pico_display_draw_titled_frame(title);
}

void pico_screen_show_ending_program(const char *title)
{
    pico_display_clear();
    pico_screen_ending_program(title);
    pico_display_flush();
}

void pico_screen_service_status(void)
{
    char label[32];
    pico_label_service(label);
    pico_display_clear_row(1);
    pico_display_center_text(1, label);
}

void pico_screen_wifi_no_signal(void)
{
    pico_display_clear_row(6);
    pico_display_start_scrolling(ui_language_text("No WiFi signal found", "No hay se\244al WiFi",
        "Pas de signal WiFi"), pico_ui_draw_continue_footer);
}

void pico_screen_wifi_connected(void)
{
    pico_display_clear_row(1);
    pico_display_center_text(1, ui_language_text("Connected", "Conectado", "Connecte"));
}

void pico_screen_wifi_connection_failed(void)
{
    pico_display_clear_row(1);
    pico_display_center_text(1, ui_language_text("Connect fail", "Conexi\242n fallida", "Echec connexion"));
}

void pico_screen_splash(void)
{
    char version[16];
    pico_display_center_text(0, PICO_MODEL_NAME);
    pico_copy_firmware_version(version);
    size_t length = strlen(version);
    pico_display_text_at_cell(1, (short)(length < 18 ? 18 - length : 0), version);
    pico_display_flush();
}

void pico_screen_error_heading(void)
{
    pico_display_center_text(0, "ERROR");
    pico_ui_draw_footer_single("Silence Alarm");
}
