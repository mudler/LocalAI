#pragma once

#include "engine/framework/runtime/session.h"
#include <cstddef>
#include <optional>
#include <string>

namespace audiocpp_backend {
constexpr std::size_t kMusicAbcMaxBytes = 256 * 1024;
enum class MusicMappingError { None, InvalidScore, InvalidAudio };
struct MusicResultMapping {
    MusicMappingError error = MusicMappingError::InvalidScore;
    std::optional<std::string> abc;
    bool ok() const { return error == MusicMappingError::None; }
};
// Pure extraction from current-pin structs. No parser, file I/O, WAV decode,
// publication, cancellation or readiness guarantee. Errors carry no native data.
MusicResultMapping map_music_transcription(const engine::runtime::TaskResult &result);
// Validates the in-memory audio contract only. Caller still owns result audio;
// Task4 must bound encoding/writing and independently validate decoded WAV.
// Externally supplied ABC is preserved by the service, not reconstructed here.
MusicResultMapping map_music_generation(const engine::runtime::TaskResult &result);
} // namespace audiocpp_backend
