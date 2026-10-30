// SPDX-License-Identifier: MIT

#pragma once

#include <string>

namespace llama_grpc {

// The chat messages of one transcription request, before the audio marker.
// An empty system string means "no system turn".
struct transcription_messages {
    std::string system;
    std::string user;
};

// Builds the text of an ASR request from the model's ASR preset
// (common_chat_get_asr_prompt). Mirrors convert_transcriptions_to_chatcmpl
// upstream, with one deliberate difference: a caller prompt is appended to the
// preset's user text, not substituted for it. That is LocalAI's historical
// behaviour, so Qwen3-ASR requests keep their exact prompt.
//
// preset_has_language is true when the preset already names the language (LFM3
// writes it into the system turn), in which case no "(language: ...)" suffix is
// added to the user text.
inline transcription_messages build_transcription_messages(
        const std::string & preset_system,
        const std::string & preset_user,
        bool preset_has_language,
        const std::string & language,
        const std::string & prompt) {
    transcription_messages out;
    out.system = preset_system;
    out.user   = preset_user;

    if (!language.empty() && !preset_has_language) {
        out.user += " (language: " + language + ")";
    }
    if (!prompt.empty()) {
        // Only join with a newline when there is preset text to join to, so a
        // model with an empty user turn (LFM3) gets the caller prompt as is.
        if (!out.user.empty()) {
            out.user += "\n";
        }
        out.user += prompt;
    }
    return out;
}

} // namespace llama_grpc
