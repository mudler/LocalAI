#include "audio_units.h"

#include <algorithm>
#include <cmath>
#include <limits>

namespace audiocpp_backend {

std::int64_t interleaved_frame_count(std::size_t sample_count, int channels) {
    const std::size_t lanes = channels > 0 ? static_cast<std::size_t>(channels)
                                           : static_cast<std::size_t>(1);
    // Truncating division is deliberate: a trailing partial frame is not a
    // position every channel reached, so counting it would overstate the length.
    return static_cast<std::int64_t>(sample_count / lanes);
}

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
    // from samples_to_seconds converts back to the sample it started as.
    // Truncation lost one sample about half the time, starting at n=1.
    //
    // That round trip is exact only below roughly 2^23 samples. Past that the
    // float samples_to_seconds returns can no longer resolve adjacent indices
    // and the trip fails whatever the rounding. Both the first failing INDEX
    // and the duration it stands for depend on the rate, so they are listed per
    // rate rather than folded into one range; measured:
    //
    //   16 kHz    16384001 samples   17.1 min
    //   44.1 kHz  11289602 samples    4.3 min
    //   48 kHz    12288002 samples    4.3 min
    //   96 kHz    12288002 samples    2.1 min
    //
    // The shortest recording this bites is therefore a couple of minutes of
    // 96 kHz audio. It is a property of the float seconds API itself, not of
    // the rounding here, and it is why nothing should use these to carry a
    // sample-accurate position in a long recording.
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
        // NaN maps to silence. A NaN sample rendered as a full-scale click is
        // worse audio than a dropped one, and this unit converts audio that may
        // have originated off the wire.
        //
        // This guard also removes what used to be a spelling hazard in the
        // clamp below. std::min and std::max return their first argument when
        // the comparison is false, and every comparison against NaN is false,
        // so before this branch existed the choice of spelling silently decided
        // whether a NaN reached std::lround, whose result is unspecified for
        // NaN. These three leaked it, the last being the idiomatic C++17 way to
        // write a clamp and so the likeliest future edit:
        //   std::min(std::max(sample, -1.0f), 1.0f)
        //   std::max(std::min(sample, 1.0f), -1.0f)
        //   std::clamp(sample, -1.0f, 1.0f)
        // The order is no longer load-bearing now that the guard runs first,
        // but the history is why the guard is here, so do not drop it.
        if (std::isnan(sample)) {
            bytes.push_back(0);
            bytes.push_back(0);
            continue;
        }
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
