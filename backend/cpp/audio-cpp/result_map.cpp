#include "result_map.h"

#include "transcript_assembly.h"

#include <string>
#include <vector>

namespace audiocpp_backend {

void fill_transcript_result(const engine::runtime::TaskResult &result,
                            int sample_rate, float duration_seconds,
                            backend::TranscriptResult *out) {
    if (out == nullptr) {
        return;
    }

    std::vector<Span> speech_segments;
    speech_segments.reserve(result.speech_segments.size());
    for (const auto &segment : result.speech_segments) {
        speech_segments.push_back(
            Span{segment.span.start_sample, segment.span.end_sample});
    }

    std::vector<SpeakerSpan> speaker_turns;
    speaker_turns.reserve(result.speaker_turns.size());
    for (const auto &turn : result.speaker_turns) {
        speaker_turns.push_back(
            SpeakerSpan{Span{turn.span.start_sample, turn.span.end_sample},
                        turn.speaker_id});
    }

    std::vector<WordSpan> words;
    words.reserve(result.word_timestamps.size());
    for (const auto &word : result.word_timestamps) {
        words.push_back(
            WordSpan{Span{word.span.start_sample, word.span.end_sample},
                     word.word});
    }

    // The ONLY read of transcript text in this function, and the only one there
    // may ever be. See THE RULE in the header.
    const std::string text =
        result.text_output.has_value() ? result.text_output->text : std::string();

    const AssembledTranscript assembled = assemble_transcript(
        text, speech_segments, speaker_turns, words, sample_rate);

    out->set_text(assembled.text);
    // language has no source inside transcript_assembly, which is span-shaped
    // only, so it is read straight off the engine result here. Left untouched
    // when the family reported no text output at all: an empty string would be
    // indistinguishable from a family that genuinely detected no language, and
    // the field is documented as optional.
    if (result.text_output.has_value()) {
        out->set_language(result.text_output->language);
    }
    out->set_duration(duration_seconds);

    // Cleared rather than appended to. A caller that fills the same message
    // twice (a stream's final_result being rebuilt, say) would otherwise emit
    // every segment twice, and the second call's ids would restart at 0 and
    // collide with the first call's.
    out->clear_segments();
    for (const auto &segment : assembled.segments) {
        auto *out_segment = out->add_segments();
        out_segment->set_id(segment.id);
        // NANOSECONDS. TranscriptSegment and TranscriptWord are the only
        // messages in backend.proto that use them; VADSegment and DiarizeSegment
        // are float seconds. assemble_transcript has already converted.
        out_segment->set_start(segment.start_ns);
        out_segment->set_end(segment.end_ns);
        out_segment->set_text(segment.text);
        out_segment->set_speaker(segment.speaker);
        for (const auto &word : segment.words) {
            auto *out_word = out_segment->add_words();
            out_word->set_start(word.start_ns);
            out_word->set_end(word.end_ns);
            out_word->set_text(word.text);
        }
    }
}

} // namespace audiocpp_backend
