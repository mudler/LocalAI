#pragma once

// Builds LocalAI's TranscriptResult shape from audio.cpp's TaskResult spans.
// Standard library only; result_map.cpp converts the engine types into these
// PODs at the boundary.
//
// THE RULE: the top-level transcript text is text_output verbatim, always.
// audio.cpp carries transcript text in exactly one place, TaskResult.text_output.
// speech_segments, speaker_turns and word_timestamps carry spans and labels but
// no text. Deriving the top-level text by concatenating per-segment text
// therefore yields an empty transcript for every producer that reports segments
// without word timestamps, which includes VibeVoice diarized ASR.

#include <cstdint>
#include <string>
#include <vector>

namespace audiocpp_backend {

// Sample-index span, mirroring engine::runtime::TimeSpan.
struct Span {
    std::int64_t start_sample = 0;
    std::int64_t end_sample = 0;
};

struct WordSpan {
    Span span;
    std::string word;
};

struct SpeakerSpan {
    Span span;
    std::string speaker;
};

struct OutWord {
    std::int64_t start_ns = 0;
    std::int64_t end_ns = 0;
    std::string text;
};

struct OutSegment {
    int id = 0;
    std::int64_t start_ns = 0;
    std::int64_t end_ns = 0;
    std::string text;
    std::string speaker;
    std::vector<OutWord> words;
};

struct AssembledTranscript {
    std::string text;
    std::vector<OutSegment> segments;
};

// Segment source, first non-empty wins:
//   1. speech_segments
//   2. speaker_turns
//   3. one segment spanning all words, when words are present
//   4. one zero-span segment carrying the full text, when text is present
//   5. no segments
//
// Words attach to the segment whose range contains their midpoint; a word
// outside every segment attaches to the nearest one by midpoint distance so it
// is never silently dropped. A segment's text is its words joined by a single
// space, except that a lone segment with no words carries the full text.
// A segment's speaker is the speaker turn with the greatest overlap.
AssembledTranscript assemble_transcript(const std::string &text_output,
                                        const std::vector<Span> &speech_segments,
                                        const std::vector<SpeakerSpan> &speaker_turns,
                                        const std::vector<WordSpan> &words,
                                        int sample_rate);

} // namespace audiocpp_backend
