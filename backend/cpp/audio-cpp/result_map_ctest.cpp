// Tests for result_map, the engine-to-proto boundary.
//
// NAMED _ctest AND NOT _test ON PURPOSE. backend/cpp/run-unit-tests.sh globs
// every *_test.cpp under backend/cpp/ and compiles it as a single standalone
// translation unit with no include path beyond its own directory. This file
// needs backend.pb.h and the audio.cpp framework headers, so it is built and
// run by ctest instead:
//
//   make -C backend/cpp/audio-cpp test-engine
//
// Renaming it to *_test.cpp would break the standalone suite for every backend.
//
// What is worth testing here is exactly one thing, and it is not the field
// copying: THE RULE. TaskResult carries transcript text in text_output and
// nowhere else, so the proto's text must be that string verbatim. An earlier
// attempt at this backend derived it from the segments, which returns an empty
// transcript for every producer that reports segments without word timing.
// transcript_assembly already enforces the rule and is tested on its own; these
// checks are here so that a future edit cannot undo it at the boundary.

#include "result_map.h"

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
namespace rt = engine::runtime;

static const int kRate = 16000;

static rt::SpeechSegment speech(std::int64_t start, std::int64_t end) {
    rt::SpeechSegment segment;
    segment.span.start_sample = start;
    segment.span.end_sample = end;
    return segment;
}

static rt::SpeakerTurn turn(std::int64_t start, std::int64_t end,
                            const std::string &speaker) {
    rt::SpeakerTurn out;
    out.span.start_sample = start;
    out.span.end_sample = end;
    out.speaker_id = speaker;
    return out;
}

static rt::WordTimestamp word(std::int64_t start, std::int64_t end,
                              const std::string &text) {
    rt::WordTimestamp out;
    out.span.start_sample = start;
    out.span.end_sample = end;
    out.word = text;
    return out;
}

// THE REGRESSION. A diarized ASR result: real text, real speaker turns, and no
// word timing at all. This is the vibevoice_asr shape, and it is the one that
// came back empty before.
static void test_text_survives_segments_without_words() {
    rt::TaskResult result;
    rt::Transcript transcript;
    transcript.text = "hello there general kenobi";
    transcript.language = "en";
    result.text_output = transcript;
    result.speaker_turns.push_back(turn(0, 16000, "speaker_0"));
    result.speaker_turns.push_back(turn(16000, 32000, "speaker_1"));

    backend::TranscriptResult out;
    fill_transcript_result(result, kRate, 2.0f, &out);

    check(out.text() == "hello there general kenobi",
          "diarized result keeps text_output verbatim");
    check(out.language() == "en", "language comes from text_output");
    check(out.segments_size() == 2, "both speaker turns become segments");
    if (out.segments_size() == 2) {
        check(out.segments(0).speaker() == "speaker_0",
              "first segment keeps its own speaker label");
        check(out.segments(1).speaker() == "speaker_1",
              "second segment keeps its own speaker label");
        check(out.segments(1).start() == 1000000000LL,
              "segment start is nanoseconds, not samples");
        check(out.segments(1).end() == 2000000000LL,
              "segment end is nanoseconds, not samples");
    }
}

// The same rule seen from the other side: text present, spans present, and the
// per-segment text empty because there is nothing truthful to split. A boundary
// that derived the top-level text from these segments would produce "".
static void test_speech_segments_do_not_supply_the_text() {
    rt::TaskResult result;
    rt::Transcript transcript;
    transcript.text = "one two three";
    result.text_output = transcript;
    result.speech_segments.push_back(speech(0, 8000));
    result.speech_segments.push_back(speech(8000, 16000));

    backend::TranscriptResult out;
    fill_transcript_result(result, kRate, 1.0f, &out);

    check(out.text() == "one two three",
          "speech segments without words do not empty the transcript");
    check(out.segments_size() == 2, "both speech segments are emitted");
    if (out.segments_size() == 2) {
        check(out.segments(0).text().empty() && out.segments(1).text().empty(),
              "per-segment text stays empty when there is no word timing");
    }
}

