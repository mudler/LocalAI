// Unit tests for stream_delta. Standard library only. The harness compiles this
// as a single translation unit, so the implementation is included directly.
//
// The traces below are transcribed from the pinned upstream sessions rather
// than invented, because the whole reason this unit exists is that the four
// streaming ASR families do NOT agree on what partial_text means.

#include "stream_delta.cpp"

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

static void check_eq(const std::string &got, const std::string &want,
                     const std::string &name) {
    check(got == want, name + " (got \"" + got + "\" want \"" + want + "\")");
}

using audiocpp_backend::TranscriptDeltaTracker;

// Feeds a whole trace and returns what a client appending every emitted
// fragment would end up holding, which is the only property that matters.
static std::string client_view(TranscriptDeltaTracker &tracker,
                               const std::vector<std::string> &partials,
                               std::vector<std::string> *emitted = nullptr) {
    std::string view;
    for (const auto &partial : partials) {
        const std::string fragment = tracker.observe(partial);
        if (emitted != nullptr && !fragment.empty()) {
            emitted->push_back(fragment);
        }
        view += fragment;
    }
    return view;
}

// nemotron_asr: decoder.cpp emits current_text.substr(emitted_text.size()) on
// every non-blank token, i.e. INCREMENTAL fragments, through the stream event
// sink during finalize().
static void test_incremental_family() {
    TranscriptDeltaTracker tracker;
    std::vector<std::string> emitted;
    const std::string view =
        client_view(tracker, {"Local", " AI", " now", " speaks."}, &emitted);
    check_eq(view, "Local AI now speaks.", "incremental deltas concatenate");
    check(emitted.size() == 4, "incremental family emits one fragment per partial");
    check_eq(tracker.assembled(), "Local AI now speaks.",
             "incremental family assembles the whole transcript");
    check_eq(tracker.reconcile("Local AI now speaks."), "",
             "a final result the deltas already cover adds nothing");
}

// voxtral_realtime: process_one_stream_chunk sets partial_text to
// tokenizer_.decode(streaming_token_ids_), the WHOLE accumulated hypothesis,
// and process_available_stream_chunks then hands the SAME event to both the
// sink and the caller, so every partial arrives twice.
static void test_cumulative_family_with_duplicate_delivery() {
    TranscriptDeltaTracker tracker;
    std::vector<std::string> emitted;
    const std::string view = client_view(
        tracker, {"Local", "Local", "Local AI", "Local AI", "Local AI now",
                  "Local AI now"},
        &emitted);
    check_eq(view, "Local AI now", "cumulative partials are not repeated to the client");
    check(emitted.size() == 3,
          "the duplicate delivery of each cumulative event emits nothing twice");
    check_eq(emitted.empty() ? "" : emitted[0], "Local", "first cumulative fragment");
    check_eq(emitted.size() < 2 ? "" : emitted[1], " AI", "second cumulative fragment");
    check_eq(emitted.size() < 3 ? "" : emitted[2], " now", "third cumulative fragment");
}

// The rule that separates the two: text the client has ALREADY been sent is
// never sent again, whatever convention produced it.
static void test_already_delivered_text_is_never_resent() {
    TranscriptDeltaTracker tracker;
    check_eq(tracker.observe("hello world"), "hello world", "first fragment");
    check_eq(tracker.observe("hello world"), "", "an identical repeat emits nothing");
    check_eq(tracker.observe("hello"), "",
             "a shortened hypothesis emits nothing rather than duplicating a prefix");
    check_eq(tracker.assembled(), "hello world",
             "a shortened hypothesis does not shrink what the client holds");
}

static void test_empty_partials_are_ignored() {
    TranscriptDeltaTracker tracker;
    check_eq(tracker.observe(""), "", "an empty partial emits nothing");
    check_eq(tracker.observe("a"), "a", "a real partial after an empty one still emits");
    check_eq(tracker.observe(""), "", "a later empty partial emits nothing");
    check_eq(tracker.assembled(), "a", "empty partials do not disturb the assembly");
}

