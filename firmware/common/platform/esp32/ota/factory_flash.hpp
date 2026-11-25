#pragma once
#include "picobrew/esp32_ota.hpp"
extern "C" {
#include <esp_ota_ops.h>
}

namespace picobrew { namespace esp32_ota {
class FactoryFlash final : public Flash {
public:
    void inspect();
#ifdef PB_Z_WIRED_RECOVERY
    bool wired_recovery() const { return wired_recovery_; }
#endif
    Role role() const override { return role_; }
    const char* running_sha256() const override { return running_digest_; }
    std::uint32_t running_size() const override { return running_size_; }
    bool select_recovery() override;
    bool start(std::uint32_t size) override;
    void abandon() override;
    std::uint32_t session_id() override;
    bool write(const std::uint8_t*, std::size_t) override;
    bool matches(std::uint32_t offset, const std::uint8_t*, std::size_t) override;
    bool finish(std::uint32_t size, const char* version, const char* sha256) override;
private:
#ifdef PB_Z_WIRED_RECOVERY
    bool wired_recovery_ = false;
#endif
    Role role_ = Role::Unavailable;
    const esp_partition_t* factory_ = nullptr;
    const esp_partition_t* app_ = nullptr;
    esp_ota_handle_t writer_ = 0;
    char running_digest_[65] = {};
    std::uint32_t running_size_ = 0;
};
}}
