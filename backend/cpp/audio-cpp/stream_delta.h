#pragma once

// Turns whatever a streaming session calls a "partial transcript" into the
// incremental deltas AudioTranscriptionStream is contracted to send. Standard
// library only, so it is tested without an audio.cpp checkout.
//
// THIS UNIT EXISTS BECAUSE THE FAMILIES DISAGREE, and the disagreement is
// invisible at the interface: StreamEvent::partial_text is a Transcript either
// way. Read out of the pinned upstream, one family at a time:
//
//   nemotron_asr      INCREMENTAL. decoder.cpp emits
//                     current_text.substr(emitted_text.size()) per non-blank
//                     token, and only through the stream event SINK, during
//                     finalize(). process_audio_chunk returns empty events.
//   vibevoice_asr     INCREMENTAL. process_audio_chunk returns
//                     text.substr(common_prefix_size(...)); the sink is
//                     deliberately swapped out around its internal run_single,
//                     so the fragment arrives once, on the return value.
//   higgs_audio_stt   INCREMENTAL. Same shape as vibevoice_asr.
//   voxtral_realtime  CUMULATIVE. partial_text is
//                     tokenizer_.decode(streaming_token_ids_), the whole
//                     hypothesis so far, and process_available_stream_chunks
//                     hands the SAME event to the sink AND returns it, so every
//                     partial arrives TWICE.
//
// Applying either convention to the other family corrupts the transcript: read
// a cumulative report as a delta and the client sees the transcript repeated on
// every event; read an incremental fragment as cumulative and the suffix
// arithmetic eats the front of it. So the tracker decides per fragment, from
// what it has already delivered, and the one rule it enforces is that TEXT THE
// CLIENT HAS ALREADY BEEN SENT IS NEVER SENT AGAIN.

#include <string>

namespace audiocpp_backend {

class TranscriptDeltaTracker {
public:
    // Takes one StreamEvent::partial_text and returns the fragment to put on
    // the wire, empty when there is nothing new.
    //
    // The rules, in order:
    //   1. An empty partial says nothing.
    //   2. A partial the assembly ALREADY STARTS WITH has been delivered:
    //      nothing is emitted. This is what absorbs voxtral's double delivery
    //      of every event, and a cumulative hypothesis that shrinks.
    //   3. A partial that EXTENDS the assembly is a cumulative report: only its
    //      new suffix is emitted.
    //   4. Anything else is an incremental fragment: it is emitted whole and
    //      appended.
    //
    // Rule 3 is the one judgement call, since a fragment that happens to begin
    // with the entire transcript so far is indistinguishable from a cumulative
    // report. It is read as cumulative because every cumulative family produces
    // that shape on EVERY event, while an incremental family produces it only
    // when one fragment repeats everything before it, which no tokenizer output
    // does in practice.
    std::string observe(const std::string &partial_text);

    // Reconciles against TaskResult::text_output, which is authoritative, and
    // returns the fragment that makes appending every delta equal it.
    //
    // This is what makes the OFFLINE FALLBACK a single line rather than its own
    // branch: with no partials observed, the assembly is empty and the whole
    // final text comes back as one delta.
    //
    // A final text that CONTRADICTS what was already sent returns empty. A
    // fragment on the wire cannot be retracted, so the alternative would be to
    // send the transcript a second time and let the client hold it twice.
    // final_result carries the authoritative text either way.
    std::string reconcile(const std::string &final_text);

    // Everything the client has been sent, concatenated.
    const std::string &assembled() const noexcept { return assembled_; }

private:
    std::string assembled_;
};

} // namespace audiocpp_backend
