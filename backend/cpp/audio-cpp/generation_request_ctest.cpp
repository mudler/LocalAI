// Tests for the TTS and SoundGeneration request builders, and for the
// filesystem rule that decides whether TTSRequest.voice is a speaker reference
// clip or a named preset.
//
// NAMED _ctest AND NOT _test ON PURPOSE: see the note at the top of
// result_map_ctest.cpp. This file needs the generated protobuf messages and the
// audio.cpp include path, neither of which backend/cpp/run-unit-tests.sh
// provides, so it is built and run by ctest:
//
//   make -C backend/cpp/audio-cpp test-engine
//
// The assertions on OPTION KEYS are the point of this file, not decoration.
// Every one of them names a key that was grepped against the pinned upstream:
// "instruct" is read, "instructions" is not; "duration_seconds" is read,
// "duration" is not. A rename that looks harmless is exactly the change that
// silently stops a family honouring the request, so the spellings are pinned
// here rather than left to a comment.

#include "generation_request.h"

#include <cstdio>
#include <filesystem>
#include <fstream>
#include <string>
#include <unordered_map>

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

static bool has_key(const std::unordered_map<std::string, std::string> &options,
                    const std::string &key) {
    return options.find(key) != options.end();
}

static std::string option_or(
    const std::unordered_map<std::string, std::string> &options,
    const std::string &key, const std::string &fallback) {
    const auto it = options.find(key);
    return it == options.end() ? fallback : it->second;
}

static engine::runtime::AudioBuffer clip(int sample_rate, int channels) {
    engine::runtime::AudioBuffer buffer;
    buffer.sample_rate = sample_rate;
    buffer.channels = channels;
    // Distinguishable content, so a builder that swapped one buffer for another
    // (or default-constructed one) is visible rather than merely a size change.
    buffer.samples = {0.25f, -0.5f, 0.75f, -1.0f};
    return buffer;
}

static void test_voice_is_reference_file() {
    const auto dir = std::filesystem::temp_directory_path() /
                     "audiocpp-generation-request-ctest";
    std::filesystem::remove_all(dir);
    std::filesystem::create_directories(dir);

    const auto file = dir / "reference.wav";
    {
        std::ofstream out(file, std::ios::binary);
        out << "not really a wav, but a regular file";
    }
    const auto subdir = dir / "a-directory";
    std::filesystem::create_directories(subdir);

    check(!voice_is_reference_file(""), "voice_is_reference_file: empty");
    check(!voice_is_reference_file("alloy"),
          "voice_is_reference_file: bare preset name");
    check(!voice_is_reference_file((dir / "absent.wav").string()),
          "voice_is_reference_file: missing path");
    check(voice_is_reference_file(file.string()),
          "voice_is_reference_file: existing regular file");
    // A directory is NOT a reference. exists() would say yes here and the read
    // would then fail with "cannot read <dir> as WAV", which sends the operator
    // after a file problem instead of a preset typo.
    check(!voice_is_reference_file(subdir.string()),
          "voice_is_reference_file: directory is not a reference");

    std::filesystem::remove_all(dir);
}

