#pragma once

// Converts engine::runtime results into LocalAI proto messages. All of the
// non-trivial shaping lives in transcript_assembly, which is stdlib-only and
// unit tested; this unit is the thin engine-typed boundary around it.

#include "backend.pb.h"

#include "engine/framework/runtime/session.h"

namespace audiocpp_backend {

// Fills text, language, duration, segments and per-segment words.
//
// THE RULE: the top-level text is TaskResult.text_output verbatim. It is never
// derived from segments or words. audio.cpp carries transcript text in
// text_output and nowhere else: speech_segments, speaker_turns and
// word_timestamps carry spans and labels and no text at all. Deriving the
// transcript from them therefore returns an EMPTY text for every producer that
// reports segments without word timing, which real VibeVoice diarized ASR does.
// An earlier attempt at this backend shipped exactly that bug. assemble_transcript
// enforces the rule and is heavily tested; this unit's job is not to re-derive
// it but to not undo it at the proto boundary.
//
// `sample_rate` is the rate the result's spans are expressed in, which is the
// rate of the AudioBuffer that was handed to the session, NOT the rate of the
// file the caller uploaded. Those differ whenever read_audio_file resampled,
// which is why the handler passes the buffer's rate rather than the file's.
//
// Segments are replaced, not appended to, so a message filled twice does not
// accumulate.
void fill_transcript_result(const engine::runtime::TaskResult &result,
                            int sample_rate, float duration_seconds,
                            backend::TranscriptResult *out);

} // namespace audiocpp_backend
