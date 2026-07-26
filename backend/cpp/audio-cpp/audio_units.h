#pragma once

// Time and sample-format conversion between audio.cpp's runtime types (sample
// indices, float PCM) and LocalAI's proto types. Standard library only.
//
// LocalAI uses three different time units:
//   TranscriptSegment / TranscriptWord start,end : int64 nanoseconds
//   VADSegment start,end                         : float seconds
//   DiarizeSegment start,end                     : float seconds

#include <cstdint>
#include <string>
#include <vector>

namespace audiocpp_backend {

// Returns 0 when sample_rate is not positive rather than dividing by zero.
// Uses integer arithmetic so 44.1 kHz does not lose precision.
std::int64_t samples_to_nanoseconds(std::int64_t samples, int sample_rate);

float samples_to_seconds(std::int64_t samples, int sample_rate);

// Rounds to nearest. Negative seconds and NaN both yield 0, and a value too
// large to convert saturates at INT64_MAX rather than overflowing. Round trips
// with samples_to_seconds only below roughly 2^23 samples, past which the float
// seconds can no longer resolve adjacent sample indices.
std::int64_t seconds_to_samples(double seconds, int sample_rate);

// Decodes little-endian signed 16-bit PCM. A trailing odd byte is dropped.
std::vector<float> s16le_to_f32(const std::string &bytes);

// Encodes to little-endian signed 16-bit PCM, clamping to [-1, 1] first so an
// overshooting sample saturates instead of wrapping to the opposite sign.
// A NaN sample encodes to 0, on the grounds that silence beats a full-scale
// click.
std::string f32_to_s16le(const std::vector<float> &samples);

} // namespace audiocpp_backend
