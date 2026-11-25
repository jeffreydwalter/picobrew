#include "zseries/standalone.hpp"
#include "engine_internal.hpp"
#include "recipe_record.hpp"
#include <algorithm>
#include <cstdio>
#include <cstring>

namespace zseries { namespace standalone {
using namespace oem;
namespace {
constexpr std::uint32_t OfferLifetime = 120000;
constexpr std::uint32_t ReplayWindow = 2 * OfferLifetime;

bool valid_invite_id(const char* id) {
    return std::strlen(id) == 16 && std::strspn(id, "0123456789abcdef") == 16;
}

bool same_recipe(const api::Recipe& a, const api::Recipe& b) {
    std::uint8_t first[api::detail::ZRecipeBytes] = {}, second[api::detail::ZRecipeBytes] = {};
    const auto size = api::detail::write_recipe_record(first, a);
    return size == api::detail::write_recipe_record(second, b) && !std::memcmp(first, second, size);
}

api::Result changed() {
    return {409, "recipe_changed", "Saved recipes changed. Request a new group invitation."};
}
api::Result invalid_body() {
    return {400, "invalid_invitation", "Invalid group invitation fields"};
}
}

api::Result Adapter::apply_group_strength(const GroupProgram& program,api::Recipe& recipe){
    if((program.selected_abv>=0&&!kit_adjustable(recipe,true))||
       (program.selected_ibu>=0&&!kit_adjustable(recipe,false)))
        return {409,"kit_adjustment_unavailable","This saved recipe cannot apply the requested PicoPak setting"};
    if(program.selected_abv>=0)recipe.pak.selected_abv=program.selected_abv;
    if(program.selected_ibu>=0)recipe.pak.selected_ibu=program.selected_ibu;
    return {};
}

bool Adapter::group_ready() const {
    return begun_ && !stopped() && engine_ && engine_->persistence_ready() &&
           !core_.system_error && core_.msp_state.id == Z_MSP_APP_RUNNING && !core_.msp_state.errors &&
           recovery_.ready() && !recovery_.present() && !resuming_ && !recovery_active_ &&
           !queued_ && core_.runtime.state.id == RuntimeStateId::Standby;
}

void Adapter::group_started(std::uint32_t id, bool local_brew) {
    group_source_id_[0] = 0;
    GroupProgram program;
    if (local_brew) {
        // The selected slot may have been changed while the native Start screen was open.
        api::Recipe saved;
        if (!engine_->recipe_get(selected_recipe_.id, saved).ok()) return;
        // Publish an ephemeral native dose only against the exact saved revision
        // selected by the operator. Every other serialized field must match.
        if(selected_recipe_.pak.recipe_id){
            const auto& choice=selected_recipe_.pak;
            if(saved.pak.selected_abv!=choice.selected_abv)program.selected_abv=choice.selected_abv;
            if(saved.pak.selected_ibu!=choice.selected_ibu)program.selected_ibu=choice.selected_ibu;
            if((program.selected_abv>=0||program.selected_ibu>=0)&&
               (selected_revision_!=engine_->recipe_revision()||!apply_group_strength(program,saved).ok()))return;
        }
        if(!same_recipe(saved,selected_recipe_))return;
        std::strcpy(program.action, "brew");
        std::strcpy(program.recipe_id, selected_recipe_.id);
        program.revision = engine_->recipe_revision();
    } else if (id == static_cast<unsigned>(ZProgramId::Rinse)) {
        std::strcpy(program.action, "rinse");
    } else if (id == static_cast<unsigned>(ZProgramId::Clean)) {
        std::strcpy(program.action, "clean");
    } else {
        return;
    }
    if (++group_sequence_ == 0) ++group_sequence_;
    ::snprintf(group_source_id_, sizeof group_source_id_, "%08lx%08lx",
                  static_cast<unsigned long>(ports_.group_boot_nonce), static_cast<unsigned long>(group_sequence_));
    group_source_time_ = ports_.millis(ports_.context);
    group_source_ = program;
}

bool Adapter::group_screen() const {
    const auto& menu = head_.screens.menu_data;
    return page_ == Page::Group && menu.count == 2 &&
           static_cast<std::uint32_t>(menu.items[0].event.command) == GroupDecline &&
           menu.items[0].event.index == invite_ticket_ &&
           std::strcmp(head_.screens.state.title.data(), text("Group Brewing","Grupo de coccion","Brassage groupe")) == 0;
}

bool Adapter::show_invite() {
    auto& screens = head_.screens;
    screens.clear_data();
    HeadUiEvent decline, accept;
    decline.command = static_cast<UiCommand>(GroupDecline);
    accept.command = static_cast<UiCommand>(GroupAccept);
    if (++invite_ticket_ == 0) ++invite_ticket_;
    decline.index = accept.index = invite_ticket_;
    invite_filter_missing_ = core_.safeties.filter_missing;
    const char* accept_label = invite_filter_missing_ ? text("Insert step filter","Inserte el filtro","Inserer le filtre") :
                                                      text("Accept and start","Aceptar e iniciar","Accepter et demarrer");
    if (!screens.menu_data.append(decline, text("Decline","Rechazar","Refuser")) ||
        !screens.menu_data.append(accept, accept_label)) return false;
    char strength[24]={};
    const auto& program=group_invite_.program;
    if(program.selected_abv>=0&&program.selected_ibu>=0)
        ::snprintf(strength,sizeof strength,"ABV %d  IBU %d",program.selected_abv,program.selected_ibu);
    else if(program.selected_abv>=0)::snprintf(strength,sizeof strength,"ABV %d",program.selected_abv);
    else if(program.selected_ibu>=0)::snprintf(strength,sizeof strength,"IBU %d",program.selected_ibu);
    const char* lines[] = {group_invite_.from, invite_name_,strength};
    HeadScreenRequest request;
    request.title = text("Group Brewing","Grupo de coccion","Brassage groupe");
    request.screen.layout = 17;
    request.has_body = true;
    request.body.lines = lines; request.body.count = strength[0]?3:2;
    request.menu_rows = 2;
    page_ = Page::Group;
    return screens.show(request) == HeadScreenResult::Ok;
}

void Adapter::finish_invite(InviteState state) {
    const bool owns_screen = group_screen();
    invite_state_ = state;
    invite_time_ = ports_.millis(ports_.context);
    if (owns_screen && core_.core.state.id == CoreStateId::Idle && !core_.system_error)
        show_home();
    else if (page_ == Page::Group)
        page_ = Page::Original;
}

void Adapter::poll_group() {
    const auto now = ports_.millis(ports_.context);
    if (group_source_id_[0] && now - group_source_time_ >= OfferLifetime) group_source_id_[0] = 0;
    if (invite_state_ == InviteState::Pending) {
        if (now - invite_time_ >= group_invite_.ttl_ms) finish_invite(InviteState::Expired);
        else if (!group_ready() || !group_screen() || core_.core.state.id != CoreStateId::Idle ||
                 core_.core.commands.pending || core_.runtime_commands.pending)
            finish_invite(InviteState::Unavailable);
        else if (core_.safeties.filter_missing != invite_filter_missing_ && !show_invite())
            finish_invite(InviteState::Unavailable);
    } else if (now - invite_time_ >= OfferLifetime) {
        invite_state_ = InviteState::Empty;
    }
}

api::Result Adapter::receive_invite(const GroupInvitation& offer) {
    const auto now = ports_.millis(ports_.context);
    if (!std::strcmp(offer.id, group_invite_.id) && invite_state_ != InviteState::Empty) {
        if (std::strcmp(offer.from, group_invite_.from) || std::strcmp(offer.program.action, group_invite_.program.action) ||
            std::strcmp(offer.program.recipe_id, group_invite_.program.recipe_id) || offer.program.revision != group_invite_.program.revision ||
            offer.program.selected_abv!=group_invite_.program.selected_abv || offer.program.selected_ibu!=group_invite_.program.selected_ibu)
            return {409,"invitation_conflict","This invitation ID already identifies a different program"};
        return {}; // A duplicate reports the current state without extending the original deadline.
    }
    SeenInvitation* slot = nullptr;
    for (auto& seen : seen_invitations_) {
        if (seen.id[0] && now - seen.time < ReplayWindow) {
            if (!std::strcmp(seen.id, offer.id)) return {409,"invitation_closed","This group invitation has already been handled"};
        } else if (!slot) slot = &seen;
    }
    if (!slot || invite_state_ == InviteState::Pending || !group_ready() || !idle() ||
        head_.ui_events.pending || head_.encoder_events.pending || head_.button_events.pending)
        return {409,"busy","Group invitations require an idle brewer at its home screen"};
    api::Recipe recipe;
    const char* label = nullptr;
    if (!std::strcmp(offer.program.action, "brew")) {
        if (offer.program.revision != engine_->recipe_revision()) return changed();
        const auto loaded = engine_->recipe_get(offer.program.recipe_id, recipe);
        if (!loaded.ok()) return loaded;
        const auto adjusted=apply_group_strength(offer.program,recipe);
        if(!adjusted.ok())return adjusted;
        label = recipe.name;
    } else {
        label = !std::strcmp(offer.program.action, "rinse") ? text("Rinse","Enjuague","Rincage") : text("Clean","Limpieza","Nettoyage");
    }
    if (!wake_standby()) return {409,"unavailable","The brewer could not wake for the invitation"};
    group_invite_ = offer;
    invite_state_ = InviteState::Pending;
    invite_time_ = now;
    std::strcpy(slot->id, offer.id); slot->time = now;
    api::detail::copy(invite_name_, sizeof invite_name_, label);
    if (!show_invite()) {
        finish_invite(InviteState::Unavailable);
        return {503,"display_unavailable","The brewer could not display the group invitation"};
    }
    return {};
}

api::Result Adapter::respond_invite(const char* id, const char* op) {
    if (std::strcmp(id, group_invite_.id) || invite_state_ == InviteState::Empty)
        return {409,"invitation_closed","This group invitation is no longer available"};
    const bool accept = !std::strcmp(op, "accept");
    const auto state = accept ? InviteState::Accepted : !std::strcmp(op, "decline") ? InviteState::Declined : InviteState::Withdrawn;
    if (invite_state_ == state) return {};
    if (invite_state_ != InviteState::Pending) return {409,"invitation_closed","The group invitation has ended"};
    if (ports_.millis(ports_.context) - invite_time_ >= group_invite_.ttl_ms) {
        finish_invite(InviteState::Expired);
        return {409,"invitation_expired","The group invitation has expired"};
    }
    if (!accept) { finish_invite(state); return {}; }
    if (!group_ready() || !group_screen() || core_.core.state.id != CoreStateId::Idle ||
        core_.core.commands.pending || core_.runtime_commands.pending) {
        finish_invite(InviteState::Unavailable);
        return {409,"busy","The brewer is no longer ready to start"};
    }
    if (core_.safeties.filter_missing) return {409,"filter_missing","Insert the step filter before accepting"};
    const auto& program = group_invite_.program;
    const bool brew = !std::strcmp(program.action, "brew");
    const auto native_id = brew ? ZProgramId::Buffered : !std::strcmp(program.action, "clean") ? ZProgramId::Clean : ZProgramId::Rinse;
    if (brew) {
        if (program.revision != engine_->recipe_revision()) { finish_invite(InviteState::Unavailable); return changed(); }
        api::Recipe recipe;
        auto result = engine_->recipe_get(program.recipe_id, recipe);
        if(result.ok())result=apply_group_strength(program,recipe);
        if (result.ok()) result = load_recipe(recipe, true);
        if (!result.ok()) { finish_invite(InviteState::Unavailable); return result; }
    } else if (!queue_program(static_cast<unsigned>(native_id))) {
        return {409,"busy","The native start queue is busy"};
    }
    joined_program_ = static_cast<unsigned>(native_id);
    page_ = Page::Original;
    finish_invite(InviteState::Accepted);
    return {};
}

bool Adapter::group_http(const PicoHttpRequest& req, PicoHttpResponse& response, char* arena, std::size_t capacity) {
    if (std::strcmp(req.path, "/api/v1/group-brew")) return false;
    api::detail::Writer writer(arena, capacity);
    api::Result result;
    if (!req.auth_configured) result = {503,"api_key_not_provisioned","Provision the device key first"};
    else if (!req.authenticated) result = {401,"unauthorized","A valid device bearer key is required"};
    else if (!begun_) result = {503,"controller_unavailable","The brewer has not completed startup"};
    else if (req.method != PICO_HTTP_POST && req.method != PICO_HTTP_GET) result = {405,"method_not_allowed","Use GET or POST"};
    else {
        poll_group();
        if (req.method == PICO_HTTP_POST) {
            char op[10] = {}, key[40];
            unsigned seen = 0;
            GroupInvitation offer;
            api::detail::Json json(req.body, req.body ? req.body_length : 0);
            bool valid = req.body && req.body_length < 512 && json.token('{');
            while (valid && !json.peek('}')) {
                if (!json.key(key)) { valid = false; break; }
                unsigned field = 0;
                if (!std::strcmp(key,"op")) { field = 1; valid = json.string(op,sizeof op); }
                else if (!std::strcmp(key,"id")) { field = 2; valid = json.string(offer.id,sizeof offer.id) && valid_invite_id(offer.id); }
                else if (!std::strcmp(key,"from")) { field = 4; valid = json.string(offer.from,sizeof offer.from) && offer.from[0]; }
                else if (!std::strcmp(key,"action")) { field = 8; valid = json.string(offer.program.action,sizeof offer.program.action); }
                else if (!std::strcmp(key,"recipe_id")) {
                    field = 16; valid = json.string(offer.program.recipe_id,sizeof offer.program.recipe_id) && api::detail::valid_id(offer.program.recipe_id);
                } else if (!std::strcmp(key,"revision")) {
                    std::int64_t number = 0; field = 32; valid = json.integer(1,UINT32_MAX,number);
                    offer.program.revision = static_cast<std::uint32_t>(number);
                } else if (!std::strcmp(key,"selected_abv")||!std::strcmp(key,"selected_ibu")) {
                    std::int64_t number=0;const bool abv=!std::strcmp(key,"selected_abv");field=abv?128:256;
                    valid=json.integer(0,10,number);
                    if(abv)offer.program.selected_abv=static_cast<std::int8_t>(number);
                    else offer.program.selected_ibu=static_cast<std::int8_t>(number);
                } else if (!std::strcmp(key,"ttl_ms")) {
                    std::int64_t number = 0; field = 64; valid = json.integer(1,OfferLifetime,number);
                    offer.ttl_ms = static_cast<std::uint32_t>(number);
                }
                if (!field || (seen & field)) valid = false;
                seen |= field;
                if (!valid || !json.next('}')) break;
                if (json.peek('}')) valid = false;
            }
            if (!valid || !json.token('}') || !json.done()) result = invalid_body();
            else if (!std::strcmp(op,"offer")) {
                const bool brew = !std::strcmp(offer.program.action,"brew");
                const bool routine = !std::strcmp(offer.program.action,"rinse") || !std::strcmp(offer.program.action,"clean");
                if ((brew && (seen & 63u) == 63) || (routine && (seen & ~64u) == 15)) result = receive_invite(offer);
                else result = invalid_body();
            } else if (seen == 3 && (!std::strcmp(op,"accept") || !std::strcmp(op,"decline") || !std::strcmp(op,"withdraw"))) {
                result = respond_invite(offer.id,op);
            } else result = invalid_body();
        }
    }
    if (!result.ok()) {
        api::detail::error(writer,result);
    } else {
        const auto write_program = [&](const GroupProgram& program) {
            writer.text(",\"action\":"); writer.quote(program.action);
            if (program.recipe_id[0]) {
                writer.text(",\"recipe_id\":"); writer.quote(program.recipe_id);
                writer.format(",\"revision\":%lu",static_cast<unsigned long>(program.revision));
                if(program.selected_abv>=0)writer.format(",\"selected_abv\":%d",program.selected_abv);
                if(program.selected_ibu>=0)writer.format(",\"selected_ibu\":%d",program.selected_ibu);
            }
        };
        writer.text("{\"source\":");
        if (group_source_id_[0]) {
            writer.text("{\"id\":"); writer.quote(group_source_id_);
            writer.format(",\"age_ms\":%lu",static_cast<unsigned long>(ports_.millis(ports_.context)-group_source_time_));
            write_program(group_source_);
            writer.text("}");
        } else writer.text("null");
        writer.text(",\"invite\":");
        if (invite_state_ != InviteState::Empty) {
            static const char* const states[] = {"","pending","accepted","declined","withdrawn","expired","unavailable"};
            writer.text("{\"id\":"); writer.quote(group_invite_.id);
            writer.text(",\"from\":"); writer.quote(group_invite_.from);
            write_program(group_invite_.program);
            writer.text(",\"state\":"); writer.quote(states[static_cast<unsigned>(invite_state_)]); writer.text("}");
        } else writer.text("null");
        writer.text("}");
    }
    if (!writer.ok) {
        result = {503,"response_capacity","Insufficient response buffer"};
        api::detail::error(writer,result);
    }
    response.status = result.status;
    response.content_type = "application/json";
    response.content_encoding = nullptr;
    response.body = reinterpret_cast<const std::uint8_t*>(arena);
    response.body_length = writer.ok ? writer.length : 0;
    return true;
}
}} // namespace zseries::standalone
