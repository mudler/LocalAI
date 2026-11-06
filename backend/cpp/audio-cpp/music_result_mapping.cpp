#include "music_result_mapping.h"

#include <cmath>
#include <cstdint>
#include <string_view>

namespace audiocpp_backend {
namespace {
namespace rt = engine::runtime;
bool meta_is(const rt::VoiceArtifact &a, const char *key, const char *value) {
    const auto it = a.meta.find(key);
    return it != a.meta.end() && it->second == value;
}
bool score_like(const rt::VoiceArtifact &a) {
    return a.id == "score" || meta_is(a, "mime", "text/vnd.abc") ||
           meta_is(a, "format", "abc") || meta_is(a, "extension", "abc");
}
// Locale-independent UTF-8 scalar validation: reject overlong encodings,
// surrogates, out-of-range scalars and incomplete sequences. No normalization.
bool valid_abc(std::string_view text) {
    if (text.empty() || text.size() > kMusicAbcMaxBytes) return false;
    bool nonblank = false;
    for (std::size_t i = 0; i < text.size();) {
        const auto c = static_cast<unsigned char>(text[i++]);
        if (c == 0) return false;
        if (c < 0x80) {
            if (c != ' ' && c != '\t' && c != '\r' && c != '\n' && c != '\f' && c != '\v') nonblank = true;
            continue;
        }
        nonblank = true;
        unsigned n = 0;
        uint32_t cp = 0, minimum = 0;
        if (c >= 0xc2 && c <= 0xdf) { n = 1; cp = c & 0x1f; minimum = 0x80; }
        else if (c >= 0xe0 && c <= 0xef) { n = 2; cp = c & 0x0f; minimum = 0x800; }
        else if (c >= 0xf0 && c <= 0xf4) { n = 3; cp = c & 7; minimum = 0x10000; }
        else return false;
        if (n > text.size() - i) return false;
        while (n--) {
            const auto next = static_cast<unsigned char>(text[i++]);
            if ((next & 0xc0) != 0x80) return false;
            cp = (cp << 6) | (next & 0x3f);
        }
        if (cp < minimum || cp > 0x10ffff || (cp >= 0xd800 && cp <= 0xdfff)) return false;
    }
    return nonblank;
}
std::optional<std::string> extract_score(const rt::VoiceArtifact &a, bool generated) {
    if (a.kind != rt::ArtifactKind::Custom || a.id != "score" ||
        !meta_is(a, "mime", "text/vnd.abc") || !meta_is(a, "format", "abc") ||
        !meta_is(a, "extension", "abc") || a.payload.empty() ||
        a.payload.size() > kMusicAbcMaxBytes) return std::nullopt;
    // Current SheetSage has neither field. Reject unexpected provenance rather
    // than silently treating a generated/truncated score as transcription.
    if (generated) {
        if (!meta_is(a, "source", "generated") || !meta_is(a, "truncated", "false")) return std::nullopt;
    } else if (a.meta.count("source") || a.meta.count("truncated")) return std::nullopt;
    const std::string_view text(reinterpret_cast<const char *>(a.payload.data()), a.payload.size());
    if (!valid_abc(text)) return std::nullopt;
    return std::string(text);
}
bool valid_audio(const rt::TaskResult &r) {
    if (!r.audio_output || !r.named_audio_outputs.empty()) return false;
    const auto &a = *r.audio_output;
    if (a.sample_rate <= 0 || (a.channels != 1 && a.channels != 2) || a.samples.empty()) return false;
    const auto channels = static_cast<std::size_t>(a.channels);
    if (a.samples.size() % channels != 0) return false;
    const auto frames = a.samples.size() / channels;
    if (static_cast<uint64_t>(frames) > static_cast<uint64_t>(a.sample_rate) * 300) return false;
    // Conservative PCM32 payload budget, not proof of any encoded WAV's size.
    if (a.samples.size() > (128ULL * 1024 * 1024 - 44) / sizeof(float)) return false;
    for (float sample : a.samples) if (!std::isfinite(sample)) return false;
    return true;
}
} // namespace

MusicResultMapping map_music_transcription(const rt::TaskResult &r) {
    if (!r.artifact_output || !r.text_output || r.text_output->language != "abc") return {};
    for (const auto &a : r.output_artifacts) if (score_like(a)) return {};
    auto abc = extract_score(*r.artifact_output, false);
    if (!abc || *abc != r.text_output->text) return {};
    return {MusicMappingError::None, std::move(abc)};
}

MusicResultMapping map_music_generation(const rt::TaskResult &r) {
    if (!valid_audio(r)) return {MusicMappingError::InvalidAudio, std::nullopt};
    // Current YuE has no transcript or singular score. Ambiguous alternate
    // layouts fail closed, even if their ABC bytes happen to agree.
    if (r.text_output || (r.artifact_output && score_like(*r.artifact_output))) return {};
    const rt::VoiceArtifact *score = nullptr;
    for (const auto &a : r.output_artifacts) {
        if (!score_like(a)) continue;
        if (score) return {};
        score = &a;
    }
    if (!score) return {MusicMappingError::None, std::nullopt};
    auto abc = extract_score(*score, true);
    if (!abc) return {};
    return {MusicMappingError::None, std::move(abc)};
}
} // namespace audiocpp_backend
