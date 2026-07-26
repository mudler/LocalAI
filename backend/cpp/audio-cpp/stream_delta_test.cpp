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

// --------------------------------------------------------------------------
// UTF-8 boundaries
//
// TranscriptStreamResponse.delta is a proto3 `string`, and the wire format
// REQUIRES a string field to be valid UTF-8. The C++ runtime serializes an
// invalid one with at most a warning; the Go runtime refuses to unmarshal it,
// so the client loses every delta AND the final_result still to come.
//
// Not hypothetical. voxtral_realtime reports the whole hypothesis as
// tokenizer_.decode(streaming_token_ids_), and that decode is a pure
// concatenation of raw token BYTES (tokenizer_text.cpp:171-183), so a
// multi-byte character is split across token boundaries and the cumulative
// difference between two consecutive reports is a lone continuation byte.
// --------------------------------------------------------------------------

// True when `text` is well-formed UTF-8. Written out here rather than reused
// from the implementation on purpose: a test that shares the implementation's
// idea of a boundary cannot catch the implementation's idea being wrong.
static bool is_valid_utf8(const std::string &text) {
    size_t i = 0;
    while (i < text.size()) {
        const auto lead = static_cast<unsigned char>(text[i]);
        size_t length = 0;
        if ((lead & 0x80) == 0x00) {
            length = 1;
        } else if ((lead & 0xE0) == 0xC0) {
            length = 2;
        } else if ((lead & 0xF0) == 0xE0) {
            length = 3;
        } else if ((lead & 0xF8) == 0xF0) {
            length = 4;
        } else {
            return false;
        }
        if (i + length > text.size()) {
            return false;
        }
        for (size_t k = 1; k < length; ++k) {
            if ((static_cast<unsigned char>(text[i + k]) & 0xC0) != 0x80) {
                return false;
            }
        }
        i += length;
    }
    return true;
}

// Written as byte escapes so the test does not depend on the encoding of this
// source file.
static const std::string kEAcute = "\xC3\xA9";        // 2 bytes
static const std::string kEuro = "\xE2\x82\xAC";      // 3 bytes
static const std::string kEmoji = "\xF0\x9F\x8E\xA7"; // 4 bytes

// A cumulative family advancing its hypothesis one BYTE at a time, which is
// what voxtral_realtime does across a multi-byte character.
static void test_cumulative_split_multibyte_character() {
    TranscriptDeltaTracker tracker;
    std::vector<std::string> emitted;
    const std::string full = "5" + kEuro;
    std::vector<std::string> partials;
    for (size_t n = 1; n <= full.size(); ++n) {
        partials.push_back(full.substr(0, n));
    }
    const std::string view = client_view(tracker, partials, &emitted);

    check_eq(view, full, "a byte-at-a-time cumulative report still assembles");
    for (size_t i = 0; i < emitted.size(); ++i) {
        check(is_valid_utf8(emitted[i]),
              "cumulative fragment " + std::to_string(i) + " is valid UTF-8");
    }
    check_eq(tracker.reconcile(full), "", "the final text adds nothing");
}

// An incremental family splitting a character across two fragments.
static void test_incremental_split_multibyte_character() {
    TranscriptDeltaTracker tracker;
    std::vector<std::string> emitted;
    const std::string view = client_view(
        tracker, {"caf" + kEAcute.substr(0, 1), kEAcute.substr(1), " au lait"},
        &emitted);

    check_eq(view, "caf" + kEAcute + " au lait",
             "an incremental split character still assembles");
    for (size_t i = 0; i < emitted.size(); ++i) {
        check(is_valid_utf8(emitted[i]),
              "incremental fragment " + std::to_string(i) + " is valid UTF-8");
    }
    check(emitted.size() == 3, "one fragment out per partial, none swallowed");
    if (emitted.size() == 3) {
        check_eq(emitted[0], "caf", "the lead byte of the character is held back");
        check_eq(emitted[1], kEAcute,
                 "the held byte is merged into the next fragment, not sent alone");
        check_eq(emitted[2], " au lait", "the rest follows unchanged");
    }
}

