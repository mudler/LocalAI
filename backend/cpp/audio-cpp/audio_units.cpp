#include "audio_units.h"

#include <algorithm>
#include <cmath>
#include <limits>

namespace audiocpp_backend {

std::int64_t samples_to_nanoseconds(std::int64_t samples, int sample_rate) {
    if (sample_rate <= 0) {
        return 0;
    }
    // Split into whole seconds plus a remainder so the intermediate product
    // cannot overflow on long recordings, and so rates like 44100 stay exact.
    // The remainder division truncates deliberately: that matches Go's
    // time.Duration conventions and keeps successive sample indices monotonic.
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
    // !(seconds > 0.0) rather than seconds <= 0.0: every comparison against NaN
    // is false, so the <= form lets NaN reach the cast below, which is undefined
    // behaviour and lands on INT64_MIN in practice. This is the one entry point
    // fed by untrusted-shaped input (a float-seconds timestamp off the wire, or
    // a boundary from a model that diverged), and a hugely negative sample index
    // used later as an offset or a length is a wild pointer rather than merely a
    // wrong timestamp.
    if (sample_rate <= 0 || !(seconds > 0.0)) {
        return 0;
    }
    const double scaled = seconds * static_cast<double>(sample_rate);
    // Bound before the cast for the same reason: converting a double at or above
    // 2^63 (infinity included) is undefined behaviour, so saturate instead.
    const double limit =
        static_cast<double>(std::numeric_limits<std::int64_t>::max());
    if (scaled >= limit) {
        return std::numeric_limits<std::int64_t>::max();
    }
    // Round rather than truncate: these functions exist to cross the float
    // seconds boundary the VAD and diarize messages use, so a value that came
    // from samples_to_seconds must convert back to the sample it started as.
    // Truncation loses one sample about half the time, starting at n=1.
    return static_cast<std::int64_t>(std::llround(scaled));
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
        // 32768 on decode against 32767 on encode is deliberate, not a typo.
        // 32768 is what keeps INT16_MIN at exactly -1.0 and every other code
        // inside the [-1, 1] range this header promises; dividing by 32767
        // would decode INT16_MIN to -1.00003. See f32_to_s16le for the other
        // half of the pair. The cost is that a round trip shrinks a sample by
        // 32767/32768, well under one LSB.
        samples.push_back(static_cast<float>(raw) / 32768.0f);
    }
    return samples;
}

std::string f32_to_s16le(const std::vector<float> &samples) {
    std::string bytes;
    bytes.reserve(samples.size() * 2);
    for (const float sample : samples) {
        // Argument order is load-bearing: std::min(1.0f, sample) returns 1.0f
        // for a NaN sample, because NaN < 1.0f is false and min returns its
        // first argument in that case. Written the equally natural
        // std::min(sample, 1.0f), a NaN would pass straight through to
        // std::lround, whose result is unspecified for NaN. Do not reorder.
        const float clamped = std::max(-1.0f, std::min(1.0f, sample));
        // 32767 rather than 32768 so +1.0 saturates at INT16_MAX instead of
        // overflowing to INT16_MIN. See s16le_to_f32 for why decode differs.
        const auto value =
            static_cast<std::int16_t>(std::lround(clamped * 32767.0f));
        const auto raw = static_cast<std::uint16_t>(value);
        bytes.push_back(static_cast<char>(raw & 0xFF));
        bytes.push_back(static_cast<char>((raw >> 8) & 0xFF));
    }
    return bytes;
}

} // namespace audiocpp_backend
