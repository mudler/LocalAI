// Unit tests for wav_header. Standard library only. The harness compiles this
// as a single translation unit, so the implementation is included directly.

#include "wav_header.cpp"

#include <cstdint>
#include <cstdio>
#include <string>

static int failures = 0;

static void check(bool ok, const std::string &name) {
    if (!ok) {
        failures++;
        fprintf(stderr, "FAIL: %s\n", name.c_str());
    } else {
        fprintf(stderr, "ok:   %s\n", name.c_str());
    }
}

using audiocpp_backend::streaming_wav_header;

static std::uint32_t read_u32(const std::string &data, size_t offset) {
    return static_cast<std::uint32_t>(static_cast<unsigned char>(data[offset])) |
           (static_cast<std::uint32_t>(static_cast<unsigned char>(data[offset + 1])) << 8) |
           (static_cast<std::uint32_t>(static_cast<unsigned char>(data[offset + 2])) << 16) |
           (static_cast<std::uint32_t>(static_cast<unsigned char>(data[offset + 3])) << 24);
}

static std::uint16_t read_u16(const std::string &data, size_t offset) {
    return static_cast<std::uint16_t>(
        static_cast<std::uint16_t>(static_cast<unsigned char>(data[offset])) |
        (static_cast<std::uint16_t>(static_cast<unsigned char>(data[offset + 1])) << 8));
}

static std::string to_hex(const std::string &data) {
    static const char *digits = "0123456789abcdef";
    std::string out;
    out.reserve(data.size() * 2);
    for (const char byte : data) {
        const auto value = static_cast<unsigned char>(byte);
        out.push_back(digits[value >> 4]);
        out.push_back(digits[value & 0x0F]);
    }
    return out;
}

static void test_header_layout() {
    const std::string header = streaming_wav_header(24000, 1);

    check(header.size() == 44, "canonical 44 byte header");
    check(header.compare(0, 4, "RIFF") == 0, "RIFF magic");
    check(header.compare(8, 4, "WAVE") == 0, "WAVE magic");
    check(header.compare(12, 4, "fmt ") == 0, "fmt chunk id");
    check(header.compare(36, 4, "data") == 0, "data chunk id");

    check(read_u32(header, 16) == 16, "PCM fmt chunk is 16 bytes");
    check(read_u16(header, 20) == 1, "format tag 1 is PCM");
    check(read_u16(header, 22) == 1, "mono channel count");
    check(read_u32(header, 24) == 24000, "sample rate");
    // byte rate = rate * channels * bytes per sample
    check(read_u32(header, 28) == 24000 * 1 * 2, "byte rate");
    check(read_u16(header, 32) == 2, "block align for mono 16 bit");
    check(read_u16(header, 34) == 16, "16 bits per sample");
}

// The field-wise checks above can all pass while the fields sit in the wrong
// ORDER, since several of them hold the same value. This pins the whole 44 byte
// string against a literal transcribed from the layout in pkg/audio/audio.go,
// which is the struct binary.Write serializes for every Go LocalAI backend.
// Independent of the implementation: it was written out by hand rather than
// captured from a run.
static void test_exact_bytes_match_the_go_layout() {
    const std::string expected =
        "52494646"          // "RIFF"
        "ffffffff"          // chunk size: streaming sentinel
        "57415645"          // "WAVE"
        "666d7420"          // "fmt "
        "10000000"          // subchunk1 size 16
        "0100"              // audio format 1 (PCM)
        "0100"              // channels 1
        "c05d0000"          // sample rate 24000
        "80bb0000"          // byte rate 48000
        "0200"              // block align 2
        "1000"              // bits per sample 16
        "64617461"          // "data"
        "ffffffff";         // subchunk2 size: streaming sentinel
    check(to_hex(streaming_wav_header(24000, 1)) == expected,
          "byte for byte match with the canonical mono 24 kHz header");
}

// The whole point: a streaming header cannot know the final length, so both
// size fields are the sentinel. A client that sees a real size stops early.
static void test_streaming_sentinels() {
    const std::string header = streaming_wav_header(16000, 1);
    check(read_u32(header, 4) == 0xFFFFFFFFu, "RIFF chunk size is the sentinel");
    check(read_u32(header, 40) == 0xFFFFFFFFu, "data chunk size is the sentinel");
}

static void test_stereo() {
    const std::string header = streaming_wav_header(44100, 2);
    check(read_u16(header, 22) == 2, "stereo channel count");
    check(read_u32(header, 28) == 44100 * 2 * 2, "stereo byte rate");
    check(read_u16(header, 32) == 4, "block align for stereo 16 bit");
    check(header.size() == 44, "stereo header is still 44 bytes");
}

static void test_degenerate_inputs() {
    // A zero or negative channel count must not produce a header that divides
    // by zero downstream; clamp to mono.
    const std::string zero_channels = streaming_wav_header(16000, 0);
    check(read_u16(zero_channels, 22) == 1, "zero channels clamps to mono");
    check(read_u16(zero_channels, 32) == 2, "zero channels still block aligns as mono");
    check(read_u32(zero_channels, 28) == 32000, "zero channels byte rate is the mono one");

    const std::string negative_channels = streaming_wav_header(16000, -3);
    check(read_u16(negative_channels, 22) == 1, "negative channels clamps to mono");

    // A channel count past the field's range must saturate rather than wrap:
    // 65536 truncated to uint16 is 0, and a zero channel count writes a zero
    // block align, which is what a reader divides the data size by.
    const std::string too_many = streaming_wav_header(16000, 65536);
    check(read_u16(too_many, 22) == 65535, "an out of range channel count saturates");
    check(read_u16(too_many, 32) != 0, "an out of range channel count never writes a zero block align");

    // A non-positive rate is written as zero rather than wrapping through the
    // unsigned conversion: 0 is visibly wrong to whoever reads the header,
    // 4294967295 looks like a plausible field nobody checks.
    const std::string zero_rate = streaming_wav_header(0, 1);
    check(read_u32(zero_rate, 24) == 0, "zero sample rate stays zero");
    check(read_u32(zero_rate, 28) == 0, "zero sample rate yields a zero byte rate");
    const std::string negative_rate = streaming_wav_header(-48000, 1);
    check(read_u32(negative_rate, 24) == 0, "negative sample rate is clamped to zero");
    check(read_u32(negative_rate, 28) == 0, "negative sample rate yields a zero byte rate");

    // The sentinels are unconditional. A degenerate rate must not turn the
    // stream into one a client thinks it can measure.
    check(read_u32(zero_rate, 4) == 0xFFFFFFFFu, "degenerate input keeps the RIFF sentinel");
    check(read_u32(zero_rate, 40) == 0xFFFFFFFFu, "degenerate input keeps the data sentinel");
}

// Large but legal: 384 kHz 8 channel would overflow a 16 bit byte rate and
// must not overflow the 32 bit one either.
static void test_large_but_legal() {
    const std::string header = streaming_wav_header(384000, 8);
    check(read_u32(header, 28) == 384000u * 8u * 2u, "high rate multichannel byte rate");
    check(read_u16(header, 32) == 16, "high channel count block align");
}

int main() {
    test_header_layout();
    test_exact_bytes_match_the_go_layout();
    test_streaming_sentinels();
    test_stereo();
    test_degenerate_inputs();
    test_large_but_legal();
    if (failures) {
        fprintf(stderr, "%d check(s) failed\n", failures);
        return 1;
    }
    fprintf(stderr, "all wav_header checks passed\n");
    return 0;
}