// A 4 byte character split three ways, so the held-back buffer has to survive
// more than one round.
static void test_four_byte_character_split_three_ways() {
    TranscriptDeltaTracker tracker;
    std::vector<std::string> emitted;
    const std::string view =
        client_view(tracker,
                    {"listen " + kEmoji.substr(0, 1), kEmoji.substr(1, 2),
                     kEmoji.substr(3), " now"},
                    &emitted);
    check_eq(view, "listen " + kEmoji + " now",
             "a 4 byte character survives three splits");
    for (size_t i = 0; i < emitted.size(); ++i) {
        check(is_valid_utf8(emitted[i]),
              "4 byte fragment " + std::to_string(i) + " is valid UTF-8");
    }
}

// The held-back bytes must reach the client. reconcile can always flush them,
// because the final text is complete by construction.
static void test_reconcile_flushes_a_held_back_sequence() {
    TranscriptDeltaTracker tracker;
    const std::string first = tracker.observe("done" + kEuro.substr(0, 2));
    check_eq(first, "done", "the incomplete trailing sequence is held back");
    check(is_valid_utf8(first), "what was emitted is valid UTF-8");
    const std::string tail = tracker.reconcile("done" + kEuro);
    check_eq(tail, kEuro, "reconcile flushes the completed character");
    check(is_valid_utf8(tail), "the flushed tail is valid UTF-8");
    check_eq(tracker.assembled(), "done" + kEuro, "the client holds the whole text");
}

// Held-back bytes are not lost track of: the next cumulative report emits the
// whole character rather than only the bytes that just arrived.
static void test_held_bytes_join_the_next_fragment() {
    TranscriptDeltaTracker tracker;
    check_eq(tracker.observe("a" + kEuro.substr(0, 1)), "a",
             "only the complete prefix goes out");
    check_eq(tracker.observe("a" + kEuro), kEuro,
             "the next cumulative report emits the whole character at once");
    check_eq(tracker.assembled(), "a" + kEuro, "assembly is correct");
}

// A whole multi-byte character arriving at once must NOT be held back: holding
// a complete sequence would stall every stream by one character.
static void test_a_complete_character_is_not_held() {
    TranscriptDeltaTracker tracker;
    check_eq(tracker.observe("x" + kEuro), "x" + kEuro,
             "a fragment ending on a boundary is emitted immediately");
    check_eq(tracker.observe("x" + kEuro + kEmoji), kEmoji,
             "and so is the next one");
}

// Bytes that can never complete must not be held forever: a stray continuation
// byte or an invalid lead is passed through rather than stalling the stream.
// Repairing a family's malformed output is not something a delta tracker can do
// without altering the transcript.
static void test_undecodable_bytes_are_not_held_forever() {
    TranscriptDeltaTracker tracker;
    check_eq(tracker.observe(std::string("ok\x80")), std::string("ok\x80"),
             "a stray continuation byte is passed through, not held");
    check_eq(tracker.observe(std::string("ok\x80") + "next"), "next",
             "the stream continues");

    // A lead byte the encoding does not define (0xF8 and above). Nothing can
    // ever complete it, so holding it would stall the stream for good.
    TranscriptDeltaTracker invalid_lead;
    check_eq(invalid_lead.observe(std::string("ok\xFE")), std::string("ok\xFE"),
             "an undefined lead byte is passed through, not held");
    check_eq(invalid_lead.observe(std::string("ok\xFE") + "more"), "more",
             "the stream continues past an undefined lead byte");

    // The same byte followed by continuation bytes, which is the shape that
    // looks most like a real sequence waiting to be completed.
    TranscriptDeltaTracker invalid_run;
    check_eq(invalid_run.observe(std::string("\xFE\x80\x80")), std::string("\xFE\x80\x80"),
             "an undefined lead with continuations is passed through");

    // Five continuation bytes with no lead in sight. They are DROPPED, not
    // held: nothing can ever precede them, so holding would stall the stream
    // for good, and emitting them would put invalid UTF-8 on the wire. See
    // test_a_fragment_never_begins_mid_character.
    TranscriptDeltaTracker orphans;
    check_eq(orphans.observe(std::string("\x80\x80\x80\x80\x80")), "",
             "a run of orphan continuation bytes is dropped, not emitted");
    check_eq(orphans.observe("after"), "after",
             "and the stream continues past them");
}

