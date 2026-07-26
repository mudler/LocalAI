// Unit tests for family_gate. Standard library only. The harness compiles this
// as a single translation unit, so the implementation is included directly.
//
// This unit is the guard against issue #9287: when a model config has no
// explicit backend, LocalAI probes every installed backend and binds to the
// first Load that succeeds. Accepting an arbitrary GGUF here would capture
// unrelated LLMs.

#include "family_gate.cpp"

#include <cstdio>
#include <string>

static int failures = 0;

static void check(bool ok, const std::string &name) {
    if (!ok) {
        failures++;
        fprintf(stderr, "FAIL: %s\n", name.c_str());
    } else {
        fprintf(stderr, "ok:   %s\n", name.c_str());
    }
}

using namespace audiocpp_backend;

static void test_gguf_suffix_detection() {
    check(path_looks_like_gguf("/models/chatterbox-q8_0.gguf"), "plain .gguf");
    check(path_looks_like_gguf("/models/CHATTERBOX.GGUF"), "uppercase .GGUF");
    check(path_looks_like_gguf("/models/x.GgUf"), "mixed case .GgUf");
    check(path_looks_like_gguf("a.gguf"), "a one character stem is still a stem");
    check(!path_looks_like_gguf("/models/chatterbox"), "extensionless directory");
    check(!path_looks_like_gguf("/models/model.safetensors"), "safetensors");
    check(!path_looks_like_gguf("/models/gguf"), "a name that is merely 'gguf'");
    check(!path_looks_like_gguf("/models/GGUF"), "an uppercase name that is merely 'GGUF'");
    check(!path_looks_like_gguf(""), "empty path");
    check(!path_looks_like_gguf(".gguf"), "a bare extension is not a model file");
    // The suffix has to be at the end. A prefix or infix match would let
    // ".gguf.tmp" download artefacts and ".ggufx" siblings through.
    check(!path_looks_like_gguf("/models/model.gguf.tmp"), ".gguf in the middle");
    check(!path_looks_like_gguf("/models/model.ggufx"), "a longer extension");
    // Every character of the suffix has to match, including the last one.
    check(!path_looks_like_gguf("/models/model.ggug"), "a near miss in the final character");
    check(!path_looks_like_gguf("/models/model_gguf"), "a near miss in the first character");
    check(!path_looks_like_gguf("/models/.gguf-notes"), "a leading .gguf");
}

static void test_explicit_family_always_wins() {
    // Explicit configuration beats metadata, so a user can force a family when
    // upstream metadata is wrong or absent.
    const auto gguf = decide_family(true, "chatterbox", "omnivoice");
    check(gguf.ok && gguf.family == "omnivoice", "explicit family overrides GGUF metadata");
    check(gguf.error.empty(), "an accepted decision carries no error text");

    const auto dir = decide_family(false, "", "qwen3_tts");
    check(dir.ok && dir.family == "qwen3_tts", "explicit family satisfies a directory path");

    // A GGUF with no embedded spec is still loadable when the user names the
    // family: the option is an override, not a tie-break that needs metadata to
    // break against.
    const auto bare = decide_family(true, "", "supertonic");
    check(bare.ok && bare.family == "supertonic",
          "explicit family rescues a GGUF that carries no spec");
}

static void test_gguf_metadata_supplies_the_family() {
    const auto d = decide_family(true, "nemotron_asr", "");
    check(d.ok, "an audio.cpp GGUF loads with no family option");
    check(d.family == "nemotron_asr", "family comes from the embedded spec");
    check(d.error.empty(), "an accepted GGUF carries no error text");
}

// THE GATE. A llama.cpp GGUF has no audiocpp.model_spec.family key.
static void test_foreign_gguf_is_refused() {
    const auto d = decide_family(true, "", "");
    check(!d.ok, "a GGUF with no audio.cpp spec is refused");
    check(d.family.empty(), "no family is guessed");
    check(!d.error.empty(), "a refusal always says why");
    check(d.error.find("audiocpp.model_spec.family") != std::string::npos,
          "error names the missing metadata key so the cause is diagnosable");
    check(d.error.find("family:") != std::string::npos,
          "error names the option that would override it");
}

static void test_directory_without_family_is_refused() {
    const auto d = decide_family(false, "", "");
    check(!d.ok, "a non-GGUF path with no family option is refused");
    check(d.family.empty(), "a refused directory guesses no family");
    check(d.error.find("family:") != std::string::npos,
          "error names the required option");
}

// A directory path never consults embedded metadata, because there is no single
// GGUF to read it from.
static void test_directory_ignores_embedded_family() {
    const auto d = decide_family(false, "chatterbox", "");
    check(!d.ok, "a directory is refused even when an embedded family is supplied");
    check(d.family.empty(), "a refused directory does not adopt the embedded family");
    // If the GGUF branch ever leaked into the directory branch this message
    // would start blaming a metadata key that a directory has no place to carry.
    check(d.error.find("audiocpp.model_spec.family") == std::string::npos,
          "a directory refusal does not blame GGUF metadata it could not have");
}

int main() {
    test_gguf_suffix_detection();
    test_explicit_family_always_wins();
    test_gguf_metadata_supplies_the_family();
    test_foreign_gguf_is_refused();
    test_directory_without_family_is_refused();
    test_directory_ignores_embedded_family();
    if (failures) {
        fprintf(stderr, "%d check(s) failed\n", failures);
        return 1;
    }
    fprintf(stderr, "all family_gate checks passed\n");
    return 0;
}
