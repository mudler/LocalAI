// SPDX-License-Identifier: MIT
//
// Bounded media preparation for the typed MultimodalEmbedding and
// MultimodalRerank RPCs (protocol version 1).
//
// This header is pure C++17 with no llama.cpp, gRPC or protobuf dependency, so
// it is unit tested on its own (multimodal_embedding_test.cpp). It does three
// things:
//
//   * probe: read the size of an image or the length of an audio clip from its
//     container header, WITHOUT decoding it. Decoding is what allocates memory,
//     so every bound is checked first.
//   * Plan: charge each part against the remaining request-wide allowances that
//     the frontend sent, and refuse before any allocation when one is exceeded.
//     A zero allowance is exhausted, not unlimited.
//   * map a failure to the reason the Go frontend turns into an HTTP status.
//
// The constants mirror pkg/embeddingmedia/budget.go. The frontend enforces the
// request-wide totals too; this code enforces them independently because the
// backend must not trust the frontend's arithmetic or the media's own claims.
#pragma once

#include <algorithm>
#include <cstdint>
#include <cstring>
#include <string>
#include <vector>

namespace llama_grpc {
namespace mmembed {

// Per-part limits of protocol version 1. The request-wide totals arrive in the
// budget of each call.
constexpr uint64_t kMaxImagePixels        = 16'000'000;
constexpr uint64_t kMaxPartDurationMs     = 30'000;
constexpr uint64_t kMaxAudioChannels      = 2;
constexpr uint64_t kMaxAudioSampleRateHz  = 192'000;
constexpr uint64_t kBytesPerAudioSample   = 4; // mono float32 after preparation

// Remaining allowances, in the units of EmbeddingBudget.
struct Budget {
    uint64_t frames                 = 0;
    uint64_t pixels                 = 0;
    uint64_t audio_allocation_bytes = 0;
    uint64_t source_duration_ms     = 0;
    uint64_t temporary_bytes        = 0;
    uint64_t preparation_ms         = 0;
    uint64_t compressed_bytes       = 0;
    uint64_t text_bytes             = 0;
    uint64_t source_parts           = 0;
};

// What one call actually used, in the units of EmbeddingConsumption.
struct Consumption {
    uint64_t frames                 = 0;
    uint64_t pixels                 = 0;
    uint64_t audio_allocation_bytes = 0;
    uint64_t source_duration_ms     = 0;
    uint64_t temporary_bytes        = 0;
    uint64_t preparation_ms         = 0;
    uint64_t compressed_bytes       = 0;
    uint64_t text_bytes             = 0;
    uint64_t source_parts           = 0;
};

enum class Reason {
    Ok,
    InvalidInput,      // malformed structure or undecodable bytes (HTTP 400)
    UnsupportedFormat, // not a container this backend reads (HTTP 415)
    LimitExceeded,     // a bound or an allowance is exceeded (HTTP 413)
    Deadline,          // preparation time is exhausted (HTTP 504)
};

struct Failure {
    Reason      reason = Reason::Ok;
    std::string detail; // fixed text, never user content
    bool ok() const { return reason == Reason::Ok; }
};

inline Failure fail(Reason r, const char * detail) { return Failure{r, detail}; }

// The gRPC status message prefix the Go frontend reads. The text after the
// prefix is for logs only and never reaches the HTTP client.
inline std::string reason_prefix(Reason r) {
    switch (r) {
        case Reason::UnsupportedFormat: return "unsupported_media_format";
        case Reason::LimitExceeded:     return "limit_exceeded";
        case Reason::Deadline:          return "deadline_exceeded";
        default:                        return "invalid_embedding_input";
    }
}

// ---------------------------------------------------------------- byte readers

inline uint32_t be16(const uint8_t * p) { return (uint32_t(p[0]) << 8) | p[1]; }
inline uint32_t be32(const uint8_t * p) { return (uint32_t(p[0]) << 24) | (uint32_t(p[1]) << 16) | (uint32_t(p[2]) << 8) | p[3]; }
inline uint32_t le16(const uint8_t * p) { return uint32_t(p[0]) | (uint32_t(p[1]) << 8); }
inline uint32_t le32(const uint8_t * p) { return uint32_t(p[0]) | (uint32_t(p[1]) << 8) | (uint32_t(p[2]) << 16) | (uint32_t(p[3]) << 24); }

// ceil(a * b / c) without overflowing 64 bits. c must be non-zero.
inline uint64_t mul_div_ceil(uint64_t a, uint64_t b, uint64_t c) {
    const unsigned __int128 num = (unsigned __int128) a * b;
    const unsigned __int128 q   = (num + c - 1) / c;
    return q > UINT64_MAX ? UINT64_MAX : (uint64_t) q;
}

// ---------------------------------------------------------------------- images

struct ImageInfo {
    std::string format; // "png" or "jpeg"
    uint64_t    width  = 0;
    uint64_t    height = 0;
    uint64_t    pixels() const { return width * height; }
};

// Reads the pixel size from the PNG IHDR chunk or the first JPEG SOF marker.
// Anything else is unsupported: the decoder is never asked to guess a format.
inline Failure probe_image(const uint8_t * buf, size_t len, ImageInfo & out) {
    static const uint8_t png_sig[8] = {0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'};
    if (len >= 8 && std::memcmp(buf, png_sig, 8) == 0) {
        // 8 signature + 4 length + 4 type + 13 data
        if (len < 8 + 4 + 4 + 13 || be32(buf + 8) != 13 || std::memcmp(buf + 12, "IHDR", 4) != 0) {
            return fail(Reason::InvalidInput, "png header");
        }
        out.format = "png";
        out.width  = be32(buf + 16);
        out.height = be32(buf + 20);
    } else if (len >= 3 && buf[0] == 0xff && buf[1] == 0xd8 && buf[2] == 0xff) {
        out.format = "jpeg";
        size_t pos = 2;
        bool found = false;
        while (pos + 4 <= len) {
            if (buf[pos] != 0xff) {
                return fail(Reason::InvalidInput, "jpeg marker");
            }
            while (pos < len && buf[pos] == 0xff) {
                pos++; // fill bytes
            }
            if (pos >= len) {
                break;
            }
            const uint8_t marker = buf[pos++];
            if (marker == 0xd8 || marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7)) {
                continue; // markers without a length
            }
            if (marker == 0xd9 || marker == 0xda) {
                break; // end of image or start of scan before any frame header
            }
            if (pos + 2 > len) {
                break;
            }
            const size_t seg = be16(buf + pos);
            if (seg < 2) {
                return fail(Reason::InvalidInput, "jpeg segment");
            }
            const bool sof = marker >= 0xc0 && marker <= 0xcf && marker != 0xc4 && marker != 0xc8 && marker != 0xcc;
            if (sof) {
                if (seg < 8 || pos + 7 > len) {
                    return fail(Reason::InvalidInput, "jpeg frame header");
                }
                out.height = be16(buf + pos + 3);
                out.width  = be16(buf + pos + 5);
                found      = true;
                break;
            }
            pos += seg;
        }
        if (!found) {
            return fail(Reason::InvalidInput, "jpeg frame header");
        }
    } else {
        return fail(Reason::UnsupportedFormat, "image container");
    }
    if (out.width == 0 || out.height == 0) {
        return fail(Reason::InvalidInput, "image size");
    }
    return {};
}

// ----------------------------------------------------------------------- audio

struct AudioInfo {
    std::string format; // "wav", "flac" or "mp3"
    uint64_t    sample_rate = 0;
    uint64_t    channels    = 0;
    // Source duration, and the number of mono frames at the model rate after
    // resampling. Both are rounded up so a bound is never under-counted.
    uint64_t    duration_ms = 0;
    uint64_t    out_frames  = 0;
};

namespace detail {

inline Failure finish_audio(AudioInfo & out, uint64_t frames, uint64_t target_rate) {
    if (out.sample_rate == 0 || out.channels == 0) {
        return fail(Reason::InvalidInput, "audio format");
    }
    if (out.sample_rate > kMaxAudioSampleRateHz) {
        return fail(Reason::LimitExceeded, "audio sample rate");
    }
    if (out.channels > kMaxAudioChannels) {
        return fail(Reason::LimitExceeded, "audio channels");
    }
    if (frames == 0) {
        return fail(Reason::InvalidInput, "audio length");
    }
    out.duration_ms = mul_div_ceil(frames, 1000, out.sample_rate);
    out.out_frames  = mul_div_ceil(frames, target_rate, out.sample_rate);
    return {};
}

inline Failure probe_wav(const uint8_t * buf, size_t len, uint64_t target_rate, AudioInfo & out) {
    out.format = "wav";
    size_t   pos        = 12;
    bool     have_fmt   = false;
    uint32_t tag = 0, bits = 0, block_align = 0;
    uint64_t data_bytes = 0;
    bool     have_data  = false;
    while (pos + 8 <= len) {
        const uint32_t size = le32(buf + pos + 4);
        const uint8_t * body = buf + pos + 8;
        const size_t    left = len - (pos + 8);
        if (std::memcmp(buf + pos, "fmt ", 4) == 0) {
            if (size < 16 || left < 16) {
                return fail(Reason::InvalidInput, "wav fmt chunk");
            }
            tag          = le16(body);
            out.channels = le16(body + 2);
            out.sample_rate = le32(body + 4);
            block_align  = le16(body + 12);
            bits         = le16(body + 14);
            if (tag == 0xfffe && size >= 40 && left >= 40) {
                tag = le16(body + 24); // sub-format code of WAVE_FORMAT_EXTENSIBLE
            }
            have_fmt = true;
        } else if (std::memcmp(buf + pos, "data", 4) == 0) {
            // A streaming writer may leave the size at 0 or 0xFFFFFFFF; the bytes
            // actually present are the only trustworthy bound.
            data_bytes = std::min<uint64_t>(size, left);
            if (size == 0) {
                data_bytes = left;
            }
            have_data = true;
            break;
        }
        const uint64_t next = (uint64_t) pos + 8 + size + (size & 1);
        if (next > len) {
            break;
        }
        pos = (size_t) next;
    }
    if (!have_fmt || !have_data) {
        return fail(Reason::InvalidInput, "wav chunks");
    }
    // Integer PCM, IEEE float, A-law and mu-law have a fixed frame size. ADPCM
    // and other codecs do not, so their length cannot be read from the header.
    if (tag != 1 && tag != 3 && tag != 6 && tag != 7) {
        return fail(Reason::UnsupportedFormat, "wav codec");
    }
    if (out.channels == 0 || bits == 0 || block_align == 0) {
        return fail(Reason::InvalidInput, "wav fmt chunk");
    }
    // The smaller divisor can only over-estimate the length, never hide samples.
    const uint64_t frame_bytes = std::min<uint64_t>(block_align, out.channels * ((bits + 7) / 8));
    if (frame_bytes == 0) {
        return fail(Reason::InvalidInput, "wav fmt chunk");
    }
    return finish_audio(out, data_bytes / frame_bytes, target_rate);
}

inline Failure probe_flac(const uint8_t * buf, size_t len, uint64_t target_rate, AudioInfo & out) {
    out.format = "flac";
    // "fLaC", then the first metadata block, which must be STREAMINFO (34 bytes).
    if (len < 4 + 4 + 34 || (buf[4] & 0x7f) != 0 || ((uint32_t(buf[5]) << 16) | (uint32_t(buf[6]) << 8) | buf[7]) != 34) {
        return fail(Reason::InvalidInput, "flac streaminfo");
    }
    const uint8_t * si = buf + 8;
    out.sample_rate = (uint64_t(si[10]) << 12) | (uint64_t(si[11]) << 4) | (uint64_t(si[12]) >> 4);
    out.channels    = ((si[12] >> 1) & 7) + 1;
    const uint64_t total = (uint64_t(si[13] & 0x0f) << 32) | be32(si + 14);
    if (total == 0) {
        // The length is unknown, so it cannot be bounded before decoding.
        return fail(Reason::UnsupportedFormat, "flac without sample count");
    }
    return finish_audio(out, total, target_rate);
}

struct Mp3Header {
    bool     valid = false;
    uint32_t frame_bytes = 0;
    uint32_t samples = 0;
    uint32_t sample_rate = 0;
    uint32_t channels = 0;
};

inline Mp3Header mp3_header(const uint8_t * p, size_t left) {
    Mp3Header h;
    if (left < 4 || p[0] != 0xff || (p[1] & 0xe0) != 0xe0) {
        return h;
    }
    const int version = (p[1] >> 3) & 3; // 0 = 2.5, 1 = reserved, 2 = 2, 3 = 1
    const int layer   = (p[1] >> 1) & 3; // 1 = III, 2 = II, 3 = I
    const int bitrate_idx = p[2] >> 4;
    const int rate_idx    = (p[2] >> 2) & 3;
    const int padding     = (p[2] >> 1) & 1;
    if (version == 1 || layer == 0 || bitrate_idx == 0 || bitrate_idx == 15 || rate_idx == 3) {
        return h;
    }
    static const uint16_t br_v1_l1[15] = {0, 32, 64, 96, 128, 160, 192, 224, 256, 288, 320, 352, 384, 416, 448};
    static const uint16_t br_v1_l2[15] = {0, 32, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384};
    static const uint16_t br_v1_l3[15] = {0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320};
    static const uint16_t br_v2_l1[15] = {0, 32, 48, 56, 64, 80, 96, 112, 128, 144, 160, 176, 192, 224, 256};
    static const uint16_t br_v2_l23[15] = {0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160};
    static const uint32_t rates[3][3] = {{11025, 12000, 8000}, {0, 0, 0}, {22050, 24000, 16000}};
    static const uint32_t rates_v1[3] = {44100, 48000, 32000};
    const bool v1 = version == 3;
    uint32_t bitrate;
    if (layer == 3) {
        bitrate = (v1 ? br_v1_l1 : br_v2_l1)[bitrate_idx];
    } else if (layer == 2) {
        bitrate = (v1 ? br_v1_l2 : br_v2_l23)[bitrate_idx];
    } else {
        bitrate = (v1 ? br_v1_l3 : br_v2_l23)[bitrate_idx];
    }
    h.sample_rate = v1 ? rates_v1[rate_idx] : rates[version][rate_idx];
    h.channels    = ((p[3] >> 6) & 3) == 3 ? 1 : 2;
    const uint64_t bps = (uint64_t) bitrate * 1000;
    if (layer == 3) {
        h.samples     = 384;
        h.frame_bytes = (uint32_t) ((12 * bps / h.sample_rate + padding) * 4);
    } else if (layer == 2) {
        h.samples     = 1152;
        h.frame_bytes = (uint32_t) (144 * bps / h.sample_rate + padding);
    } else {
        h.samples     = v1 ? 1152 : 576;
        h.frame_bytes = (uint32_t) ((v1 ? 144 : 72) * bps / h.sample_rate + padding);
    }
    h.valid = h.frame_bytes >= 4;
    return h;
}

inline Failure probe_mp3(const uint8_t * buf, size_t len, uint64_t target_rate, AudioInfo & out) {
    out.format = "mp3";
    size_t pos = 0;
    if (len >= 10 && std::memcmp(buf, "ID3", 3) == 0) {
        const size_t tag = ((size_t) (buf[6] & 0x7f) << 21) | ((size_t) (buf[7] & 0x7f) << 14) | ((size_t) (buf[8] & 0x7f) << 7) | (buf[9] & 0x7f);
        pos = 10 + tag + ((buf[5] & 0x10) ? 10 : 0);
    }
    // Walk the frames: an MP3 has no trustworthy duration in its header, so the
    // length is the sum of its frames. A frame found after a gap must be
    // followed by another frame (or the end), so noise is not counted as audio.
    uint64_t out_frames = 0, src_ms_num = 0; // src_ms_num: sum of samples*1000/rate, in 1/1000 ms
    uint64_t frames = 0;
    bool in_stream = false;
    while (pos + 4 <= len) {
        const Mp3Header h = mp3_header(buf + pos, len - pos);
        bool accept = h.valid && pos + h.frame_bytes <= len;
        if (accept && !in_stream) {
            const size_t next = pos + h.frame_bytes;
            accept = next == len || next + 4 > len || mp3_header(buf + next, len - next).valid;
        }
        if (!accept) {
            in_stream = false;
            pos++;
            continue;
        }
        if (frames == 0) {
            out.sample_rate = h.sample_rate;
            out.channels    = h.channels;
        }
        in_stream = true;
        frames++;
        out_frames += mul_div_ceil(h.samples, target_rate, h.sample_rate);
        src_ms_num += mul_div_ceil((uint64_t) h.samples * 1000, 1000, h.sample_rate);
        pos += h.frame_bytes;
    }
    if (frames == 0) {
        return fail(Reason::InvalidInput, "mp3 frames");
    }
    Failure f = finish_audio(out, 1, target_rate); // validates rate and channels
    if (!f.ok()) {
        return f;
    }
    out.out_frames  = out_frames;
    out.duration_ms = (src_ms_num + 999) / 1000;
    return {};
}

} // namespace detail

// Reads the length of an audio clip from its container. target_rate is the
// sample rate of the projector; the clip is resampled to it as mono float32.
inline Failure probe_audio(const uint8_t * buf, size_t len, uint64_t target_rate, AudioInfo & out) {
    if (target_rate == 0) {
        return fail(Reason::UnsupportedFormat, "model has no audio input");
    }
    if (len >= 12 && std::memcmp(buf, "RIFF", 4) == 0 && std::memcmp(buf + 8, "WAVE", 4) == 0) {
        return detail::probe_wav(buf, len, target_rate, out);
    }
    if (len >= 4 && std::memcmp(buf, "fLaC", 4) == 0) {
        return detail::probe_flac(buf, len, target_rate, out);
    }
    if ((len >= 3 && std::memcmp(buf, "ID3", 3) == 0) || (len >= 2 && buf[0] == 0xff && (buf[1] & 0xe0) == 0xe0)) {
        return detail::probe_mp3(buf, len, target_rate, out);
    }
    return fail(Reason::UnsupportedFormat, "audio container");
}

// ------------------------------------------------------------------------ plan

// Plan charges parts against the remaining allowances in request order. Every
// check happens before the corresponding decode, so an over-budget part never
// allocates. A charge that fails leaves the plan unchanged.
class Plan {
public:
    explicit Plan(const Budget & remaining) : remaining_(remaining) {}

