// SPDX-License-Identifier: MIT

#pragma once

#include <cstdint>
#include <exception>
#include <map>
#include <string>

namespace llama_grpc {

// Validated, parsed form of a backend::TTSRequest, kept free of llama.cpp,
// mtmd and gRPC headers so backend/cpp/run-unit-tests.sh can compile it as a
// standalone translation unit. grpc-server.cpp turns this into a
// mtmd_helper::gen_audio::inp.
struct tts_request_options {
    bool        ok = false;
    std::string error;

    std::string text;
    std::string voice_path;
    std::string language;

    // 0 / 0.0f mean "unset": upstream only overrides the sampler defaults when
    // the value is strictly positive.
    int32_t top_k = 0;
    float   top_p = 0.0f;

    // Upper bound on generated audio frames, exposed because the model does not
    // always emit its codec EOS and will otherwise run to the 512-frame default,
    // which is roughly 41 s at the 12.5 Hz frame rate. 0 means unset, leaving
    // that default in place.
    int32_t max_frames = 0;
};

namespace detail {

// Strict whole-string numeric parsing. std::stoi/stof accept trailing garbage
// ("40abc" -> 40), which would silently honour a typo'd request.
inline bool parse_whole_int32(const std::string & value, int32_t & out) {
    if (value.empty()) {
        return false;
    }
    try {
        size_t     consumed = 0;
        const long parsed   = std::stol(value, &consumed);
        if (consumed != value.size()) {
            return false;
        }
        if (parsed < INT32_MIN || parsed > INT32_MAX) {
            return false;
        }
        out = static_cast<int32_t>(parsed);
        return true;
    } catch (const std::exception &) {
        return false;
    }
}

inline bool parse_whole_float(const std::string & value, float & out) {
    if (value.empty()) {
        return false;
    }
    try {
        size_t      consumed = 0;
        const float parsed   = std::stof(value, &consumed);
        if (consumed != value.size()) {
            return false;
        }
        out = parsed;
        return true;
    } catch (const std::exception &) {
        return false;
    }
}

inline tts_request_options reject(const std::string & message) {
    tts_request_options opts;
    opts.ok    = false;
    opts.error = message;
    return opts;
}

} // namespace detail

inline tts_request_options parse_tts_request_options(
        const std::string & text,
        const std::string & voice,
        const std::string & language,
        const std::map<std::string, std::string> & params) {
    if (text.empty()) {
        return detail::reject("text must be a non-empty string");
    }

    // The Qwen3-TTS Base checkpoints have no built-in speaker. Without a
    // reference clip the model picks an arbitrary voice, so an unset voice is
    // a request error rather than a defaulted one.
    if (voice.empty()) {
        return detail::reject("voice must name a speaker reference audio file");
    }

    tts_request_options opts;
    opts.text       = text;
    opts.voice_path = voice;
    opts.language   = language;

    // Both values are range-checked here rather than left to the caller: the
    // consumer copies them straight into mtmd_helper::gen_audio::inp, and only
    // its separate sampler assignment is guarded by "> 0". An out-of-range or
    // non-finite value would slip past that guard and reach llama.cpp.
    const auto top_k_it = params.find("top_k");
    if (top_k_it != params.end()) {
        if (!detail::parse_whole_int32(top_k_it->second, opts.top_k)) {
            return detail::reject("top_k must be an integer, got \"" + top_k_it->second + "\"");
        }
        if (opts.top_k < 0) {
            return detail::reject("top_k must be >= 0, got \"" + top_k_it->second + "\"");
        }
    }

    const auto top_p_it = params.find("top_p");
    if (top_p_it != params.end()) {
        if (!detail::parse_whole_float(top_p_it->second, opts.top_p)) {
            return detail::reject("top_p must be a number, got \"" + top_p_it->second + "\"");
        }
        // Phrased as a negated in-range test, not "p < 0.0f || p > 1.0f",
        // because every comparison against NaN is false: the obvious form
        // would accept NaN, and NaN then defeats the consumer's "> 0" guard
        // too, since that comparison is false as well.
        if (!(opts.top_p >= 0.0f && opts.top_p <= 1.0f)) {
            return detail::reject("top_p must be between 0.0 and 1.0, got \"" + top_p_it->second + "\"");
        }
    }

    const auto max_frames_it = params.find("max_frames");
    if (max_frames_it != params.end()) {
        if (!detail::parse_whole_int32(max_frames_it->second, opts.max_frames)) {
            return detail::reject("max_frames must be an integer, got \"" + max_frames_it->second + "\"");
        }
        if (opts.max_frames < 0) {
            return detail::reject("max_frames must be >= 0, got \"" + max_frames_it->second + "\"");
        }
    }

    opts.ok = true;
    return opts;
}

} // namespace llama_grpc
