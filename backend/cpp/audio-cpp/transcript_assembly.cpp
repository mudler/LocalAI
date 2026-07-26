#include "transcript_assembly.h"

#include "audio_units.h"

#include <algorithm>
#include <cstdlib>
#include <limits>

namespace audiocpp_backend {
namespace {

std::int64_t midpoint(const Span &span) {
    return span.start_sample + (span.end_sample - span.start_sample) / 2;
}

bool contains(const Span &span, std::int64_t sample) {
    return sample >= span.start_sample && sample < span.end_sample;
}

std::int64_t overlap(const Span &a, const Span &b) {
    const std::int64_t begin = std::max(a.start_sample, b.start_sample);
    const std::int64_t end = std::min(a.end_sample, b.end_sample);
    return end > begin ? end - begin : 0;
}

std::string join_words(const std::vector<OutWord> &words) {
    std::string out;
    for (const auto &word : words) {
        if (word.text.empty()) {
            continue;
        }
        if (!out.empty()) {
            out += " ";
        }
        out += word.text;
    }
    return out;
}

std::string speaker_for(const Span &segment,
                        const std::vector<SpeakerSpan> &turns) {
    std::string best;
    std::int64_t best_overlap = 0;
    for (const auto &turn : turns) {
        const std::int64_t shared = overlap(segment, turn.span);
        if (shared > best_overlap) {
            best_overlap = shared;
            best = turn.speaker;
        }
    }
    return best;
}

// Chooses the segment spans, per the documented precedence.
std::vector<Span> choose_segment_spans(const std::string &text_output,
                                       const std::vector<Span> &speech_segments,
                                       const std::vector<SpeakerSpan> &speaker_turns,
                                       const std::vector<WordSpan> &words) {
    if (!speech_segments.empty()) {
        return speech_segments;
    }
    if (!speaker_turns.empty()) {
        std::vector<Span> spans;
        spans.reserve(speaker_turns.size());
        for (const auto &turn : speaker_turns) {
            spans.push_back(turn.span);
        }
        return spans;
    }
    if (!words.empty()) {
        Span covering = words.front().span;
        for (const auto &word : words) {
            covering.start_sample =
                std::min(covering.start_sample, word.span.start_sample);
            covering.end_sample = std::max(covering.end_sample, word.span.end_sample);
        }
        return {covering};
    }
    if (!text_output.empty()) {
        // A zero span rather than a fabricated duration: the model reported no
        // timing, and inventing one would be a lie the caller cannot detect.
        return {Span{0, 0}};
    }
    return {};
}

// Returns the index of the segment a word belongs to, or the nearest segment
// when the word falls outside all of them.
size_t segment_index_for_word(const std::vector<Span> &spans, const Span &word) {
    const std::int64_t centre = midpoint(word);
    for (size_t i = 0; i < spans.size(); ++i) {
        if (contains(spans[i], centre)) {
            return i;
        }
    }
    size_t nearest = 0;
    std::int64_t best_distance = std::numeric_limits<std::int64_t>::max();
    for (size_t i = 0; i < spans.size(); ++i) {
        const std::int64_t distance = std::llabs(midpoint(spans[i]) - centre);
        if (distance < best_distance) {
            best_distance = distance;
            nearest = i;
        }
    }
    return nearest;
}

} // namespace

AssembledTranscript assemble_transcript(const std::string &text_output,
                                        const std::vector<Span> &speech_segments,
                                        const std::vector<SpeakerSpan> &speaker_turns,
                                        const std::vector<WordSpan> &words,
                                        int sample_rate) {
    AssembledTranscript assembled;
    // THE RULE. Never derived from spans.
    assembled.text = text_output;

    const std::vector<Span> spans =
        choose_segment_spans(text_output, speech_segments, speaker_turns, words);
    if (spans.empty()) {
        return assembled;
    }

    assembled.segments.resize(spans.size());
    for (size_t i = 0; i < spans.size(); ++i) {
        OutSegment &segment = assembled.segments[i];
        segment.id = static_cast<int>(i);
        segment.start_ns = samples_to_nanoseconds(spans[i].start_sample, sample_rate);
        segment.end_ns = samples_to_nanoseconds(spans[i].end_sample, sample_rate);
        segment.speaker = speaker_for(spans[i], speaker_turns);
    }

    for (const auto &word : words) {
        const size_t index = segment_index_for_word(spans, word.span);
        OutWord out;
        out.start_ns = samples_to_nanoseconds(word.span.start_sample, sample_rate);
        out.end_ns = samples_to_nanoseconds(word.span.end_sample, sample_rate);
        out.text = word.word;
        assembled.segments[index].words.push_back(out);
    }

    for (auto &segment : assembled.segments) {
        segment.text = join_words(segment.words);
    }

    // A single segment with no word timing carries the whole transcript. With
    // several segments there is no defensible way to split the text, so their
    // per-segment text stays empty and only the top-level text is authoritative.
    if (assembled.segments.size() == 1 && assembled.segments[0].words.empty()) {
        assembled.segments[0].text = text_output;
    }

    return assembled;
}

} // namespace audiocpp_backend