    const Budget &      remaining() const { return remaining_; }
    const Consumption & consumption() const { return used_; }

    Failure add_text(uint64_t bytes) {
        if (remaining_.source_parts < 1 || bytes > remaining_.text_bytes) {
            return fail(Reason::LimitExceeded, "text");
        }
        remaining_.source_parts -= 1;
        remaining_.text_bytes   -= bytes;
        used_.source_parts      += 1;
        used_.text_bytes        += bytes;
        return {};
    }

    // Charges one image of `compressed` bytes and reads its size. The caller
    // decodes only when this returns Ok.
    Failure add_image(const uint8_t * buf, size_t len, ImageInfo & info) {
        if (remaining_.source_parts < 1 || len > remaining_.compressed_bytes) {
            return fail(Reason::LimitExceeded, "image bytes");
        }
        Failure f = probe_image(buf, len, info);
        if (!f.ok()) {
            return f;
        }
        const uint64_t px = info.pixels();
        if (px > kMaxImagePixels || px > remaining_.pixels) {
            return fail(Reason::LimitExceeded, "image pixels");
        }
        remaining_.source_parts     -= 1;
        remaining_.compressed_bytes -= len;
        remaining_.pixels           -= px;
        used_.source_parts          += 1;
        used_.compressed_bytes      += len;
        used_.pixels                += px;
        return {};
    }