// build_tts_shape is what TTS and TTSStream both hand to routing, so a wrong
// answer here silently changes which task a request runs as, with a 200 and no
// diagnostic. Every field is asserted in both directions.
static void test_tts_shape() {
    const auto dir = std::filesystem::temp_directory_path() /
                     "audiocpp-generation-request-ctest-shape";
    std::filesystem::remove_all(dir);
    std::filesystem::create_directories(dir);
    const auto file = dir / "reference.wav";
    {
        std::ofstream out(file, std::ios::binary);
        out << "a regular file";
    }

    {
        backend::TTSRequest request;
        request.set_text("hello");
        const auto shape = build_tts_shape(request);
        check(!shape.has_voice_reference && !shape.has_instructions,
              "shape: bare request has neither signal");
        // Never filled here: it comes off the LoadedModel, and leaving it empty
        // is what makes the caller's assignment visible at the call site.
        check(shape.pinned_task.empty(), "shape: pinned_task is left to the caller");
    }
    {
        backend::TTSRequest request;
        request.set_voice(file.string());
        const auto shape = build_tts_shape(request);
        check(shape.has_voice_reference,
              "shape: an existing file is a voice reference");
        check(!shape.has_instructions, "shape: a clip is not an instruction");
    }
    {
        backend::TTSRequest request;
        request.set_voice("alloy");
        const auto shape = build_tts_shape(request);
        check(!shape.has_voice_reference,
              "shape: a preset name is not a voice reference");
    }
    {
        backend::TTSRequest request;
        (*request.mutable_params())["multi_reference_cond"] =
            R"([{"audio":"one.wav","text":"one"}])";
        const auto shape = build_tts_shape(request);
        check(shape.has_voice_reference,
              "shape: multi-reference conditioning is a voice reference");
    }
    {
        backend::TTSRequest request;
        request.set_voice(dir.string());
        const auto shape = build_tts_shape(request);
        check(!shape.has_voice_reference,
              "shape: a directory is not a voice reference");
    }
    {
        backend::TTSRequest request;
        request.set_instructions("a calm older man");
        const auto shape = build_tts_shape(request);
        check(shape.has_instructions, "shape: instructions are seen");
        check(!shape.has_voice_reference,
              "shape: instructions do not imply a reference");
    }
    {
        // The guard that has to match build_tts_request's. An empty
        // instructions string builds no style condition, so telling routing to
        // prefer VoiceDesign for it would route to a task with nothing to
        // design from.
        backend::TTSRequest request;
        request.set_instructions("");
        const auto shape = build_tts_shape(request);
        check(!shape.has_instructions,
              "shape: an empty instructions string is not an instruction");
    }
    {
        backend::TTSRequest request;
        request.set_voice(file.string());
        request.set_instructions("a calm older man");
        const auto shape = build_tts_shape(request);
        check(shape.has_voice_reference && shape.has_instructions,
              "shape: both signals are reported when both are set");
    }

    std::filesystem::remove_all(dir);
}

static void test_tts_plain() {
    backend::TTSRequest request;
    request.set_text("hello there");

    const auto task = build_tts_request(request, std::nullopt);

    check(task.text_input.has_value() && task.text_input->text == "hello there",
          "tts: text reaches the transcript");
    // No voice and no instructions means NO voice condition at all. A builder
    // that always emitted one would make every family think a speaker was
    // named, and chatterbox in particular refuses a prepare whose voice
    // condition carries neither audio nor anything else it can use.
    check(!task.voice.has_value(), "tts: no voice condition when nothing is set");
    check(task.options.empty(), "tts: no options when nothing is set");
    check(!task.audio_input.has_value(), "tts: no audio input");
}

static void test_tts_named_preset() {
    backend::TTSRequest request;
    request.set_text("hello");
    request.set_voice("alloy");

    const auto task = build_tts_request(request, std::nullopt);

    check(task.voice.has_value() && task.voice->speaker.has_value(),
          "tts preset: speaker condition present");
    check(task.voice->speaker->cached_voice_id.has_value() &&
              *task.voice->speaker->cached_voice_id == "alloy",
          "tts preset: lands in cached_voice_id");
    // The clip slot must stay empty, or a cloning family would try to prepare
    // conditionals from a default-constructed buffer.
    check(!task.voice->speaker->audio.has_value(),
          "tts preset: no reference audio");
    check(!task.voice->style.has_value(), "tts preset: no style condition");
    check(option_or(task.options, "voice", "") == "alloy",
          "tts preset: forwarded as the voice option too");
}

