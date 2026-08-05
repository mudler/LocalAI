// SPDX-License-Identifier: MIT

#include <cstdio>
#include <map>
#include <string>

#include "tts_request_options.h"

static int failures = 0;

static void check(bool ok, const char * name) {
    if (!ok) {
        ++failures;
        std::fprintf(stderr, "FAIL: %s\n", name);
    }
}

static void test_accepts_a_minimal_valid_request() {
    const auto opts = llama_grpc::parse_tts_request_options(
        "Hello world", "/models/voices/ref.wav", "en", {});

    check(opts.ok, "minimal request is accepted");
    check(opts.error.empty(), "minimal request has no error");
    check(opts.text == "Hello world", "text passes through");
    check(opts.voice_path == "/models/voices/ref.wav", "voice path passes through");
    check(opts.language == "en", "language passes through");
    check(opts.top_k == 0, "top_k defaults to the unset sentinel");
    check(opts.top_p == 0.0f, "top_p defaults to the unset sentinel");
}

static void test_rejects_empty_text() {
    const auto opts = llama_grpc::parse_tts_request_options(
        "", "/models/voices/ref.wav", "en", {});

    check(!opts.ok, "empty text is rejected");
    check(opts.error.find("text") != std::string::npos, "empty-text error names the field");
}

static void test_rejects_missing_speaker_reference() {
    // Qwen3-TTS Base has no built-in speaker; without a reference it produces
    // an arbitrary voice, so this must be a hard error rather than a surprise.
    const auto opts = llama_grpc::parse_tts_request_options(
        "Hello world", "", "en", {});

    check(!opts.ok, "missing voice is rejected");
    check(opts.error.find("voice") != std::string::npos, "missing-voice error names the field");
}

static void test_parses_sampling_params() {
    const std::map<std::string, std::string> params{
        {"top_k", "40"},
        {"top_p", "0.85"},
    };
    const auto opts = llama_grpc::parse_tts_request_options(
        "Hello world", "/models/voices/ref.wav", "", params);

    check(opts.ok, "sampling params are accepted");
    check(opts.top_k == 40, "top_k is parsed");
    check(opts.top_p > 0.849f && opts.top_p < 0.851f, "top_p is parsed");
    check(opts.language.empty(), "absent language stays empty");
}

static void test_rejects_malformed_sampling_params() {
    const auto bad_top_k = llama_grpc::parse_tts_request_options(
        "Hello world", "/models/voices/ref.wav", "", {{"top_k", "forty"}});
    check(!bad_top_k.ok, "non-numeric top_k is rejected");
    check(bad_top_k.error.find("top_k") != std::string::npos, "top_k error names the field");

    const auto bad_top_p = llama_grpc::parse_tts_request_options(
        "Hello world", "/models/voices/ref.wav", "", {{"top_p", ""}});
    check(!bad_top_p.ok, "empty top_p is rejected");

    const auto trailing = llama_grpc::parse_tts_request_options(
        "Hello world", "/models/voices/ref.wav", "", {{"top_k", "40abc"}});
    check(!trailing.ok, "top_k with trailing garbage is rejected");
}

static void test_ignores_unknown_params() {
    // Unknown keys are backend-specific knobs meant for other TTS engines. A
    // request routed here must not fail just because it carries them.
    const auto opts = llama_grpc::parse_tts_request_options(
        "Hello world", "/models/voices/ref.wav", "", {{"exaggeration", "0.7"}});

    check(opts.ok, "unknown params are ignored, not rejected");
}

int main() {
    test_accepts_a_minimal_valid_request();
    test_rejects_empty_text();
    test_rejects_missing_speaker_reference();
    test_parses_sampling_params();
    test_rejects_malformed_sampling_params();
    test_ignores_unknown_params();

    if (failures == 0) {
        std::printf("tts_request_options_test: all checks passed\n");
    }
    return failures;
}