    // Charges one audio clip and reads its length. Audio allocation is the mono
    // float32 buffer at the model rate; duration counts the source once.
    Failure add_audio(const uint8_t * buf, size_t len, uint64_t target_rate, AudioInfo & info) {
        if (remaining_.source_parts < 1 || len > remaining_.compressed_bytes) {
            return fail(Reason::LimitExceeded, "audio bytes");
        }
        Failure f = probe_audio(buf, len, target_rate, info);
        if (!f.ok()) {
            return f;
        }
        const unsigned __int128 alloc = (unsigned __int128) info.out_frames * kBytesPerAudioSample;
        if (info.duration_ms > kMaxPartDurationMs || info.duration_ms > remaining_.source_duration_ms ||
            alloc > remaining_.audio_allocation_bytes) {
            return fail(Reason::LimitExceeded, "audio length");
        }
        const uint64_t bytes = (uint64_t) alloc;
        remaining_.source_parts           -= 1;
        remaining_.compressed_bytes       -= len;
        remaining_.source_duration_ms     -= info.duration_ms;
        remaining_.audio_allocation_bytes -= bytes;
        used_.source_parts                += 1;
        used_.compressed_bytes            += len;
        used_.source_duration_ms          += info.duration_ms;
        used_.audio_allocation_bytes      += bytes;
        return {};
    }

    // Records elapsed preparation time and fails once the allowance is spent.
    // Time is cumulative: the caller passes the total since the call started.
    Failure check_time(uint64_t elapsed_ms) {
        used_.preparation_ms = elapsed_ms;
        if (elapsed_ms > remaining_.preparation_ms) {
            return fail(Reason::Deadline, "preparation time");
        }
        return {};
    }

private:
    Budget      remaining_;
    Consumption used_;
};

// ---------------------------------------------------------------------- prompt

// True when `text` contains the media marker. User text must never contain it:
// the marker is what binds a media part to its position, so a text part that
// carries one would shift every later binding or smuggle a position.
inline bool contains_marker(const std::string & text, const std::string & marker) {
    return !marker.empty() && text.find(marker) != std::string::npos;
}

} // namespace mmembed
} // namespace llama_grpc
