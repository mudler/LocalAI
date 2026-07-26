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
//                     hypothesis so far. process_available_stream_chunks hands
//                     every event it produces to the sink from INSIDE its loop
//                     (session.cpp:385-386) and RETURNS only the last of the
//                     batch, so the last event of each batch arrives twice and
//                     the others arrive once.
//
// Applying either convention to the other family corrupts the transcript: read
// a cumulative report as a delta and the client sees the transcript repeated on
// every event; read an incremental fragment as cumulative and the suffix
// arithmetic eats the front of it. So the tracker decides per fragment, from
// what it has already delivered, and the one rule it enforces is that TEXT THE
// CLIENT HAS ALREADY BEEN SENT IS NEVER SENT AGAIN.
//
// The cumulative reading is provably safe for voxtral, which is the family it
// matters for: its decode is a pure concatenation of per-token byte strings
// (tokenizer_text.cpp:171-183), so decode(ids[0..n]) is an unconditional BYTE
// PREFIX of decode(ids[0..n+1]) and one of its reports can never be mistaken
// for an incremental fragment.
//
// UTF-8 IS THE OTHER HALF OF THAT SAME FACT. Because that decode concatenates
// raw token BYTES, a multi-byte character is split across token boundaries, and
// the difference between two consecutive cumulative reports is then a lone
// continuation byte. TranscriptStreamResponse.delta is a proto3 `string`, whose
// wire format REQUIRES valid UTF-8: the C++ runtime serializes an invalid one
// with at most a warning, but the Go runtime refuses to unmarshal it, and the
// client loses every remaining delta AND the final_result. So no fragment this
// class returns ever BEGINS OR ENDS inside a character: an incomplete trailing
// sequence is held back and merged into the next fragment, and a leading orphan
// continuation byte, which nothing can ever complete, is dropped.
//
// It is NOT a voxtral-only concern, which is what the first attempt at this
// assumed. The incremental families split characters by the same arithmetic:
// nemotron_asr's decoder cuts at a BYTE offset (decoder.cpp:550) and
// vibevoice_asr's common_prefix_size compares BYTES (session.cpp:80-86). Nor is
// European text the worst case: a Japanese transcript, whose every character is
// three bytes, carried at least one invalid delta in 33.74% of traces until
// rule 2 below learned to leave an incomplete fragment alone.

#include <string>

namespace audiocpp_backend {

class TranscriptDeltaTracker {
public:
    // Takes one StreamEvent::partial_text and returns the fragment to put on
    // the wire, empty when there is nothing new.
    //
    // The rules, in order, all of them against everything KNOWN (delivered
    // plus held back), never against the delivered text alone:
    //   1. An empty partial says nothing.
    //   2. A partial the known text ALREADY STARTS WITH, AND WHICH IS ITSELF A
    //      WHOLE NUMBER OF CHARACTERS, has been accounted for: nothing is
    //      emitted. This is what absorbs voxtral's repeat of the last event in
    //      each batch, and a cumulative hypothesis that shrinks. The second
    //      condition is what keeps a new character's lead byte from being
    //      mistaken for a repeat of an old character that starts with the same
    //      byte; see the note at the rule in the implementation.
    //   3. A partial that EXTENDS the known text is a cumulative report: only
    //      its new suffix is emitted.
    //   4. Anything else is an incremental fragment: it is emitted whole and
    //      appended.
    //
    // Rule 3 is the one judgement call, since a fragment that happens to begin
    // with the entire transcript so far is indistinguishable from a cumulative
    // report. It is read as cumulative because every cumulative family produces
    // that shape on EVERY event, while an incremental family produces it only
    // when one fragment repeats everything before it, which no tokenizer output
    // does in practice.
    //
    // What comes back is the fragment MINUS any incomplete trailing UTF-8
    // sequence, which is carried into the next call, and minus any leading
    // orphan continuation byte, which is dropped. So an empty return can also
    // mean "the only new bytes were half a character", and the caller needs no
    // knowledge of that: writing nothing is exactly right.
    std::string observe(const std::string &partial_text);

    // Reconciles against TaskResult::text_output, which is authoritative, and
    // returns the fragment that makes appending every delta equal it.
    //
    // This is what makes the OFFLINE FALLBACK a single line rather than its own
    // branch: with no partials observed, the assembly is empty and the whole
    // final text comes back as one delta.
    //
    // It is also what FLUSHES a held-back UTF-8 sequence, and it can always do
    // so: the final text is complete, so the fragment from the last delivered
    // byte to its end ends on a character boundary.
    //
    // A final text that CONTRADICTS what was already sent returns empty. A
    // fragment on the wire cannot be retracted, so the alternative would be to
    // send the transcript a second time and let the client hold it twice.
    // final_result carries the authoritative text either way.
    std::string reconcile(const std::string &final_text);

    // Everything the client has been sent, concatenated. Held-back bytes are
    // deliberately NOT included: this is what the client holds, not what the
    // tracker knows.
    const std::string &assembled() const noexcept { return assembled_; }

private:
    // Appends the emittable prefix of `fragment` to assembled_ and returns it,
    // keeping any incomplete trailing UTF-8 sequence in pending_.
    std::string release(const std::string &fragment);

    std::string assembled_;
    // An incomplete trailing UTF-8 sequence, computed but not sent. Always a
    // proper prefix of one character, so at most three bytes.
    std::string pending_;
};

} // namespace audiocpp_backend
