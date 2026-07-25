#include "audio_units.h"

#include <algorithm>
#include <cmath>

namespace audiocpp_backend {

std::int64_t samples_to_nanoseconds(std::int64_t samples, int sample_rate) {
    if (sample_rate <= 0) {
        return 0;
    }
    // Split into whole seconds plus a remainder so the intermediate product
    // cannot overflow on long recordings, and so rates like 44100 stay exact.
    const std::int64_t rate = static_cast<std::int64_t>(sample_rate);
    const std::int64_t whole_seconds = samples / rate;
    const std::int64_t remainder = samples % rate;
    return whole_seconds * 1000000000LL + (remainder * 1000000000LL) / rate;
}

float samples_to_seconds(std::int64_t samples, int sample_rate) {
    if (sample_rate <= 0) {
        return 0.0f;
    }
    return static_cast<float>(static_cast<double>(samples) /
                              static_cast<double>(sample_rate));
}

std::int64_t seconds_to_samples(double seconds, int sample_rate) {
    if (sample_rate <= 0 || seconds <= 0.0) {
        return 0;
    }
    return static_cast<std::int64_t>(seconds * static_cast<double>(sample_rate));
}

std::vector<float> s16le_to_f32(const std::string &bytes) {
    std::vector<float> samples;
    const size_t count = bytes.size() / 2;
    samples.reserve(count);
    for (size_t i = 0; i < count; ++i) {
        const auto low = static_cast<unsigned char>(bytes[i * 2]);
        const auto high = static_cast<unsigned char>(bytes[i * 2 + 1]);
        const auto raw = static_cast<std::int16_t>(
            static_cast<std::uint16_t>(low) |
            (static_cast<std::uint16_t>(high) << 8));
        samples.push_back(static_cast<float>(raw) / 32768.0f);
    }
    return samples;
}

std::string f32_to_s16le(const std::vector<float> &samples) {
    std::string bytes;
    bytes.reserve(samples.size() * 2);
    for (const float sample : samples) {
        const float clamped = std::max(-1.0f, std::min(1.0f, sample));
        // 32767 rather than 32768 so +1.0 saturates at INT16_MAX instead of
        // overflowing to INT16_MIN.
        const auto value =
            static_cast<std::int16_t>(std::lround(clamped * 32767.0f));
        const auto raw = static_cast<std::uint16_t>(value);
        bytes.push_back(static_cast<char>(raw & 0xFF));
        bytes.push_back(static_cast<char>((raw >> 8) & 0xFF));
    }
    return bytes;
}

} // namespace audiocpp_backend