static void test_tts_reference_clip() {
    backend::TTSRequest request;
    request.set_text("hello");
    request.set_voice("/tmp/reference.wav");

    const auto task = build_tts_request(request, clip(44100, 2));

    check(task.voice.has_value() && task.voice->speaker.has_value(),
          "tts clip: speaker condition present");
    check(task.voice->speaker->audio.has_value(),
          "tts clip: reference audio present");
    // Rate and channels survive untouched. This is the assertion that fails if
    // anybody decides to fold the clip to 16 kHz mono on the way in.
    check(task.voice->speaker->audio->sample_rate == 44100 &&
              task.voice->speaker->audio->channels == 2 &&
              task.voice->speaker->audio->samples.size() == 4,
          "tts clip: rate, channels and samples pass through unchanged");
    // A clip is NOT also a cached voice id, and the path must not travel as a
    // preset name: a family reading cached_voice_id would then look up a voice
    // called "/tmp/reference.wav".
    check(!task.voice->speaker->cached_voice_id.has_value(),
          "tts clip: no cached_voice_id");
    check(!has_key(task.options, "voice"), "tts clip: no voice option");
}

static void test_tts_instructions() {
    backend::TTSRequest request;
    request.set_text("hello");
    request.set_instructions("a calm older man, speaking slowly");

    const auto task = build_tts_request(request, std::nullopt);

    check(option_or(task.options, "instruct", "") ==
              "a calm older man, speaking slowly",
          "tts instructions: instruct option is the one qwen3_tts reads");
    check(option_or(task.options, "caption", "") ==
              "a calm older man, speaking slowly",
          "tts instructions: caption option is the one irodori_tts reads");
    check(option_or(task.options, "instructions", "") ==
              "a calm older man, speaking slowly",
          "tts instructions: proto field name forwarded as an alias");
    check(task.voice.has_value() && task.voice->style.has_value(),
          "tts instructions: style condition present");
    // "instruct", not "instructions". omnivoice and qwen3_tts both look this tag
    // up by that exact name and by no other.
    check(option_or(task.voice->style->tags, "instruct", "") ==
              "a calm older man, speaking slowly",
          "tts instructions: style tag is spelled instruct");
    check(!has_key(task.voice->style->tags, "instructions"),
          "tts instructions: style tag is NOT spelled instructions");
    // Instructions alone must not invent a speaker: has_voice_reference is what
    // routing keys VoiceCloning off, and a speaker here would make a
    // clone-capable family expect a clip it never received.
    check(!task.voice->speaker.has_value(),
          "tts instructions: no speaker without a voice");
}

static void test_tts_empty_instructions_are_not_instructions() {
    backend::TTSRequest request;
    request.set_text("hello");
    request.set_instructions("");

    const auto task = build_tts_request(request, std::nullopt);

    // has_instructions() is true here, because the field was set. An empty
    // string is still no instruction, and forwarding it would set an empty
    // instruct option that qwen3_tts would prefer over its style tag fallback.
    check(!has_key(task.options, "instruct"),
          "tts: an empty instructions string sets no instruct option");
    check(!task.voice.has_value(),
          "tts: an empty instructions string sets no voice condition");
}

// THE EXACT SHAPE LocalAI PUTS ON THE WIRE. core/backend/tts.go's newTTSRequest
// sets Language: &language UNCONDITIONALLY, so has_language() is true on every
// request that ever reaches this backend, carrying an empty string whenever the
// caller named no language.
//
// An empty StyleCondition::language is not a harmless default. supertonic reads
// text_input->language behind a !empty() guard and then OVERRIDES it from
// style->language whenever that optional is engaged, with no guard at all
// (supertonic/session.cpp generation_options_from_request), so an empty style
// language replaces its "en" default (session.h) with "" and
// tokenizer_text.cpp's preprocess throws "invalid Supertonic language: ".
// Every /v1/audio/speech request carrying instructions and no language would be
// an INTERNAL. A plain request never sees it, because the style condition only
// exists when instructions are non-empty.
static void test_tts_empty_language_is_not_a_language() {
    backend::TTSRequest request;
    request.set_text("hello");
    request.set_instructions("a calm older man");
    request.set_language("");

    const auto task = build_tts_request(request, std::nullopt);

    check(task.voice.has_value() && task.voice->style.has_value(),
          "tts empty language: the style condition still exists");
    check(!task.voice->style->language.has_value(),
          "tts empty language: style language is left unset, not set to empty");
    // The option emission has always guarded on !empty(); this pins the two to
    // the same rule so they cannot drift apart again.
    check(!has_key(task.options, "language"),
          "tts empty language: no language option");
    check(task.text_input.has_value() && task.text_input->language.empty(),
          "tts empty language: transcript language stays empty");
}

