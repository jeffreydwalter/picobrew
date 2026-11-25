#include "factory_flash.hpp"
#include <esp_system.h>
#include <mbedtls/sha256.h>
#include <algorithm>
#include <cstring>

namespace picobrew { namespace esp32_ota {
namespace {
const esp_partition_t* exact(esp_partition_type_t type, esp_partition_subtype_t subtype, const char* label,
                             unsigned address, unsigned size) {
    const auto* p = esp_partition_find_first(type, subtype, label);
    return p && p->address == address && p->size == size && !p->encrypted ? p : nullptr;
}
unsigned partition_count(esp_partition_type_t type) {
    unsigned count = 0;
    for (auto it = esp_partition_find(type, ESP_PARTITION_SUBTYPE_ANY, nullptr); it; it = esp_partition_next(it)) ++count;
    return count;
}
bool same_partition(const esp_partition_t* left, const esp_partition_t* right) {
    return left && right && left->type == right->type && left->subtype == right->subtype &&
           left->address == right->address && left->size == right->size;
}
bool inspect_image(const esp_partition_t* part, const char* prefix, const char* expected_version, std::uint32_t& size) {
    if (!part) return false;
    esp_image_metadata_t image = {};
    const esp_partition_pos_t pos = {part->address, part->size};
    char marker[64] = {};
    std::uint32_t descriptor_magic = 0;
    constexpr std::size_t DescriptorOffset = sizeof(esp_image_header_t) + sizeof(esp_image_segment_header_t);
    if (esp_image_verify(ESP_IMAGE_VERIFY, &pos, &image) != ESP_OK || !image.image.hash_appended ||
        !image.image.segment_count || image.segments[0].load_addr != 0x3f400020 ||
        image.segments[0].data_len < sizeof(esp_app_desc_t) + sizeof(marker) ||
        esp_partition_read(part, DescriptorOffset, &descriptor_magic, sizeof(descriptor_magic)) != ESP_OK ||
        descriptor_magic != ESP_APP_DESC_MAGIC_WORD ||
        esp_partition_read(part, DescriptorOffset + sizeof(esp_app_desc_t), marker, sizeof(marker)) != ESP_OK ||
        !std::memchr(marker, 0, sizeof(marker)) || std::strncmp(marker, prefix, std::strlen(prefix)))
        return false;
    const char* version = marker + std::strlen(prefix);
    if (!*version || (expected_version && std::strcmp(version, expected_version))) return false;
    size = image.image_len;
    return size && size <= part->size;
}
bool hash_image(const esp_partition_t* part, std::uint32_t size, char (&output)[65]) {
    mbedtls_sha256_context ctx;
    mbedtls_sha256_init(&ctx);
    bool ok = !mbedtls_sha256_starts_ret(&ctx, 0);
    std::uint8_t buffer[1024], hash[32];
    for (std::uint32_t offset = 0; ok && offset < size; offset += sizeof(buffer)) {
        const auto n = std::min<std::uint32_t>(size - offset, sizeof(buffer));
        ok = esp_partition_read(part, offset, buffer, n) == ESP_OK && !mbedtls_sha256_update_ret(&ctx, buffer, n);
    }
    ok = ok && !mbedtls_sha256_finish_ret(&ctx, hash);
    mbedtls_sha256_free(&ctx);
    if (ok) {
        constexpr char Hex[] = "0123456789abcdef";
        for (unsigned i = 0; i < sizeof(hash); ++i) { output[i*2] = Hex[hash[i] >> 4]; output[i*2+1] = Hex[hash[i] & 15]; }
        output[64] = 0;
    }
    return ok;
}
}
void FactoryFlash::inspect() {
    const auto* running = esp_ota_get_running_partition();
    // A factory recovery image always keeps the controller off, including if
    // damaged partition metadata makes the update path unavailable.
    if (running && !std::strcmp(running->label, "pb_recovery")) role_ = Role::InvalidRecovery;
    factory_ = exact(ESP_PARTITION_TYPE_APP, ESP_PARTITION_SUBTYPE_APP_FACTORY, "pb_recovery", 0x10000, RecoveryBytes);
    app_ = exact(ESP_PARTITION_TYPE_APP, ESP_PARTITION_SUBTYPE_APP_OTA_0, "pb_app", SlotOffset, SlotBytes);
    if (!factory_ || !app_ || partition_count(ESP_PARTITION_TYPE_APP) != 2 || partition_count(ESP_PARTITION_TYPE_DATA) != 3 ||
        !exact(ESP_PARTITION_TYPE_DATA, ESP_PARTITION_SUBTYPE_DATA_OTA, "otadata", 0xe000, 0x2000) ||
        !exact(ESP_PARTITION_TYPE_DATA, ESP_PARTITION_SUBTYPE_DATA_NVS, "nvs", 0x9000, 0x5000) ||
        !exact(ESP_PARTITION_TYPE_DATA, ESP_PARTITION_SUBTYPE_DATA_SPIFFS, "spiffs", FilesystemOffset, FilesystemBytes)) return;
    std::uint32_t recovery_size = 0;
    if (!inspect_image(factory_, RecoveryPrefix, nullptr, recovery_size) ||
        !inspect_image(running, same_partition(running, factory_) ? RecoveryPrefix : IdentityPrefix, Version, running_size_) || !hash_image(running, running_size_, running_digest_)) return;
#ifdef PB_Z_WIRED_RECOVERY
    // The preceding esp_image_verify checked the native image integrity.
    // Legacy USB installations have the same public version but a Wi-Fi-only receiver.
    char transport[sizeof(PB_Z_WIRED_RECOVERY)] = {};
    const std::size_t offset = sizeof(esp_image_header_t)+sizeof(esp_image_segment_header_t)+sizeof(esp_app_desc_t)+64;
    wired_recovery_ = recovery_size >= offset+sizeof(transport) &&
        esp_partition_read(factory_,offset,transport,sizeof(transport))==ESP_OK &&
        !std::memcmp(transport,PB_Z_WIRED_RECOVERY,sizeof(transport));
#endif
    if (same_partition(running, factory_)) role_ = Role::Recovery;
    else if (same_partition(running, app_)) role_ = Role::Application;
}
bool FactoryFlash::select_recovery() {
    std::uint32_t size = 0;
    return role_ == Role::Application && inspect_image(factory_, RecoveryPrefix, nullptr, size) &&
           esp_ota_set_boot_partition(factory_) == ESP_OK && same_partition(esp_ota_get_boot_partition(), factory_);
}
bool FactoryFlash::start(std::uint32_t size) {
    if (role_ != Role::Recovery || !size || size > SlotBytes || !same_partition(esp_ota_get_running_partition(), factory_)) return false;
    // The native loader can fall back to factory if the previously selected
    // application is corrupt. Make that fallback durable before erasing it.
    if (!same_partition(esp_ota_get_boot_partition(), factory_) &&
        (esp_ota_set_boot_partition(factory_) != ESP_OK || !same_partition(esp_ota_get_boot_partition(), factory_))) return false;
    return esp_ota_begin(app_, size, &writer_) == ESP_OK;
}
void FactoryFlash::abandon() {
    // This SDK has no abort call. end frees its handle, and never selects boot.
    if (writer_) esp_ota_end(writer_);
    writer_ = 0;
}
std::uint32_t FactoryFlash::session_id() { return esp_random(); }
bool FactoryFlash::write(const std::uint8_t* bytes, std::size_t size) {
    return writer_ && esp_ota_write(writer_, bytes, size) == ESP_OK;
}
bool FactoryFlash::matches(std::uint32_t offset, const std::uint8_t* bytes, std::size_t size) {
    std::uint8_t stored[128];
    while (size) {
        const auto n = std::min(size, sizeof(stored));
        if (esp_partition_read(app_, offset, stored, n) != ESP_OK || std::memcmp(bytes, stored, n)) return false;
        bytes += n; offset += n; size -= n;
    }
    return true;
}
bool FactoryFlash::finish(std::uint32_t size, const char* version, const char* sha256) {
    const auto handle = writer_; writer_ = 0;
    char digest[65] = {};
    std::uint32_t native_size = 0;
    return handle && esp_ota_end(handle) == ESP_OK && inspect_image(app_, IdentityPrefix, version, native_size) && native_size == size &&
           hash_image(app_, size, digest) && !std::strcmp(digest, sha256) &&
           esp_ota_set_boot_partition(app_) == ESP_OK && same_partition(esp_ota_get_boot_partition(), app_);
}
}}