// THE SECOND HALF OF THE SAME BUG, and the one that survived fix round 1.
//
// Rule 2 discards a fragment the known text already starts with. When that
// fragment is the LEAD BYTE of a NEW character it looks exactly like a repeat of
// an earlier character beginning with the same byte, so it was discarded and
// never held. Its continuation bytes then arrived on their own and began the
// next delta, which is invalid UTF-8 at the FRONT, and utf8_complete_prefix_length
// only ever inspected the TRAILING sequence.
//
// Reachable from shipping families, not synthetic: nemotron_asr's
// decoder.cpp:550 cuts at a BYTE offset (current_text.substr(emitted_text.size()))
// and vibevoice_asr's common_prefix_size (session.cpp:80-86) compares BYTES, so
// both split characters mid-sequence. The trace below is exactly how they split
// "ssee" spelled with the German sharp s, an e-acute, a euro sign and an o-grave,
// three of which begin with the same 0xC3 lead byte.
static void test_a_repeated_lead_byte_is_not_swallowed() {
    TranscriptDeltaTracker tracker;
    std::vector<std::string> emitted;
    const std::string sharp_s = "\xC3\x9F";  // U+00DF
    const std::string e_acute = "\xC3\xA9";  // U+00E9
    const std::string euro = "\xE2\x82\xAC"; // U+20AC
    const std::string o_grave = "\xC3\xB2";  // U+00F2
    const std::string full = sharp_s + e_acute + euro + o_grave;

    const std::string view = client_view(tracker,
                                         {sharp_s.substr(0, 1), sharp_s.substr(1),
                                          e_acute.substr(0, 1),
                                          e_acute.substr(1) + euro,
                                          o_grave.substr(0, 1), o_grave.substr(1)},
                                         &emitted);

    for (size_t i = 0; i < emitted.size(); ++i) {
        check(is_valid_utf8(emitted[i]),
              "repeated-lead fragment " + std::to_string(i) + " is valid UTF-8");
    }
    check_eq(view, full, "no character is lost to a repeated lead byte");
    check_eq(tracker.reconcile(full), "", "the final text adds nothing");
}

// The same shape one layer down, as a backstop: a fragment that BEGINS with
// orphan continuation bytes must never go on the wire, whatever produced it.
// Dropping bytes keeps the stream alive; emitting them ends it, and takes the
// final_result that was still to come with it.
static void test_a_fragment_never_begins_mid_character() {
    TranscriptDeltaTracker tracker;
    const std::string first = tracker.observe(std::string("\xA9") + "rest");
    check(is_valid_utf8(first), "a leading orphan continuation byte is not emitted");
    check_eq(first, "rest", "the rest of the fragment still goes out");

    TranscriptDeltaTracker all_orphans;
    check_eq(all_orphans.observe(std::string("\x82\xAC")), "",
             "a fragment that is nothing but orphans emits nothing");
    check_eq(all_orphans.assembled(), "",
             "and nothing is recorded as delivered");
    check_eq(all_orphans.observe("after"), "after", "the stream continues");
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
    test_cumulative_split_multibyte_character();
    test_incremental_split_multibyte_character();
    test_four_byte_character_split_three_ways();
    test_reconcile_flushes_a_held_back_sequence();
    test_held_bytes_join_the_next_fragment();
    test_a_complete_character_is_not_held();
    test_undecodable_bytes_are_not_held_forever();
    test_a_repeated_lead_byte_is_not_swallowed();
    test_a_fragment_never_begins_mid_character();
    if (failures) {
        fprintf(stderr, "%d check(s) failed\n", failures);
        return 1;
    }
    fprintf(stderr, "all stream_delta checks passed\n");
    return 0;
}