// The offline fallback: a family with no streaming ASR runs once, so nothing is
// ever observed and the reconciliation IS the single delta the RPC promises.
static void test_offline_fallback_is_one_delta() {
    TranscriptDeltaTracker tracker;
    check_eq(tracker.reconcile("the whole transcript"), "the whole transcript",
             "with no partials the final text is emitted whole");
    check_eq(tracker.assembled(), "the whole transcript",
             "the reconciliation is recorded as delivered");
    check_eq(tracker.reconcile("the whole transcript"), "",
             "reconciling twice does not duplicate");
}

// A streaming family whose partials stopped short of the final text: the tail
// is emitted so that appending every delta still equals final_result.text.
static void test_reconcile_emits_the_tail() {
    TranscriptDeltaTracker tracker;
    check_eq(tracker.observe("Local AI"), "Local AI", "partial arrives");
    check_eq(tracker.reconcile("Local AI now speaks."), " now speaks.",
             "the untold tail of the final text is emitted");
    check_eq(tracker.assembled(), "Local AI now speaks.", "tail is recorded");
}

// Divergence. nemotron's decoder has a rewrite branch: when the new hypothesis
// is NOT an extension of what it already emitted, it emits the whole new text.
// Nothing can retract a fragment already written to the wire, so the tracker
// must not try: it emits nothing further and leaves final_result authoritative.
static void test_divergent_final_text_is_not_appended() {
    TranscriptDeltaTracker tracker;
    check_eq(tracker.observe("the cat"), "the cat", "first hypothesis");
    check_eq(tracker.reconcile("the dog"), "",
             "a final text that contradicts the deltas is not appended to them");
    check_eq(tracker.assembled(), "the cat",
             "a contradicted assembly is left as it was actually sent");
}

static void test_empty_final_text() {
    TranscriptDeltaTracker tracker;
    check_eq(tracker.observe("something"), "something", "partial arrives");
    check_eq(tracker.reconcile(""), "", "an empty final text emits nothing");
    check_eq(tracker.assembled(), "something", "an empty final text changes nothing");
}

// A whitespace-only fragment is real text: the space between two words is
// exactly what an incremental family delivers on its own.
static void test_whitespace_fragments_survive() {
    TranscriptDeltaTracker tracker;
    check_eq(tracker.observe("one"), "one", "word");
    check_eq(tracker.observe(" "), " ", "a bare separator is emitted");
    check_eq(tracker.observe("two"), "two", "next word");
    check_eq(tracker.assembled(), "one two", "separator is kept in the assembly");
}

// The ambiguity this unit cannot resolve, pinned so that a future reader sees
// the choice rather than rediscovering it: a fragment that EXTENDS everything
// delivered so far is read as a cumulative report, because that is what every
// cumulative family produces on every event, while an incremental family
// producing one is the rare coincidence of a fragment repeating the whole
// transcript so far.
static void test_prefix_extension_is_read_as_cumulative() {
    TranscriptDeltaTracker tracker;
    check_eq(tracker.observe("I"), "I", "first fragment");
    check_eq(tracker.observe("I'm"), "'m",
             "a fragment extending the assembly is treated as a cumulative report");
    check_eq(tracker.assembled(), "I'm", "cumulative reading assembles once");
}

int main() {
    test_incremental_family();
    test_cumulative_family_with_duplicate_delivery();
    test_already_delivered_text_is_never_resent();
    test_empty_partials_are_ignored();
    test_offline_fallback_is_one_delta();
    test_reconcile_emits_the_tail();
    test_divergent_final_text_is_not_appended();
    test_empty_final_text();
    test_whitespace_fragments_survive();
    test_prefix_extension_is_read_as_cumulative();
    if (failures) {
        fprintf(stderr, "%d check(s) failed\n", failures);
        return 1;
    }
    fprintf(stderr, "all stream_delta checks passed\n");
    return 0;
}
