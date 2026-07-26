// Unit tests for transcript_assembly. Standard library only. The harness
// compiles this as a single translation unit, so both implementations are
// included directly rather than linked.
//
// Every fixture below either mirrors a producer shape actually observed from
// audio.cpp families, and names the families it was checked against, or says in
// its own comment that it is defensive. Do not replace an observed shape with an
// invented one and do not quietly promote a defensive fixture to an observed
// one: an invented shape is what let the earlier attempt ship an empty
// transcript.

#include "audio_units.cpp"
#include "transcript_assembly.cpp"

#include <cstddef>
#include <cstdio>
#include <string>
#include <vector>

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

// Indexed access that reports a named failure instead of running off the end.
// std::vector::operator[] past the end is undefined behaviour, so a regression
// that drops a segment would crash the process here and take every later check
// with it. Returning a default element keeps the rest of the suite reporting.
static const OutSegment &segment_at(const AssembledTranscript &out, size_t index,
                                    const std::string &name) {
    static const OutSegment missing;
    if (index >= out.segments.size()) {
        failures++;
        fprintf(stderr, "FAIL: %s (segment %zu is missing)\n", name.c_str(), index);
        return missing;
    }
    return out.segments[index];
}

static const OutWord &word_at(const OutSegment &segment, size_t index,
                              const std::string &name) {
    static const OutWord missing;
    if (index >= segment.words.size()) {
        failures++;
        fprintf(stderr, "FAIL: %s (word %zu is missing)\n", name.c_str(), index);
        return missing;
    }
    return segment.words[index];
}

static const int kRate = 16000;

// Shape A: word timestamps only. Emitted by nemotron_asr, qwen3_asr and
// qwen3_forced_aligner, all of which set text_output plus word_timestamps and
// leave speech_segments empty.
static void test_words_only() {
    const std::vector<WordSpan> words = {
        {{0, 8000}, "hello"},
        {{8000, 16000}, "world"},
    };
    const auto out = assemble_transcript("hello world", {}, {}, words, kRate);

    check(out.text == "hello world", "text is text_output verbatim");
    check(out.segments.size() == 1, "words with no segments yield one segment");
    const OutSegment &first = segment_at(out, 0, "words only segment");
    check(first.start_ns == 0, "segment starts at the first word");
    check(first.end_ns == 1000000000LL, "segment ends at the last word");
    check(first.words.size() == 2, "both words attached");
    check(word_at(first, 0, "first word").text == "hello", "first word text");
    check(word_at(first, 1, "second word").start_ns == 500000000LL,
          "second word start in ns");
    check(first.text == "hello world", "segment text joins its words");
    check(first.id == 0, "ids are zero based");
}

// Shape A': the same producer, but text_output is punctuated and cased while
// the word timestamps are not. qwen3_asr rebuilds text_output from its word
// list only when timestamps are requested, so the two genuinely differ; this
// pins the sole segment's text to its words rather than to the top-level text.
static void test_words_only_with_punctuated_text_output() {
    const std::vector<WordSpan> words = {
        {{0, 8000}, "hello"},
        {{8000, 16000}, "world"},
    };
    const auto out = assemble_transcript("Hello, world!", {}, {}, words, kRate);

    check(out.text == "Hello, world!", "punctuated text_output is untouched");
    check(out.segments.size() == 1, "one segment");
    check(segment_at(out, 0, "punctuated segment").text == "hello world",
          "a segment with words takes its text from the words, not text_output");
}

// Shape A'': a merged word list whose last word is not the one that ends
// latest. audio.cpp concatenates per-chunk word lists in chunk order
// (append_chunk_word_timestamps in framework/audio/chunking.cpp). It drops a
// word whose global start falls before the chunk's keep span, but it never
// clips a word's end to that boundary, so the last word kept from one chunk can
// outlast the first word kept from the next. The covering span must therefore
// be the extent of every word, not the span from the first to the last.
static void test_covering_span_spans_every_word() {
    const std::vector<WordSpan> words = {
        {{0, 4000}, "a"},
        // Kept from the earlier chunk, ending past the chunk boundary.
        {{4000, 10000}, "b"},
        // First word of the next chunk, shorter, so it ends earlier.
        {{8000, 9000}, "c"},
    };
    const auto out = assemble_transcript("a b c", {}, {}, words, kRate);

    check(out.segments.size() == 1, "one covering segment");
    check(segment_at(out, 0, "covering segment").end_ns == 625000000LL,
          "the covering span reaches the latest word end, not the last word's");
}

