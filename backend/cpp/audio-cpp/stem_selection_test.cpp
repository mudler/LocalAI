// Unit tests for stem_selection. Standard library only. The harness
// (backend/cpp/run-unit-tests.sh) compiles this as a single translation unit,
// so the implementation is included directly.
//
// What is actually at stake here: AudioTransformResult carries one dst, a
// separation model produces several stems, and the caller cannot see which one
// they got. Every check below is about a wrong file arriving with a 200.

#include "stem_selection.cpp"

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

static void check_equal(const std::string &got, const std::string &want,
                        const std::string &name) {
    check(got == want, name + " (got \"" + got + "\", want \"" + want + "\")");
}

using namespace audiocpp_backend;

// htdemucs's real source order, taken from its GGUF config.sources. vocals is
// LAST, which is why "first output" is not the default.
static const std::vector<std::string> kDemucs = {"drums", "bass", "other",
                                                 "vocals"};
// mel_band_roformer's, where vocals is first.
static const std::vector<std::string> kRoformer = {"vocals", "instrumental"};

static void test_default_selection() {
    const auto demucs = select_named_output(kDemucs, "");
    check(demucs.error.empty(), "an unrequested selection is not an error");
    check(demucs.index == 3, "no stem asked for picks vocals, not the first output");

    const auto roformer = select_named_output(kRoformer, "");
    check(roformer.index == 0, "vocals is picked when it is already first");

    // No vocals anywhere: the first output is the documented fallback.
    const auto novocals = select_named_output({"accompaniment", "drums"}, "");
    check(novocals.index == 0 && novocals.error.empty(),
          "a family with no vocals stem falls back to the first output");

    // Substring matches must not count: "vocals_2" is a different stem.
    const auto near = select_named_output({"vocals_2", "backing"}, "");
    check(near.index == 0 && near.error.empty(),
          "'vocals_2' is not 'vocals', so the fallback and not the preference applies");
    const auto near_second = select_named_output({"backing", "vocals_2"}, "");
    check(near_second.index == 0,
          "a near miss on the preferred name does not pull it to the front");
}

static void test_explicit_selection() {
    for (int i = 0; i < 4; ++i) {
        const auto choice = select_named_output(kDemucs, kDemucs[static_cast<size_t>(i)]);
        check(choice.index == i && choice.error.empty(),
              "explicit '" + kDemucs[static_cast<size_t>(i)] + "' selects its own index");
    }
    // Including the one the default would have chosen anyway: asking for it
    // must not be treated as "no request".
    const auto vocals = select_named_output(kDemucs, "vocals");
    check(vocals.index == 3, "explicitly asking for vocals still selects vocals");
}

static void test_unknown_stem_is_refused() {
    const auto choice = select_named_output(kDemucs, "kazoo");
    check(choice.index == -1, "an unknown stem selects nothing");
    check(!choice.error.empty(), "an unknown stem is refused rather than substituted");
    check(choice.error.find("kazoo") != std::string::npos,
          "the refusal names the stem that was asked for");
    // The real names, so the caller can fix the request without guessing.
    for (const auto &name : kDemucs) {
        check(choice.error.find(name) != std::string::npos,
              "the refusal lists the real stem '" + name + "'");
    }
    check(choice.error.find("drums, bass, other, vocals") != std::string::npos,
          "the refusal lists the stems in the model's own order");

    // Case matters: the engine's ids are exact, so a wrong case is a wrong name
    // rather than a near miss to be forgiven.
    const auto wrong_case = select_named_output(kDemucs, "Vocals");
    check(wrong_case.index == -1 && !wrong_case.error.empty(),
          "stem names are matched case sensitively");
}

static void test_no_named_outputs() {
    const auto choice = select_named_output({}, "");
    check(choice.index == -1, "an empty output list selects nothing");
    check(choice.error.empty(),
          "an empty output list is not an error here: the caller decides");
    const auto requested = select_named_output({}, "vocals");
    check(requested.index == -1 && requested.error.empty(),
          "an empty output list stays the caller's decision even when a stem was asked for");
}

