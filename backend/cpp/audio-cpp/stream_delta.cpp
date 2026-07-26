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

// Length of the longest prefix of `text` that does NOT end inside a multi-byte
// UTF-8 sequence, i.e. the most that can go on the wire without splitting a
// character in half.
//
// Only the TRAILING sequence is examined. Bytes further back are none of this
// function's business: a family that emitted a malformed sequence in the middle
// of its own transcript cannot be repaired here without deleting part of that
// transcript.
//
// Anything that can never complete is reported as complete, so it goes out
// rather than being held forever: a lone continuation byte with no lead, a lead
// byte the encoding does not define, and a run of five or more continuation
// bytes are all passed through. A tracker that stalled on undecodable input
// would turn one bad byte into a permanently silent stream, which is worse than
// the bad byte.
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
    const std::string candidate = pending_ + fragment;
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
    // Already accounted for. Covers the repeat voxtral produces when the sink
    // and the return value carry the same event, and a hypothesis that shrank.
    if (starts_with(known, partial_text)) {
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
    // decoder has such a branch (decoder.cpp:551-554): when the new text is not
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
