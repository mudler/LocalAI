// SPDX-License-Identifier: MIT

#include <cstdio>
#include <string>

#include "transcription_prompt.h"

static int failures = 0;

static void check(bool ok, const char * name) {
    if (!ok) {
        ++failures;
        std::fprintf(stderr, "FAIL: %s\n", name);
    }
}

// Qwen3-ASR keeps the default preset: no system turn, the fixed user text.
static const char * kQwenUser = "Transcribe audio to text";

static void test_qwen_preset_without_options_is_unchanged() {
    const auto m = llama_grpc::build_transcription_messages(
        "", kQwenUser, false, "", "");

    check(m.system.empty(), "qwen preset adds no system turn");
    check(m.user == kQwenUser, "qwen preset keeps the fixed user text");
}

static void test_qwen_preset_appends_language_hint() {
    const auto m = llama_grpc::build_transcription_messages(
        "", kQwenUser, false, "fr", "");

    check(m.user == "Transcribe audio to text (language: fr)",
          "qwen preset appends the language hint");
}

static void test_qwen_preset_appends_caller_prompt_after_newline() {
    // LocalAI has always appended the caller prompt to the preset text, so
    // Qwen3-ASR requests keep exactly the prompt they had before.
    const auto m = llama_grpc::build_transcription_messages(
        "", kQwenUser, false, "fr", "Names: Kennedy");

    check(m.user == "Transcribe audio to text (language: fr)\nNames: Kennedy",
          "qwen preset appends the caller prompt after a newline");
}

static void test_lfm3_preset_puts_language_in_system_turn() {
    // LFM3 names the language in the system turn and sends an empty user turn,
    // so no "(language: ...)" suffix may appear in the user text.
    const auto m = llama_grpc::build_transcription_messages(
        "Perform ASR in French.", "", true, "fr", "");

    check(m.system == "Perform ASR in French.", "lfm3 preset keeps its system turn");
    check(m.user.empty(), "lfm3 user turn is empty (media only)");
}

static void test_lfm3_preset_with_caller_prompt_has_no_leading_newline() {
    const auto m = llama_grpc::build_transcription_messages(
        "Perform ASR.", "", true, "", "Speaker is a nurse");

    check(m.user == "Speaker is a nurse", "empty preset user takes the caller prompt as is");
}

static void test_lfm3_preset_without_language_or_prompt() {
    const auto m = llama_grpc::build_transcription_messages(
        "Perform ASR.", "", true, "", "");

    check(m.system == "Perform ASR.", "lfm3 auto-detect keeps the generic system turn");
    check(m.user.empty(), "lfm3 auto-detect has an empty user turn");
}

int main() {
    test_qwen_preset_without_options_is_unchanged();
    test_qwen_preset_appends_language_hint();
    test_qwen_preset_appends_caller_prompt_after_newline();
    test_lfm3_preset_puts_language_in_system_turn();
    test_lfm3_preset_with_caller_prompt_has_no_leading_newline();
    test_lfm3_preset_without_language_or_prompt();

    if (failures == 0) {
        std::printf("transcription_prompt_test: all checks passed\n");
    }
    return failures;
}
