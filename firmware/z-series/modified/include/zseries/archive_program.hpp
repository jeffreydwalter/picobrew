#pragma once
#include <cstdint>

namespace zseries { namespace standalone {
// Recreate the Z119 ArduinoJson decimal conversion for an original fill
// quantity with up to three decimal places. The frozen parser multiplies
// the mantissa by 10^-1 and then 10^-2 rather than dividing by 1000.
inline double archive_water_liters(std::uint16_t ml) noexcept {
    unsigned places=3;
    while(places&&ml%10==0){ml/=10;--places;}
    double value=ml;
    if(places&1)value*=0.1;
    if(places&2)value*=0.01;
    return value;
}
}}