// Defensive, not observed: no pinned family emits a word with no text.
// nemotron_asr's build_token_timestamps (models/nemotron_asr/decoder.cpp:97)
// skips a token that decodes to an empty chunk before it ever becomes a
// WordTimestamp. join_words guards against one anyway, and an unexercised guard
// is a guard the next reader deletes as dead weight.
static void test_empty_word_contributes_no_separator() {
    const std::vector<WordSpan> words = {
        {{0, 4000}, "alpha"},
        {{4000, 8000}, ""},
        {{8000, 12000}, "beta"},
    };
    const auto out = assemble_transcript("alpha beta", {}, {}, words, kRate);

    check(out.segments.size() == 1, "one segment");
    check(segment_at(out, 0, "sole segment").text == "alpha beta",
          "an empty word adds no separator to the segment text");
    check(segment_at(out, 0, "sole segment").words.size() == 3,
          "the empty word still reports its span");
}

// Shape B: speech segments, no words. Emitted by ASR families that report
// utterance boundaries without word-level timing.
static void test_segments_without_words() {
    const std::vector<Span> segments = {{0, 16000}, {16000, 32000}};
    const auto out = assemble_transcript("one two three", segments, {}, {}, kRate);

    // The regression: this must NOT be empty.
    check(out.text == "one two three", "multi-segment text is not empty");
    check(out.segments.size() == 2, "both segments survive");
    check(segment_at(out, 0, "first segment").end_ns == 1000000000LL,
          "first segment ends at 1s");
    check(segment_at(out, 1, "second segment").start_ns == 1000000000LL,
          "second segment starts at 1s");
    check(segment_at(out, 0, "first segment").text.empty(),
          "per-segment text stays empty when there are no words to split by");
    check(segment_at(out, 1, "second segment").id == 1, "ids increment");
}

// Shape C: speech segments plus speaker turns, no words. This is the real
// VibeVoice diarized ASR shape that broke the earlier attempt.
static void test_segments_with_speaker_turns_no_words() {
    const std::vector<Span> segments = {{0, 16000}, {16000, 32000}};
    const std::vector<SpeakerSpan> turns = {
        {{0, 16000}, "SPEAKER_00"},
        {{16000, 32000}, "SPEAKER_01"},
    };
    const auto out = assemble_transcript("hi there", segments, turns, {}, kRate);

    check(out.text == "hi there", "diarized multi-segment text is not empty");
    check(out.segments.size() == 2, "two segments");
    check(segment_at(out, 0, "first diarized segment").speaker == "SPEAKER_00",
          "first speaker assigned");
    check(segment_at(out, 1, "second diarized segment").speaker == "SPEAKER_01",
          "second speaker assigned");
}

// Defensive, not observed: speech segments and speaker turns that disagree.
// vibevoice_asr builds each SpeakerTurn with turn.span = speech_segment.span in
// one loop (models/vibevoice_asr/session.cpp:965) and shifts and clips both
// lists identically when merging chunks, so in practice the two lists are 1:1
// with identical spans. That is exactly why the shape C fixture above cannot
// show which list is the segment source: swapping the precedence there produces
// byte-identical output. This fixture pins the precedence, and it is the shape
// any future family that segments and diarizes separately would produce.
static void test_speech_segments_outrank_speaker_turns() {
    const std::vector<Span> segments = {{0, 32000}};
    const std::vector<SpeakerSpan> turns = {
        {{0, 16000}, "SPEAKER_00"},
        {{16000, 32000}, "SPEAKER_01"},
    };
    const auto out = assemble_transcript("hi there", segments, turns, {}, kRate);

    check(out.segments.size() == 1,
          "speech segments decide the segmentation, not speaker turns");
    check(segment_at(out, 0, "single utterance").end_ns == 2000000000LL,
          "the utterance keeps its own span");
}

