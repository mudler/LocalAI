#include "wav_header.h"

#include <cstdint>
#include <limits>

namespace audiocpp_backend {
namespace {

void append_u32(std::string &out, std::uint32_t value) {
    out.push_back(static_cast<char>(value & 0xFF));
    out.push_back(static_cast<char>((value >> 8) & 0xFF));
    out.push_back(static_cast<char>((value >> 16) & 0xFF));
    out.push_back(static_cast<char>((value >> 24) & 0xFF));
}

void append_u16(std::string &out, std::uint16_t value) {
    out.push_back(static_cast<char>(value & 0xFF));
    out.push_back(static_cast<char>((value >> 8) & 0xFF));
}

// The unknown-length sentinel, in both the RIFF and the data chunk size.
constexpr std::uint32_t kStreamingSize = 0xFFFFFFFFu;
constexpr std::uint16_t kBitsPerSample = 16;
constexpr std::uint32_t kPcmFmtChunkSize = 16;
constexpr std::uint16_t kFormatTagPcm = 1;
constexpr int kMaxChannels = 65535;

} // namespace

std::string streaming_wav_header(int sample_rate, int channels) {
    int clamped_channels = channels > 0 ? channels : 1;
    if (clamped_channels > kMaxChannels) {
        clamped_channels = kMaxChannels;
    }
    const auto channel_count = static_cast<std::uint16_t>(clamped_channels);
    const auto rate = static_cast<std::uint32_t>(sample_rate > 0 ? sample_rate : 0);
    const auto block_align =
        static_cast<std::uint16_t>(channel_count * (kBitsPerSample / 8));
    // uint32 arithmetic on purpose: 384 kHz by 8 channels is 6.1 MB/s, which
    // does not fit the uint16 block align it is derived from.
    const std::uint32_t byte_rate = rate * static_cast<std::uint32_t>(block_align);
    static_assert(std::numeric_limits<std::uint32_t>::max() >= 0xFFFFFFFFu,
                  "the streaming sentinel must be representable");

    std::string header;
    header.reserve(44);
    header += "RIFF";
    append_u32(header, kStreamingSize); // unknown total length
    header += "WAVE";
    header += "fmt ";
    append_u32(header, kPcmFmtChunkSize);
    append_u16(header, kFormatTagPcm);
    append_u16(header, channel_count);
    append_u32(header, rate);
    append_u32(header, byte_rate);
    append_u16(header, block_align);
    append_u16(header, kBitsPerSample);
    header += "data";
    append_u32(header, kStreamingSize); // unknown payload length
    return header;
}

} // namespace audiocpp_backend
