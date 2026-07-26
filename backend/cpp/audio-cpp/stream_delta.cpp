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

} // namespace

std::string TranscriptDeltaTracker::observe(const std::string &partial_text) {
    if (partial_text.empty()) {
        return {};
    }
    // Already delivered. Covers the exact repeat voxtral produces on every
    // event and a hypothesis that shrank.
    if (starts_with(assembled_, partial_text)) {
        return {};
    }
    if (starts_with(partial_text, assembled_)) {
        // Cumulative: the report is the whole transcript so far.
        std::string fragment = partial_text.substr(assembled_.size());
        assembled_ = partial_text;
        return fragment;
    }
    // Incremental: the fragment is new text to append.
    assembled_ += partial_text;
    return partial_text;
}

std::string TranscriptDeltaTracker::reconcile(const std::string &final_text) {
    if (final_text.empty() || final_text == assembled_) {
        return {};
    }
    if (!starts_with(final_text, assembled_)) {
        // Contradicted. Nothing sent can be taken back, so nothing more is
        // sent; final_result carries the authoritative text.
        return {};
    }
    std::string fragment = final_text.substr(assembled_.size());
    assembled_ = final_text;
    return fragment;
}

} // namespace audiocpp_backend
