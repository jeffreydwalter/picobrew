// The original fixed-length record remains readable and writable. The distinct
// Z record length identifies the version for catalogs and brew checkpoints.
#pragma once
#include "picobrew/device_services.hpp"
#include "json.hpp"

namespace picobrew { namespace standalone { namespace detail {
bool recipe_valid(const Recipe& recipe, bool zero_minutes, bool z_program_maker, bool z_cloud);
constexpr std::size_t LegacyRecipeBytes = 54 + RecipeStepCapacity * 28;
// The cloud extension retains the complete legacy step layout and adds the
// starting keg charge (milliliters, little endian) and native beer setup flag.
constexpr std::size_t CloudRecipeBytes = LegacyRecipeBytes + 3;
constexpr std::size_t ZRecipeBytes = 55 + RecipeStepCapacity * 38;

inline std::size_t write_recipe_record(std::uint8_t* bytes, const Recipe& recipe) {
    std::memcpy(bytes, recipe.id, 21);
    std::memcpy(bytes + 21, recipe.name, 32);
    bytes[53] = recipe.step_count;
    if (recipe.z_program) {
        bytes[54] = (recipe.is_beer ? 1 : 0) | (recipe.use_pico_pak ? 2 : 0) |
                    (recipe.has_metric ? 4 : 0) | (recipe.is_metric ? 8 : 0);
    }
    for (unsigned i = 0; i < recipe.step_count; ++i) {
        const auto& step = recipe.steps[i];
        auto* p = recipe.z_program ? bytes + 55 + i * 38 : bytes + 54 + i * 28;
        std::memcpy(p, step.name, 22);
        p[22] = step.location;
        if (recipe.z_program) {
            const auto& z = step.z;
            p[23] = (z.pause ? 1 : 0) | (z.null_target ? 2 : 0) | (z.has_time ? 4 : 0);
            std::uint64_t bits;
            static_assert(sizeof(bits) == sizeof(z.target_c), "binary64 Celsius required");
            std::memcpy(&bits, &z.target_c, sizeof(bits));
            put64(p + 24, bits);
            put32(p + 32, z.seconds);
            p[36] = z.drain_seconds;
            p[37] = z.drain_seconds >> 8;
        } else {
            p[23] = step.target_f;
            p[24] = step.minutes;
            p[25] = step.minutes >> 8;
            p[26] = step.drain_minutes;
            p[27] = step.drain_minutes >> 8;
        }
    }
    if (recipe.z_cloud_water_ml) {
        bytes[LegacyRecipeBytes] = recipe.z_cloud_water_ml;
        bytes[LegacyRecipeBytes + 1] = recipe.z_cloud_water_ml >> 8;
        bytes[LegacyRecipeBytes + 2] = recipe.is_beer;
        return CloudRecipeBytes;
    }
    return recipe.z_program ? ZRecipeBytes : LegacyRecipeBytes;
}
inline bool read_recipe_record(Recipe& recipe, const std::uint8_t* bytes, std::size_t size) {
    if (size != ZRecipeBytes && size != LegacyRecipeBytes && size != CloudRecipeBytes) return false;
    recipe.z_program = size == ZRecipeBytes;
    std::memcpy(recipe.id, bytes, 21);
    std::memcpy(recipe.name, bytes + 21, 32);
    recipe.step_count = bytes[53];
    if (recipe.step_count > RecipeStepCapacity) return false;
    unsigned flags = recipe.z_program ? bytes[54] : 1;
    if (flags & ~15u) return false;
    recipe.is_beer = flags & 1;
    recipe.z_cloud_water_ml = 0;
    if (size == CloudRecipeBytes) {
        const auto* fields = bytes + LegacyRecipeBytes;
        recipe.z_cloud_water_ml = fields[0] | (unsigned(fields[1]) << 8);
        if (!recipe.z_cloud_water_ml || fields[2] > 1) return false;
        recipe.is_beer = fields[2] != 0;
    }
    recipe.use_pico_pak = flags & 2;
    recipe.has_metric = flags & 4;
    recipe.is_metric = flags & 8;
    for (unsigned i = 0; i < recipe.step_count; ++i) {
        auto& step = recipe.steps[i];
        step = RecipeStep{};
        const auto* p = recipe.z_program ? bytes + 55 + i * 38 : bytes + 54 + i * 28;
        std::memcpy(step.name, p, 22);
        step.location = p[22];
        if (recipe.z_program) {
            if (p[23] & ~7u) return false;
            step.z.pause = p[23] & 1;
            step.z.null_target = p[23] & 2;
            step.z.has_time = p[23] & 4;
            std::uint64_t bits = get64(p + 24);
            std::memcpy(&step.z.target_c, &bits, sizeof(bits));
            step.z.seconds = get32(p + 32);
            step.z.drain_seconds = p[36] | (unsigned(p[37]) << 8);
        } else {
            step.target_f = p[23];
            step.minutes = p[24] | (unsigned(p[25]) << 8);
            step.drain_minutes = p[26] | (unsigned(p[27]) << 8);
        }
    }
    return true;
}
}}}