// Defensive, not observed: no pinned family emits speaker turns and word
// timestamps together. It pins rule 2 against rule 3, which nothing else does:
// a diarized result is segmented by who spoke, and words only fill the turns in.
static void test_speaker_turns_outrank_words() {
    const std::vector<SpeakerSpan> turns = {
        {{0, 16000}, "SPEAKER_00"},
        {{16000, 32000}, "SPEAKER_01"},
    };
    const std::vector<WordSpan> words = {
        {{0, 8000}, "hi"},
        {{16000, 24000}, "there"},
    };
    const auto out = assemble_transcript("hi there", {}, turns, words, kRate);

    check(out.segments.size() == 2, "the two turns segment the result");
    check(segment_at(out, 0, "turn 0").text == "hi", "first turn takes its word");
    check(segment_at(out, 1, "turn 1").text == "there", "second turn takes its word");
}

// Shape D: text only. Emitted by ASR families that report no timing at all,
// such as hviske_asr and citrinet_asr.
static void test_text_only() {
    const auto out = assemble_transcript("just text", {}, {}, {}, kRate);

    check(out.text == "just text", "text survives");
    check(out.segments.size() == 1, "a single synthetic segment is emitted");
    const OutSegment &only = segment_at(out, 0, "synthetic segment");
    check(only.start_ns == 0 && only.end_ns == 0,
          "synthetic segment has zero span, not a fabricated duration");
    check(only.text == "just text", "the sole segment carries the full text");
}

// Shape E: speaker turns only, no speech segments and no text. This is
// sortformer_diar, reached through the Diarize RPC.
static void test_speaker_turns_only() {
    const std::vector<SpeakerSpan> turns = {
        {{0, 24000}, "0"},
        {{24000, 48000}, "1"},
    };
    const auto out = assemble_transcript("", {}, turns, {}, kRate);

    check(out.text.empty(), "no text is reported when the model produced none");
    check(out.segments.size() == 2, "turns become segments");
    check(segment_at(out, 0, "turn 0").speaker == "0",
          "speaker label preserved verbatim");
    check(segment_at(out, 1, "turn 1").start_ns == 1500000000LL,
          "second turn starts at 1.5s");
}

// Shape E', the same producer with one speaker talking over another.
// decode_sortformer_speaker_turns (models/sortformer_diar/postprocess.cpp)
// binarizes each speaker's probability track independently, which is the whole
// point of sortformer, then sorts the turns by start sample. So a turn can be
// wholly contained in another speaker's turn, and the containing turn always
// comes first. A segment sourced from a speaker turn must keep that turn's own
// label: re-deriving it by overlap can only ever tie with the containing turn,
// which then wins on order and silently erases the interjecting speaker.
static void test_nested_speaker_turn_keeps_its_own_label() {
    const std::vector<SpeakerSpan> turns = {
        {{0, 100000}, "speaker_0"},
        {{10000, 20000}, "speaker_1"},
    };
    const auto out = assemble_transcript("", {}, turns, {}, kRate);

    check(out.segments.size() == 2, "both turns become segments");
    check(segment_at(out, 0, "containing turn").speaker == "speaker_0",
          "the containing turn keeps its label");
    check(segment_at(out, 1, "nested turn").speaker == "speaker_1",
          "a turn nested inside another is not relabelled to the container");
}

// Shape F: nothing at all. A model that ran but produced no output must not
// crash or fabricate a segment.
static void test_empty() {
    const auto out = assemble_transcript("", {}, {}, {}, kRate);
    check(out.text.empty(), "empty stays empty");
    check(out.segments.empty(), "no segments are invented");
}