// The one shape routing treats specially: a clip outranks instructions, and the
// VoiceCondition then has to carry BOTH, because the family that wins is chosen
// on the clip but may still read the style tag.
static void test_tts_clip_and_instructions() {
    backend::TTSRequest request;
    request.set_text("hello");
    request.set_voice("/tmp/reference.wav");
    request.set_instructions("bright and fast");
    request.set_language("en");

    const auto task = build_tts_request(request, clip(22050, 1));

    check(task.voice.has_value(), "tts clip+instructions: voice condition present");
    check(task.voice->speaker.has_value() &&
              task.voice->speaker->audio.has_value() &&
              task.voice->speaker->audio->sample_rate == 22050,
          "tts clip+instructions: speaker carries the clip");
    check(!task.voice->speaker->cached_voice_id.has_value(),
          "tts clip+instructions: the clip path is not also a preset id");
    check(task.voice->style.has_value() &&
              option_or(task.voice->style->tags, "instruct", "") == "bright and fast",
          "tts clip+instructions: style carries the instruct tag");
    check(task.voice->style->language.has_value() &&
              *task.voice->style->language == "en",
          "tts clip+instructions: a real language does reach the style condition");
    check(option_or(task.options, "instruct", "") == "bright and fast",
          "tts clip+instructions: instruct option still emitted");
    check(!has_key(task.options, "voice"),
          "tts clip+instructions: still no voice option for a clip");
}

static void test_tts_language_and_params() {
    backend::TTSRequest request;
    request.set_text("ciao");
    request.set_language("it");
    request.set_instructions("warm");
    (*request.mutable_params())["exaggeration"] = "0.7";
    // An explicit param must win over the value derived from instructions.
    (*request.mutable_params())["caption"] = "explicitly chosen caption";

    const auto task = build_tts_request(request, std::nullopt);

    check(task.text_input.has_value() && task.text_input->language == "it",
          "tts language: reaches the transcript");
    check(option_or(task.options, "language", "") == "it",
          "tts language: forwarded as an option alias");
    check(task.voice.has_value() && task.voice->style.has_value() &&
              task.voice->style->language.has_value() &&
              *task.voice->style->language == "it",
          "tts language: reaches the style condition");
    check(option_or(task.options, "exaggeration", "") == "0.7",
          "tts params: passed through verbatim");
    check(option_or(task.options, "caption", "") == "explicitly chosen caption",
          "tts params: an explicit param overrides the derived caption");
}

static void test_sound_generation_minimal() {
    backend::SoundGenerationRequest request;
    request.set_text("a distant thunderstorm");

    const auto task = build_sound_generation_request(request, std::nullopt);

    check(task.text_input.has_value() &&
              task.text_input->text == "a distant thunderstorm",
          "sound: text reaches the transcript");
    // Unset optionals must emit NOTHING. Emitting a zero for an unset duration
    // would make heartmula refuse the request ("duration_seconds must be
    // positive") on a request that never mentioned a duration.
    check(task.options.empty(), "sound: unset optionals emit no options");
    check(!task.audio_input.has_value(), "sound: no audio input without src");
    check(!task.voice.has_value(), "sound: no voice condition");
}

