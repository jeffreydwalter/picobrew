#pragma once
#include <cstdint>
#define PB_FIELD_IDENTITY "PicoBrew OTA1|z-series|1.0.0"
#define PB_RECOVERY_IDENTITY "PicoBrew REC1|z-series|1.0.0"
namespace picobrew { namespace esp32_ota {
constexpr char Model[] = "z-series";
constexpr char Version[] = "1.0.0";
constexpr char IdentityPrefix[] = "PicoBrew OTA1|z-series|";
constexpr char RecoveryPrefix[] = "PicoBrew REC1|z-series|";
constexpr std::uint32_t RecoveryBytes = 0xe0000, SlotOffset = 0xf0000, SlotBytes = 0x1a1000;
constexpr std::uint32_t FilesystemOffset = 0x291000, FilesystemBytes = 0x16f000;
}}
