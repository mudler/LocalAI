// Unit tests for audio_units. Standard library only. The harness compiles this
// as a single translation unit, so the implementation is included directly.

#include "audio_units.cpp"

#include <cmath>
#include <cstdio>
#include <limits>
#include <string>
#include <vector>

static int failures = 0;

static void check(bool ok, const std::string &name) {
    if (!ok) {
        failures++;
        fprintf(stderr, "FAIL: %s\n", name.c_str());
    } else {
        fprintf(stderr, "ok:   %s\n", name.c_str());
    }
}

static bool close_to(float a, float b, float tol) { return std::fabs(a - b) <= tol; }

using namespace audiocpp_backend;

static void test_nanoseconds() {
    // LocalAI TranscriptSegment/TranscriptWord times are nanoseconds
    // (Go reads them as time.Duration).
    check(samples_to_nanoseconds(16000, 16000) == 1000000000LL, "1s at 16k is 1e9 ns");
    check(samples_to_nanoseconds(8000, 16000) == 500000000LL, "0.5s at 16k");
    check(samples_to_nanoseconds(0, 16000) == 0, "zero samples is zero ns");
    check(samples_to_nanoseconds(1000, 0) == 0, "zero sample rate yields zero, not UB");
    // 44.1 kHz must not lose precision to float arithmetic.
    check(samples_to_nanoseconds(44100, 44100) == 1000000000LL, "1s at 44.1k");
    check(samples_to_nanoseconds(22050, 44100) == 500000000LL, "0.5s at 44.1k");
    // The cases above all land on values a float happens to hold exactly, so
    // they do not actually rule float arithmetic out. These do:
    // a fraction that does not divide evenly, and a duration whose magnitude
    // exceeds a float's 24-bit mantissa at nanosecond resolution.
    check(samples_to_nanoseconds(44099, 44100) == 999977324LL,
          "44.1k fraction is exact, not rounded through a float");
    check(samples_to_nanoseconds(44100LL * 3600, 44100) == 3600000000000LL,
          "one hour at 44.1k is exact to the nanosecond");
    // A naive samples * 1e9 would overflow int64 here; the split into whole
    // seconds plus a remainder is what keeps this correct.
    check(samples_to_nanoseconds(44100LL * 360000, 44100) == 360000000000000LL,
          "100 hours at 44.1k does not overflow");
    // Double arithmetic is close enough to pass everything above, but still
    // truncates this one a nanosecond short. Integer division does not.
    check(samples_to_nanoseconds(4004, 8000) == 500500000LL,
          "0.5005s at 8k is exact to the nanosecond");
    // Truncation, not rounding: this matches Go's time.Duration conventions and
    // keeps successive sample indices monotonic. The exact value here is
    // 22675.7...; rounding to nearest would give 22676.
    check(samples_to_nanoseconds(1, 44100) == 22675LL,
          "a sub-nanosecond fraction truncates rather than rounding up");
}

static void test_seconds() {
    check(close_to(samples_to_seconds(24000, 24000), 1.0f, 1e-6f), "1s at 24k");
    check(close_to(samples_to_seconds(12000, 24000), 0.5f, 1e-6f), "0.5s at 24k");
    check(close_to(samples_to_seconds(100, 0), 0.0f, 1e-6f), "zero sample rate is 0s");
    check(seconds_to_samples(1.0, 16000) == 16000, "1s to samples at 16k");
    check(seconds_to_samples(0.5, 16000) == 8000, "0.5s to samples at 16k");
    check(seconds_to_samples(1.0, 0) == 0, "zero sample rate yields zero samples");
    check(seconds_to_samples(-1.0, 16000) == 0, "negative seconds clamps to zero");

    // seconds_to_samples is the one entry point fed by untrusted-shaped input:
    // a float-seconds timestamp off the wire, or a VAD boundary from a model
    // that diverged. A hugely negative sample index used later as an offset or
    // a length is a wild pointer, not merely a wrong timestamp.
    const double nan_seconds = std::numeric_limits<double>::quiet_NaN();
    const double inf_seconds = std::numeric_limits<double>::infinity();
    const std::int64_t max_samples = std::numeric_limits<std::int64_t>::max();
    check(seconds_to_samples(nan_seconds, 16000) == 0, "NaN seconds yields zero");
    check(seconds_to_samples(inf_seconds, 16000) == max_samples,
          "infinite seconds saturates instead of overflowing");
    check(seconds_to_samples(1e30, 16000) == max_samples,
          "out of range seconds saturates instead of overflowing");
    check(seconds_to_samples(-inf_seconds, 16000) == 0,
          "negative infinity clamps to zero");

    // Crossing the float-seconds boundary and back is the expected round trip
    // for the VAD and diarize messages, so it must not lose a sample.
    // Truncation loses one about half the time, starting at n=1.
    check(seconds_to_samples(samples_to_seconds(1, 44100), 44100) == 1,
          "one sample survives the seconds round trip at 44.1k");
    check(seconds_to_samples(samples_to_seconds(1, 16000), 16000) == 1,
          "one sample survives the seconds round trip at 16k");
    check(seconds_to_samples(samples_to_seconds(4001, 8000), 8000) == 4001,
          "4001 samples survive the seconds round trip at 8k");
}

