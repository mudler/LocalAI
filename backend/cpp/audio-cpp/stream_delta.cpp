#include "stream_delta.h"

namespace audiocpp_backend {
namespace {

// True when `text` begins with `prefix`. An empty prefix matches everything,
// which is what makes the first fragment take the cumulative branch and the
// incremental branch alike: they agree there.
bool starts_with(const std::string &text, const std::string &prefix) {
    return text.size() >= prefix.size() &&
           text.compare(0, prefix.size(), prefix) == 0;
}

// Number of leading bytes of `text` that cannot BEGIN a character, i.e. orphan
// continuation bytes with no lead byte in front of them.
//
// They are unrecoverable rather than early: the byte that would have led them
// has already gone past, and nothing can be prepended to a fragment after the
// fact. Holding them would stall the stream for good, and emitting them puts
// invalid UTF-8 on the wire, so the caller DROPS them. Losing a byte keeps the
// stream alive; emitting one ends it, and takes the final_result still to come
// with it.
std::size_t utf8_orphan_prefix_length(const std::string &text) {
    std::size_t index = 0;
    while (index < text.size() &&
           (static_cast<unsigned char>(text[index]) & 0xC0) == 0x80) {
        ++index;
    }
    return index;
}

// Length of the longest prefix of `text` that does NOT end inside a multi-byte
// UTF-8 sequence, i.e. the most that can go on the wire without splitting a
// character in half.
//
// Only the TRAILING sequence is examined; the leading side is
// utf8_orphan_prefix_length's job. Bytes in the MIDDLE are neither one's
// business: a family that emitted a malformed sequence inside its own text
// cannot be repaired here without deleting part of that transcript, and the
// stream is lost anyway, because final_result.text carries the same bytes
// through the same proto3 string field.
//
// Anything that can never complete is reported as complete, so it goes out
// rather than being held forever: a lead byte the encoding does not define, and
// a run of five or more continuation bytes, are both passed through. A tracker
// that stalled on undecodable input would turn one bad byte into a permanently
// silent stream, which is worse than the bad byte.
std::size_t utf8_complete_prefix_length(const std::string &text) {
    std::size_t index = text.size();
    std::size_t continuations = 0;
    while (index > 0 && continuations < 4) {
        const auto byte = static_cast<unsigned char>(text[index - 1]);
        if ((byte & 0xC0) == 0x80) {
            --index;
            ++continuations;
            continue;
        }
        std::size_t needed = 1;
        if ((byte & 0x80) == 0x00) {
            needed = 1;
        } else if ((byte & 0xE0) == 0xC0) {
            needed = 2;
        } else if ((byte & 0xF0) == 0xE0) {
            needed = 3;
        } else if ((byte & 0xF8) == 0xF0) {
            needed = 4;
        } else {
            // Not a lead byte this encoding defines, so nothing is waiting on
            // it and it must not be held.
            needed = 1;
        }
        if (continuations + 1 >= needed) {
            return text.size();
        }
        // The trailing sequence is short by at least one byte: cut before its
        // lead byte and keep the rest for the next fragment.
        return index - 1;
    }
    return text.size();
}

} // namespace

std::string TranscriptDeltaTracker::release(const std::string &fragment) {
    // Held-back bytes go in FRONT of whatever arrived next, or the character
    // they begin is reassembled in the wrong order.
    std::string candidate = pending_ + fragment;
    // A fragment must not BEGIN mid-character either. pending_ always starts on
    // a lead byte, so this only bites when nothing was held and the caller's
    // rules dropped the lead byte somewhere upstream; it is the backstop that
    // makes "no delta this class returns is ever invalid UTF-8" true of the
    // FRONT as well as the back, independently of those rules being right.
    candidate.erase(0, utf8_orphan_prefix_length(candidate));
    const std::size_t cut = utf8_complete_prefix_length(candidate);
    pending_ = candidate.substr(cut);
    std::string emitted = candidate.substr(0, cut);
    assembled_ += emitted;
    return emitted;
}

std::string TranscriptDeltaTracker::observe(const std::string &partial_text) {
    if (partial_text.empty()) {
        return {};
    }
    // The comparisons run against everything KNOWN, delivered plus held back,
    // rather than against the delivered text alone. Comparing against the
    // delivered text would treat the held-back byte as new on the very next
    // report and emit it twice.
    const std::string known = assembled_ + pending_;
    // An EXACT repeat, and nothing looser. This absorbs the duplicate delivery
    // voxtral_realtime produces, which is the ONLY thing rule 2 was ever needed
    // for: process_available_stream_chunks hands each event it produces to the
    // sink from inside its loop and RETURNS the last of the batch
    // (session.cpp:385-386), so that last event arrives twice with byte-equal
    // text both times. A duplicate IS an exact repeat, so equality covers it.
    //
    // It used to discard any report the known text merely STARTED WITH, and that
    // cost far more than it bought. Two separate defects came out of it, and
    // both were found by randomized traces rather than by reading:
    //
    //   1. A short INCREMENTAL fragment that happens to be a byte prefix of the
    //      transcript so far was read as a repeat and dropped, losing text with
    //      a 200 and no diagnostic. Both incremental families emit fragments
    //      that small routinely: nemotron_asr cuts at a byte offset
    //      (decoder.cpp:550) and vibevoice_asr at a common prefix
    //      (session.cpp:89-105). 9.50% of randomized pure-ASCII traces and
    //      29.12% of French ones ended with a corrupted transcript.
    //   2. When such a fragment was the LEAD BYTE of a multi-byte character, its
    //      continuation bytes then arrived alone and began the next delta, which
    //      is invalid UTF-8, which the Go runtime refuses to unmarshal, which
    //      ends the stream and the final_result with it.
    //
    // What the narrowing gives up is the shrinking-hypothesis case: a cumulative
    // report SHORTER than what is known is now read as an incremental fragment
    // and duplicates those bytes at the client. No pinned family produces one.
    // voxtral is the only cumulative reporter, and its hypothesis is
    // tokenizer_.decode(streaming_token_ids_) over a vector that is only ever
    // push_back'ed (session.cpp:436) and cleared by reset() (session.cpp:257),
    // so within a stream it can only grow. Measured: narrowing this changed not
    // one byte of 30,000 randomized cumulative traces.
    //
    // KEPT DELIBERATELY THOUGH NO TEST CAN SEE IT. Once narrowed to equality
    // this rule became redundant with rule 3 below: an equal partial has an
    // empty suffix, so rule 3 would call release("") and emit nothing either
    // way. Deleting it is therefore an equivalent mutation, and the mutation
    // harness reports it as a survivor, which is the honest result and not a
    // gap in the tests. It stays for two reasons: it states the duplicate
    // absorption where the citation for it lives, and it is independent of rule
    // 3's condition. A future tightening of rule 3 to, say, require a STRICTLY
    // longer partial would otherwise send every duplicate down the incremental
    // branch and put the whole transcript on the wire a second time.
    if (partial_text == known) {
        return {};
    }
    if (starts_with(partial_text, known)) {
        // Cumulative: the report is the whole transcript so far.
        return release(partial_text.substr(known.size()));
    }
    // Incremental: the fragment is new text to append.
    //
    // A family that REWRITES its hypothesis lands here too, and the client's
    // view is then wrong in a way nothing downstream can fix. nemotron's
    // decoder has such a branch (decoder.cpp:552-554): when the new text is not
    // an extension of what it already emitted, it emits the whole new text. So
    // "the cat sat" followed by "the cat sap" leaves the client holding
    // "the cat satthe cat sap", and reconcile then correctly refuses to append
    // to a contradicted assembly, which leaves concat(deltas) != final with no
    // signal on the wire. This is NOT repaired here, and the reason is that a
    // delta stream has no retraction: emitting only the differing suffix would
    // read as "sap" appended to "the cat sat", which is a different wrong
    // answer, and emitting a correction would need a wire field that does not
    // exist. final_result carries the authoritative text either way. It did not
    // fire in a 331 delta run, because an RNN-T decode is monotonic in practice.
    return release(partial_text);
}

std::string TranscriptDeltaTracker::reconcile(const std::string &final_text) {
    if (final_text.empty() || final_text == assembled_) {
        // Nothing further is owed. Held-back bytes are dropped rather than
        // flushed: they are not in the authoritative text, so sending them
        // would contradict it.
        pending_.clear();
        return {};
    }
    if (!starts_with(final_text, assembled_)) {
        // Contradicted. Nothing sent can be taken back, so nothing more is
        // sent; final_result carries the authoritative text.
        pending_.clear();
        return {};
    }
    // Compared against the DELIVERED text, so the fragment below already
    // contains whatever was held back. pending_ is therefore cleared rather
    // than prepended, or those bytes would go out twice.
    //
    // The fragment ends on a character boundary whenever final_text is
    // well-formed, which is the normal case and the reason a held-back sequence
    // is always flushed here. It is still cut, so a family handing back a final
    // text that is itself truncated mid-character cannot put a partial sequence
    // on the wire through this path either.
    pending_.clear();
    return release(final_text.substr(assembled_.size()));
}

} // namespace audiocpp_backend