// Shape H: speech segments with no text and no words at all. This is the VAD
// path, silero_vad and marblenet_vad, which fill speech_segments and never
// touch text_output. It reaches the lone-segment rule with nothing to carry.
static void test_vad_segments_without_text() {
    const std::vector<Span> segments = {{0, 16000}, {24000, 32000}};
    const auto out = assemble_transcript("", segments, {}, {}, kRate);

    check(out.text.empty(), "VAD reports no text");
    check(out.segments.size() == 2, "both speech regions survive");
    check(segment_at(out, 1, "second speech region").start_ns == 1500000000LL,
          "second region starts at 1.5s");
    check(segment_at(out, 0, "first speech region").text.empty(),
          "a VAD segment carries no text");

    const std::vector<Span> one = {{0, 16000}};
    const auto single = assemble_transcript("", one, {}, {}, kRate);
    check(single.segments.size() == 1, "a single speech region survives");
    check(segment_at(single, 0, "lone speech region").text.empty(),
          "a lone VAD segment does not fabricate text");
}

// Shape G: segments and words together. Words are assigned by midpoint so a
// word straddling a boundary lands in exactly one segment.
static void test_words_distributed_into_segments() {
    const std::vector<Span> segments = {{0, 16000}, {16000, 32000}};
    const std::vector<WordSpan> words = {
        {{0, 4000}, "alpha"},
        {{4000, 8000}, "beta"},
        // Straddles the boundary; midpoint 16000 falls in the second segment.
        {{12000, 20000}, "gamma"},
        {{20000, 28000}, "delta"},
    };
    const auto out = assemble_transcript("alpha beta gamma delta", segments, {},
                                         words, kRate);

    check(out.text == "alpha beta gamma delta", "top level text unchanged");
    check(out.segments.size() == 2, "two segments");
    check(segment_at(out, 0, "first segment").words.size() == 2,
          "first segment takes two words");
    check(segment_at(out, 1, "second segment").words.size() == 2,
          "second segment takes two words");
    check(segment_at(out, 0, "first segment").text == "alpha beta",
          "first segment text");
    check(segment_at(out, 1, "second segment").text == "gamma delta",
          "boundary-straddling word lands by midpoint");
}

// The midpoint rule is not the same as either endpoint rule. "early" starts in
// the first segment but ends in the second, and "late" the other way round;
// each must land where its midpoint says, which no start-only or end-only rule
// reproduces.
static void test_words_assigned_by_midpoint_not_endpoint() {
    const std::vector<Span> segments = {{0, 16000}, {16000, 32000}};
    const std::vector<WordSpan> words = {
        // Midpoint 12000 -> first segment, although it ends in the second.
        {{4000, 20000}, "early"},
        // Midpoint 20000 -> second segment, although it starts in the first.
        {{12000, 28000}, "late"},
    };
    const auto out = assemble_transcript("early late", segments, {}, words, kRate);

    check(segment_at(out, 0, "first segment").text == "early",
          "a word ending past the boundary stays where its midpoint is");
    check(segment_at(out, 1, "second segment").text == "late",
          "a word starting before the boundary follows its midpoint");
}

// A word outside every segment must still be reachable rather than dropped
// silently, so it attaches to the nearest segment by midpoint distance.
static void test_word_outside_all_segments() {
    const std::vector<Span> segments = {{0, 16000}};
    const std::vector<WordSpan> words = {
        {{0, 8000}, "inside"},
        {{40000, 48000}, "outside"},
    };
    const auto out = assemble_transcript("inside outside", segments, {}, words,
                                         kRate);
    check(out.segments.size() == 1, "one segment");
    check(segment_at(out, 0, "sole segment").words.size() == 2,
          "the stray word is not dropped");
}

// The fallback picks the nearest segment, which is not the same as picking the
// first. With one segment the two are indistinguishable, so this uses three and
// puts the stray word past the last one.
static void test_stray_word_goes_to_the_nearest_segment() {
    const std::vector<Span> segments = {{0, 8000}, {8000, 16000}, {16000, 24000}};
    const std::vector<WordSpan> words = {
        // Midpoint 44000, nearest the third segment.
        {{40000, 48000}, "trailing"},
    };
    const auto out = assemble_transcript("trailing", segments, {}, words, kRate);

    check(segment_at(out, 0, "first segment").words.empty(),
          "the stray word does not fall back to the first segment");
    check(segment_at(out, 2, "third segment").text == "trailing",
          "the stray word attaches to the nearest segment");
}