static void test_s16le_round_trip() {
    const std::vector<float> original = {0.0f, 0.5f, -0.5f, 1.0f, -1.0f};
    const std::string encoded = f32_to_s16le(original);
    check(encoded.size() == original.size() * 2, "two bytes per sample");

    const std::vector<float> decoded = s16le_to_f32(encoded);
    check(decoded.size() == original.size(), "round trip keeps the sample count");
    for (size_t i = 0; i < original.size(); ++i) {
        // 16-bit quantisation: one LSB is ~3.05e-5. Guard the index so a short
        // result reports a named failure instead of aborting the whole suite.
        check(i < decoded.size() && close_to(decoded[i], original[i], 1e-4f),
              "round trip preserves sample " + std::to_string(i));
    }
}

static void test_s16le_endianness() {
    // 0.5 encodes to 16384 = 0x4000, little endian is 0x00 0x40.
    const std::string encoded = f32_to_s16le({0.5f});
    check(encoded.size() == 2, "one sample is two bytes");
    check(static_cast<unsigned char>(encoded[0]) == 0x00, "low byte first");
    check(static_cast<unsigned char>(encoded[1]) == 0x40, "high byte second");
}

static void test_s16le_clamping() {
    // Values outside [-1, 1] must clamp, not wrap around to the opposite sign.
    const std::string encoded = f32_to_s16le({2.0f, -2.0f});
    const std::vector<float> decoded = s16le_to_f32(encoded);
    check(decoded.size() == 2, "two samples survive clamping");
    check(decoded.size() > 0 && decoded[0] > 0.99f,
          "positive overshoot clamps to full scale");
    check(decoded.size() > 1 && decoded[1] < -0.99f,
          "negative overshoot clamps to full scale");
}

static void test_s16le_decode_range() {
    // INT16_MIN is the one value that pins the decode scale. Dividing by 32767
    // instead of 32768 would decode it to -1.00003, outside the [-1, 1] range
    // the header promises, and every other test would still pass.
    const std::vector<float> decoded = s16le_to_f32(std::string("\x00\x80", 2));
    check(decoded.size() == 1, "INT16_MIN decodes to one sample");
    check(decoded.size() == 1 && decoded[0] == -1.0f,
          "INT16_MIN decodes to exactly -1.0, not past full scale");
}

static void test_s16le_nan_input() {
    // A NaN sample must not reach std::lround, whose result is unspecified for
    // NaN. See the argument-order comment in f32_to_s16le.
    const std::vector<float> decoded =
        s16le_to_f32(f32_to_s16le({std::numeric_limits<float>::quiet_NaN()}));
    check(decoded.size() == 1, "a NaN sample still encodes to one sample");
    check(decoded.size() == 1 && std::isfinite(decoded[0]),
          "a NaN sample encodes to a finite value");
    check(decoded.size() == 1 && decoded[0] >= -1.0f && decoded[0] <= 1.0f,
          "a NaN sample encodes within full scale");
}

static void test_s16le_odd_length() {
    // A truncated frame must drop the dangling byte rather than read past it.
    const std::string odd(5, '\0');
    check(s16le_to_f32(odd).size() == 2, "odd byte count drops the trailing byte");
    check(s16le_to_f32(std::string()).empty(), "empty input yields no samples");
}

int main() {
    test_nanoseconds();
    test_seconds();
    test_s16le_round_trip();
    test_s16le_endianness();
    test_s16le_clamping();
    test_s16le_decode_range();
    test_s16le_nan_input();
    test_s16le_odd_length();
    if (failures) {
        fprintf(stderr, "%d check(s) failed\n", failures);
        return 1;
    }
    fprintf(stderr, "all audio_units checks passed\n");
    return 0;
}