static void test_unwritable_names_are_refused() {
    // Model-supplied names become file path components. A separator would write
    // outside the caller's output directory.
    const std::vector<std::string> traversal = {"vocals", "../../etc/passwd"};
    const auto escaped = select_named_output(traversal, "vocals");
    check(escaped.index == -1 && !escaped.error.empty(),
          "a stem name containing a path separator is refused");
    check(escaped.error.find("../../etc/passwd") != std::string::npos,
          "the refusal names the offending stem");

    check(!select_named_output({"vo\\cals", "drums"}, "").error.empty(),
          "a backslash separator is refused too");
    check(!select_named_output({"drums", ""}, "").error.empty(),
          "an empty stem name is refused");
    check(!select_named_output({"drums", "."}, "").error.empty(),
          "a stem named '.' is refused");
    check(!select_named_output({"drums", ".."}, "").error.empty(),
          "a stem named '..' is refused");

    // The check covers EVERY name, not only the selected one: all of them are
    // written, so a bad fourth name must not be found after three files exist.
    const auto late = select_named_output({"vocals", "drums", "bass", "a/b"}, "vocals");
    check(late.index == -1 && !late.error.empty(),
          "an unwritable name after the selected one still refuses the whole request");

    // A leading dot is not a traversal and must stay usable.
    const auto dotted = select_named_output({".vocals", "drums"}, ".vocals");
    check(dotted.index == 0 && dotted.error.empty(),
          "a leading dot in a stem name is allowed");
}

static void test_duplicate_names_are_refused() {
    const auto choice = select_named_output({"vocals", "drums", "vocals"}, "vocals");
    check(choice.index == -1 && !choice.error.empty(),
          "two stems sharing a name are refused: one file would overwrite the other");
    check(choice.error.find("vocals") != std::string::npos,
          "the duplicate refusal names the repeated stem");
}

static void test_sibling_paths() {
    check_equal(sibling_stem_path("/generated/transform-1.wav", "drums"),
                "/generated/transform-1.drums.wav", "sibling beside an absolute dst");
    check_equal(sibling_stem_path("sep.wav", "vocals"), "sep.vocals.wav",
                "sibling of a bare file name has no directory");
    check_equal(sibling_stem_path("/out/sep", "vocals"), "/out/sep.vocals.wav",
                "an extensionless dst gets .wav");
    check_equal(sibling_stem_path("/out/take.2.wav", "bass"), "/out/take.2.bass.wav",
                "only the final extension is treated as the extension");
    check_equal(sibling_stem_path("/out/sep.WAV", "bass"), "/out/sep.bass.WAV",
                "the caller's extension spelling is preserved");
    check_equal(sibling_stem_path("/a b/c d.wav", "other"), "/a b/c d.other.wav",
                "spaces in the destination survive");

    // The property that matters: no stem can ever be written over dst itself,
    // or the "dst holds the selected stem" contract would depend on write order.
    const std::string dst = "/out/sep.wav";
    for (const auto &name : kDemucs) {
        check(sibling_stem_path(dst, name) != dst,
              "the sibling for '" + name + "' is not dst itself");
    }
    // Distinct stems must land in distinct files.
    check(sibling_stem_path(dst, "drums") != sibling_stem_path(dst, "bass"),
          "two stems get two different sibling paths");
}

// The contract grpc-server.cpp indexes on: an accepted choice over a non-empty
// name list is always in range, so the handler needs no bounds guard of its own.
// A -1 reaching the subscript would become a colossal size_t.
static void test_accepted_index_is_always_in_range() {
    const std::vector<std::vector<std::string>> lists = {
        kDemucs, kRoformer, {"solo"}, {"accompaniment", "drums"}, {"a", "b", "c"}};
    const std::vector<std::string> requests = {"", "vocals", "drums", "solo", "c",
                                               "kazoo", "..", "a/b"};
    for (const auto &names : lists) {
        for (const auto &requested : requests) {
            const auto choice = select_named_output(names, requested);
            if (!choice.error.empty()) {
                check(choice.index == -1,
                      "a refusal never carries an index (request '" + requested + "')");
                continue;
            }
            check(choice.index >= 0 &&
                      choice.index < static_cast<int>(names.size()),
                  "an accepted choice is in range (request '" + requested + "')");
            // And the selected name is the one that was asked for, when one was.
            if (!requested.empty()) {
                check(names[static_cast<size_t>(choice.index)] == requested,
                      "an accepted explicit request selects that exact name");
            }
        }
    }
}

int main() {
    test_accepted_index_is_always_in_range();
    test_default_selection();
    test_explicit_selection();
    test_unknown_stem_is_refused();
    test_no_named_outputs();
    test_unwritable_names_are_refused();
    test_duplicate_names_are_refused();
    test_sibling_paths();
    if (failures != 0) {
        fprintf(stderr, "%d check(s) failed\n", failures);
        return 1;
    }
    fprintf(stderr, "all stem_selection checks passed\n");
    return 0;
}