// "Nearest" is measured from the segment's midpoint, and it is neither "the
// first segment" nor "the last". A leading stray word is the case a
// trailing-only fixture cannot reach: forced-aligner words scored against VAD
// segments produce one, and with only trailing coverage it would land at the
// end of the transcript with the suite green. Here the leading word's nearest
// midpoint is the first segment while its nearest start is the second, and the
// trailing word's nearest midpoint is the third while its nearest end is the
// second, so no endpoint rule reproduces this assignment either.
static void test_stray_word_distance_is_measured_from_the_midpoint() {
    const std::vector<Span> segments = {{0, 2000}, {8000, 200000}, {300000, 302000}};
    const std::vector<WordSpan> words = {
        {{4000, 6000}, "lead"},
        {{249000, 251000}, "trail"},
    };
    const auto out = assemble_transcript("lead trail", segments, {}, words, kRate);

    check(out.segments.size() == 3, "three segments");
    check(segment_at(out, 0, "first segment").text == "lead",
          "the leading stray word goes to the nearest segment by midpoint");
    check(segment_at(out, 2, "third segment").text == "trail",
          "the trailing stray word goes to the nearest segment by midpoint");
    check(segment_at(out, 1, "middle segment").words.empty(),
          "the long middle segment claims neither stray word");
}

// Speaker assignment uses greatest overlap, not first match, so a turn that
// barely touches a segment does not win over one that covers it.
static void test_speaker_assigned_by_greatest_overlap() {
    const std::vector<Span> segments = {{8000, 24000}};
    const std::vector<SpeakerSpan> turns = {
        {{0, 9000}, "brief"},   // overlaps 1000 samples
        {{9000, 24000}, "main"} // overlaps 15000 samples
    };
    const auto out = assemble_transcript("x", segments, turns, {}, kRate);
    check(out.segments.size() == 1, "one segment");
    check(segment_at(out, 0, "sole segment").speaker == "main",
          "greatest overlap wins");
}

// A segment no turn touches gets no speaker rather than the label of whichever
// turn happened to be listed first.
static void test_segment_without_any_overlapping_turn_has_no_speaker() {
    const std::vector<Span> segments = {{0, 8000}, {40000, 48000}};
    const std::vector<SpeakerSpan> turns = {{{0, 8000}, "SPEAKER_00"}};
    const auto out = assemble_transcript("x", segments, turns, {}, kRate);

    check(segment_at(out, 0, "overlapped segment").speaker == "SPEAKER_00",
          "the overlapped segment is labelled");
    check(segment_at(out, 1, "unlabelled segment").speaker.empty(),
          "a segment no turn overlaps is left unlabelled");
}

static void test_zero_sample_rate_is_safe() {
    const std::vector<Span> segments = {{0, 16000}};
    const auto out = assemble_transcript("x", segments, {}, {}, 0);
    check(out.segments.size() == 1, "a zero sample rate still yields the segment");
    const OutSegment &only = segment_at(out, 0, "sole segment");
    check(only.start_ns == 0 && only.end_ns == 0,
          "unknown sample rate yields zero timings rather than garbage");
}

int main() {
    test_words_only();
    test_words_only_with_punctuated_text_output();
    test_covering_span_spans_every_word();
    test_empty_word_contributes_no_separator();
    test_segments_without_words();
    test_segments_with_speaker_turns_no_words();
    test_speech_segments_outrank_speaker_turns();
    test_speaker_turns_outrank_words();
    test_text_only();
    test_speaker_turns_only();
    test_nested_speaker_turn_keeps_its_own_label();
    test_empty();
    test_vad_segments_without_text();
    test_words_distributed_into_segments();
    test_words_assigned_by_midpoint_not_endpoint();
    test_word_outside_all_segments();
    test_stray_word_goes_to_the_nearest_segment();
    test_stray_word_distance_is_measured_from_the_midpoint();
    test_speaker_assigned_by_greatest_overlap();
    test_segment_without_any_overlapping_turn_has_no_speaker();
    test_zero_sample_rate_is_safe();
    if (failures) {
        fprintf(stderr, "%d check(s) failed\n", failures);
        return 1;
    }
    fprintf(stderr, "all transcript_assembly checks passed\n");
    return 0;
}