static void test_sound_generation_full() {
    backend::SoundGenerationRequest request;
    request.set_text("a slow blues in E");
    request.set_duration(30.0f);
    request.set_temperature(0.8f);
    request.set_sample(false);
    request.set_src_divisor(4);
    request.set_think(true);
    request.set_caption("smoky bar recording");
    request.set_lyrics("first line\nsecond line");
    request.set_bpm(72);
    request.set_keyscale("E minor");
    request.set_language("en");
    request.set_timesignature("4/4");
    request.set_instrumental(true);

    const auto task = build_sound_generation_request(request, clip(48000, 2));

    // duration_seconds is the key every AudioGeneration family actually reads;
    // "duration" rides along as an alias. Both spellings are pinned so a
    // "cleanup" that keeps only the proto's own name is a test failure and not
    // a silent loss of the duration.
    check(option_or(task.options, "duration_seconds", "").rfind("30.", 0) == 0,
          "sound: duration lands as duration_seconds");
    check(option_or(task.options, "duration", "").rfind("30.", 0) == 0,
          "sound: duration also forwarded under its own name");
    check(option_or(task.options, "temperature", "").rfind("0.8", 0) == 0,
          "sound: temperature forwarded");
    // Set to FALSE, so this also proves the key is written whenever the field is
    // present rather than only when the value is truthy.
    check(option_or(task.options, "do_sample", "") == "false",
          "sound: sample=false is forwarded as do_sample=false");
    check(option_or(task.options, "src_divisor", "") == "4",
          "sound: src_divisor forwarded");
    check(option_or(task.options, "thinking", "") == "true",
          "sound: think lands as thinking, the key ace_step reads");
    check(option_or(task.options, "think", "") == "true",
          "sound: think also forwarded under its own name");
    check(option_or(task.options, "caption", "") == "smoky bar recording",
          "sound: caption forwarded");
    check(option_or(task.options, "lyrics", "") == "first line\nsecond line",
          "sound: lyrics forwarded");
    check(option_or(task.options, "bpm", "") == "72", "sound: bpm forwarded");
    check(option_or(task.options, "keyscale", "") == "E minor",
          "sound: keyscale forwarded");
    check(option_or(task.options, "timesignature", "") == "4/4",
          "sound: timesignature forwarded");
    check(option_or(task.options, "instrumental", "") == "true",
          "sound: instrumental forwarded");
    check(option_or(task.options, "language", "") == "en",
          "sound: language forwarded as an option alias");
    check(task.text_input->language == "en",
          "sound: language reaches the transcript, which is what ace_step reads");
    check(task.audio_input.has_value() &&
              task.audio_input->sample_rate == 48000 &&
              task.audio_input->channels == 2 &&
              task.audio_input->samples.size() == 4,
          "sound: src passes through at its own rate and channel count");
}


// ---------------------------------------------------------------------------
// apply_transform_text_input
//
// AudioTransform has no text field on the wire, so a text-conditioned route
// (vevo2's speech-to-speech) can only be reached if the text travels as a
// param and is unpacked into text_input. Every assertion below pins a spelling
// or a precedence that a family actually depends on, not a shape that merely
// looks tidy.

static void test_transform_text_absent() {
    engine::runtime::TaskRequest task;
    task.options["stem"] = "vocals";
    check(!apply_transform_text_input(task),
          "transform text: reports false when no text key is present");
    check(!task.text_input.has_value(),
          "transform text: a request with no text keeps text_input unset");
}

static void test_transform_text_canonical_key() {
    engine::runtime::TaskRequest task;
    task.options["target_text"] = "sing this line";
    check(apply_transform_text_input(task), "transform text: target_text reports true");
    check(task.text_input.has_value() && task.text_input->text == "sing this line",
          "transform text: target_text becomes text_input.text");
    check(has_key(task.options, "target_text"),
          "transform text: target_text survives in options for families that read it there");
}

