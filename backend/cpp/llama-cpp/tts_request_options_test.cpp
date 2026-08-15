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
    check(opts.max_frames == 0, "max_frames defaults to the unset sentinel");
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

    const auto trailing_float = llama_grpc::parse_tts_request_options(
        "Hello world", "/models/voices/ref.wav", "", {{"top_p", "0.8abc"}});
    check(!trailing_float.ok, "top_p with trailing garbage is rejected");

    // std::stol returns a long, which is wider than int32_t on 64-bit hosts, so
    // an in-range-for-long value still has to be caught before the narrowing.
    const auto overflow_top_k = llama_grpc::parse_tts_request_options(
        "Hello world", "/models/voices/ref.wav", "", {{"top_k", "99999999999"}});
    check(!overflow_top_k.ok, "top_k beyond int32 range is rejected");
    check(overflow_top_k.error.find("top_k") != std::string::npos,
          "top_k overflow error names the field");
}

static void test_rejects_out_of_range_sampling_params() {
    // These reach mtmd_helper::gen_audio::inp unconditionally downstream, where
    // the "> 0" sampler guard does not screen them, so they must die here.
    const auto negative_top_k = llama_grpc::parse_tts_request_options(
        "Hello world", "/models/voices/ref.wav", "", {{"top_k", "-5"}});
    check(!negative_top_k.ok, "negative top_k is rejected");
    check(negative_top_k.error.find("top_k") != std::string::npos,
          "negative top_k error names the field");

    const auto negative_top_p = llama_grpc::parse_tts_request_options(
        "Hello world", "/models/voices/ref.wav", "", {{"top_p", "-0.1"}});
    check(!negative_top_p.ok, "negative top_p is rejected");
    check(negative_top_p.error.find("top_p") != std::string::npos,
          "negative top_p error names the field");

    const auto large_top_p = llama_grpc::parse_tts_request_options(
        "Hello world", "/models/voices/ref.wav", "", {{"top_p", "1.5"}});
    check(!large_top_p.ok, "top_p above 1.0 is rejected");

    // NaN survives a naive "p < 0.0f || p > 1.0f" range test because every
    // comparison against NaN is false. This case pins the correct form.
    const auto nan_top_p = llama_grpc::parse_tts_request_options(
        "Hello world", "/models/voices/ref.wav", "", {{"top_p", "nan"}});
    check(!nan_top_p.ok, "NaN top_p is rejected");

    const auto inf_top_p = llama_grpc::parse_tts_request_options(
        "Hello world", "/models/voices/ref.wav", "", {{"top_p", "inf"}});
    check(!inf_top_p.ok, "infinite top_p is rejected");
}

static void test_accepts_sampling_param_boundaries() {
    const auto zero_top_p = llama_grpc::parse_tts_request_options(
        "Hello world", "/models/voices/ref.wav", "", {{"top_p", "0.0"}});
    check(zero_top_p.ok, "top_p of 0.0 is accepted");
    check(zero_top_p.top_p == 0.0f, "top_p of 0.0 round-trips");

    const auto one_top_p = llama_grpc::parse_tts_request_options(
        "Hello world", "/models/voices/ref.wav", "", {{"top_p", "1.0"}});
    check(one_top_p.ok, "top_p of 1.0 is accepted");
    check(one_top_p.top_p == 1.0f, "top_p of 1.0 round-trips");

    const auto zero_top_k = llama_grpc::parse_tts_request_options(
        "Hello world", "/models/voices/ref.wav", "", {{"top_k", "0"}});
    check(zero_top_k.ok, "top_k of 0 is accepted");
}

static void test_parses_max_frames() {
    // The consumer maps a positive value onto n_predict and leaves upstream's
    // 512-frame default in place when it is unset, so the sentinel matters as
    // much as the parsed value.
    const auto opts = llama_grpc::parse_tts_request_options(
        "Hello world", "/models/voices/ref.wav", "", {{"max_frames", "120"}});

    check(opts.ok, "max_frames is accepted");
    check(opts.max_frames == 120, "max_frames is parsed");

    const auto absent = llama_grpc::parse_tts_request_options(
        "Hello world", "/models/voices/ref.wav", "", {{"top_k", "40"}});
    check(absent.ok, "a request without max_frames is accepted");
    check(absent.max_frames == 0, "absent max_frames leaves the unset sentinel");

    const auto zero = llama_grpc::parse_tts_request_options(
        "Hello world", "/models/voices/ref.wav", "", {{"max_frames", "0"}});
    check(zero.ok, "max_frames of 0 is accepted");
    check(zero.max_frames == 0, "max_frames of 0 means unset");
}

static void test_rejects_malformed_max_frames() {
    const auto negative = llama_grpc::parse_tts_request_options(
        "Hello world", "/models/voices/ref.wav", "", {{"max_frames", "-1"}});
    check(!negative.ok, "negative max_frames is rejected");
    check(negative.error.find("max_frames") != std::string::npos,
          "negative max_frames error names the field");

    const auto non_numeric = llama_grpc::parse_tts_request_options(
        "Hello world", "/models/voices/ref.wav", "", {{"max_frames", "many"}});
    check(!non_numeric.ok, "non-numeric max_frames is rejected");
    check(non_numeric.error.find("max_frames") != std::string::npos,
          "non-numeric max_frames error names the field");

    const auto trailing = llama_grpc::parse_tts_request_options(
        "Hello world", "/models/voices/ref.wav", "", {{"max_frames", "120abc"}});
    check(!trailing.ok, "max_frames with trailing garbage is rejected");

    const auto empty = llama_grpc::parse_tts_request_options(
        "Hello world", "/models/voices/ref.wav", "", {{"max_frames", ""}});
    check(!empty.ok, "empty max_frames is rejected");

    const auto overflow = llama_grpc::parse_tts_request_options(
        "Hello world", "/models/voices/ref.wav", "", {{"max_frames", "99999999999"}});
    check(!overflow.ok, "max_frames beyond int32 range is rejected");
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
    test_rejects_out_of_range_sampling_params();
    test_accepts_sampling_param_boundaries();
    test_parses_max_frames();
    test_rejects_malformed_max_frames();
    test_ignores_unknown_params();

    if (failures == 0) {
        std::printf("tts_request_options_test: all checks passed\n");
    }
    return failures;
}