static void test_words_reach_the_proto_in_nanoseconds() {
    rt::TaskResult result;
    rt::Transcript transcript;
    transcript.text = "hi there";
    result.text_output = transcript;
    result.word_timestamps.push_back(word(0, 8000, "hi"));
    result.word_timestamps.push_back(word(8000, 16000, "there"));

    backend::TranscriptResult out;
    fill_transcript_result(result, kRate, 1.0f, &out);

    check(out.text() == "hi there", "word-timed result keeps text_output");
    check(out.segments_size() == 1, "words with no spans yield one covering segment");
    if (out.segments_size() == 1) {
        const auto &segment = out.segments(0);
        check(segment.words_size() == 2, "both words are emitted");
        if (segment.words_size() == 2) {
            check(segment.words(0).text() == "hi", "first word text");
            check(segment.words(0).start() == 0, "first word start");
            check(segment.words(0).end() == 500000000LL,
                  "first word end is 0.5 s in nanoseconds");
            check(segment.words(1).start() == 500000000LL, "second word start");
            check(segment.words(1).end() == 1000000000LL, "second word end");
        }
    }
}

// The buffer's rate, not the file's, is what the spans mean. Passing 8000 for
// the same spans has to halve every timestamp, which is what makes resampling
// the input at read time load-bearing rather than cosmetic.
static void test_sample_rate_scales_the_timestamps() {
    rt::TaskResult result;
    rt::Transcript transcript;
    transcript.text = "x";
    result.text_output = transcript;
    result.speech_segments.push_back(speech(0, 8000));

    backend::TranscriptResult out;
    fill_transcript_result(result, 8000, 1.0f, &out);

    check(out.segments_size() == 1, "one segment at 8 kHz");
    if (out.segments_size() == 1) {
        check(out.segments(0).end() == 1000000000LL,
              "8000 samples at 8 kHz is one second");
    }
}

static void test_duration_is_carried_through() {
    rt::TaskResult result;
    rt::Transcript transcript;
    transcript.text = "x";
    result.text_output = transcript;

    backend::TranscriptResult out;
    fill_transcript_result(result, kRate, 14.07f, &out);

    check(out.duration() > 14.06f && out.duration() < 14.08f,
          "duration is set from the argument");
}

// No text output at all. A VAD-shaped result reaching this boundary must not
// invent a transcript, and must not overwrite a language the caller had already
// decided on.
static void test_missing_text_output_leaves_language_alone() {
    rt::TaskResult result;
    result.speech_segments.push_back(speech(0, 16000));

    backend::TranscriptResult out;
    out.set_language("it");
    fill_transcript_result(result, kRate, 1.0f, &out);

    check(out.text().empty(), "no text_output means no text");
    check(out.language() == "it",
          "a result with no text_output does not clear the language");
    check(out.segments_size() == 1, "spans are still emitted");
}

// Filling the same message twice must replace, not accumulate: the second
// call's ids restart at 0 and would collide with the first call's.
static void test_refilling_replaces_the_segments() {
    rt::TaskResult first;
    rt::Transcript transcript;
    transcript.text = "first";
    first.text_output = transcript;
    first.speech_segments.push_back(speech(0, 16000));
    first.speech_segments.push_back(speech(16000, 32000));

    backend::TranscriptResult out;
    fill_transcript_result(first, kRate, 2.0f, &out);

    rt::TaskResult second;
    rt::Transcript replacement;
    replacement.text = "second";
    second.text_output = replacement;
    second.speech_segments.push_back(speech(0, 16000));
    fill_transcript_result(second, kRate, 1.0f, &out);

    check(out.text() == "second", "the second fill replaces the text");
    check(out.segments_size() == 1,
          "the second fill replaces the segments instead of appending");
}

static void test_empty_result_is_empty() {
    rt::TaskResult result;
    backend::TranscriptResult out;
    fill_transcript_result(result, kRate, 0.0f, &out);

    check(out.text().empty(), "empty result has no text");
    check(out.segments_size() == 0, "empty result has no segments");
}

int main() {
    test_text_survives_segments_without_words();
    test_speech_segments_do_not_supply_the_text();
    test_words_reach_the_proto_in_nanoseconds();
    test_sample_rate_scales_the_timestamps();
    test_duration_is_carried_through();
    test_missing_text_output_leaves_language_alone();
    test_refilling_replaces_the_segments();
    test_empty_result_is_empty();
    if (failures) {
        fprintf(stderr, "%d check(s) failed\n", failures);
        return 1;
    }
    fprintf(stderr, "all result_map checks passed\n");
    return 0;
}