static void test_transform_text_alias_key() {
    engine::runtime::TaskRequest task;
    task.options["text"] = "say this instead";
    check(apply_transform_text_input(task), "transform text: text alias reports true");
    check(task.text_input.has_value() && task.text_input->text == "say this instead",
          "transform text: the text alias becomes text_input.text");
}

static void test_transform_text_canonical_wins() {
    engine::runtime::TaskRequest task;
    task.options["target_text"] = "canonical";
    task.options["text"] = "alias";
    check(apply_transform_text_input(task), "transform text: both keys reports true");
    check(task.text_input.has_value() && task.text_input->text == "canonical",
          "transform text: target_text wins over text, not whichever hashed first");
}

static void test_transform_text_empty_is_not_a_text() {
    engine::runtime::TaskRequest task;
    task.options["target_text"] = "";
    check(!apply_transform_text_input(task),
          "transform text: an empty target_text reports false");
    check(!task.text_input.has_value(),
          "transform text: an empty target_text leaves text_input unset");
}

static void test_transform_text_empty_canonical_falls_through_to_alias() {
    engine::runtime::TaskRequest task;
    task.options["target_text"] = "";
    task.options["text"] = "the real one";
    check(apply_transform_text_input(task),
          "transform text: an empty canonical key does not mask a usable alias");
    check(task.text_input.has_value() && task.text_input->text == "the real one",
          "transform text: the alias is used when the canonical key is empty");
}

static void test_transform_text_language_rides_along() {
    engine::runtime::TaskRequest task;
    task.options["target_text"] = "vocalise me";
    task.options["language"] = "ja";
    check(apply_transform_text_input(task), "transform text: text plus language reports true");
    check(task.text_input.has_value() && task.text_input->language == "ja",
          "transform text: language lands on the Transcript alongside the text");
    check(has_key(task.options, "language"),
          "transform text: language survives in options too");
}

static void test_transform_language_alone_is_not_a_text() {
    engine::runtime::TaskRequest task;
    task.options["language"] = "ja";
    check(!apply_transform_text_input(task),
          "transform text: a language with no text reports false");
    check(!task.text_input.has_value(),
          "transform text: a language alone must not route a separation request through text");
}

static void test_transform_text_preserves_other_inputs() {
    engine::runtime::TaskRequest task;
    engine::runtime::AudioBuffer audio;
    audio.sample_rate = 44100;
    audio.channels = 2;
    audio.samples = {0.1f, 0.2f, 0.3f, 0.4f};
    task.audio_input = audio;
    task.options["target_text"] = "keep the audio";
    check(apply_transform_text_input(task), "transform text: with audio present reports true");
    check(task.audio_input.has_value() && task.audio_input->samples.size() == 4 &&
              task.audio_input->sample_rate == 44100,
          "transform text: the source audio is untouched");
}

int main() {
    test_voice_is_reference_file();
    test_tts_shape();
    test_tts_plain();
    test_tts_named_preset();
    test_tts_reference_clip();
    test_tts_instructions();
    test_tts_empty_instructions_are_not_instructions();
    test_tts_empty_language_is_not_a_language();
    test_tts_clip_and_instructions();
    test_tts_language_and_params();
    test_sound_generation_minimal();
    test_sound_generation_full();
    test_transform_text_absent();
    test_transform_text_canonical_key();
    test_transform_text_alias_key();
    test_transform_text_canonical_wins();
    test_transform_text_empty_is_not_a_text();
    test_transform_text_empty_canonical_falls_through_to_alias();
    test_transform_text_language_rides_along();
    test_transform_language_alone_is_not_a_text();
    test_transform_text_preserves_other_inputs();

    if (failures != 0) {
        fprintf(stderr, "%d check(s) failed\n", failures);
        return 1;
    }
    fprintf(stderr, "all checks passed\n");
    return 0;
}
