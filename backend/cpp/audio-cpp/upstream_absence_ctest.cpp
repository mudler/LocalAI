// Checks upstream premises used by the backend's refusal messages against
// audio.cpp ad14f0c4dbd5b12b7711bb7b46abb8d94f78f067. Codec and speaker
// recognition remain absent; streaming transforms and conversational S2S do
// NOT. Exact catalog checks make future pin drift visible without exposing
// new RPCs. The backend still needs adapters for those streaming contracts.
//
// Queries loader advertisements, not loaded-model capabilities: no models,
// inference or files are needed. A loaded model may differ from its loader's
// advertisement. Positive controls keep absence checks from passing vacuously.

#include "engine/framework/runtime/registry.h"
#include "engine/framework/runtime/session.h"

#include <algorithm>
#include <cstdio>
#include <set>
#include <stdexcept>
#include <string>
#include <vector>

namespace {

int failures = 0;

void check(bool ok, const std::string &name) {
    if (!ok) {
        failures++;
        fprintf(stderr, "FAIL: %s\n", name.c_str());
    } else {
        fprintf(stderr, "ok:   %s\n", name.c_str());
    }
}

using engine::runtime::LoaderAdvertisement;
using engine::runtime::RunMode;
using engine::runtime::VoiceTaskKind;

// Built once. make_default_registry constructs every loader, which is the
// expensive part, and none of the assertions mutates it.
const std::vector<LoaderAdvertisement> &catalog() {
    static const std::vector<LoaderAdvertisement> kCatalog =
        engine::runtime::make_default_registry().advertise_loaders();
    return kCatalog;
}

bool advertises(const LoaderAdvertisement &loader, VoiceTaskKind task,
                RunMode mode) {
    for (const auto &capability : loader.capabilities.supported_tasks) {
        if (capability.task != task) {
            continue;
        }
        return std::find(capability.modes.begin(), capability.modes.end(), mode) !=
               capability.modes.end();
    }
    return false;
}

bool advertises_any_mode(const LoaderAdvertisement &loader, VoiceTaskKind task) {
    return advertises(loader, task, RunMode::Offline) ||
           advertises(loader, task, RunMode::Streaming);
}

std::string describe(const LoaderAdvertisement &loader) {
    std::string out;
    for (const auto &capability : loader.capabilities.supported_tasks) {
        for (const RunMode mode : capability.modes) {
            if (!out.empty()) {
                out += ", ";
            }
            out += engine::runtime::to_string(capability.task);
            out += "/";
            out += engine::runtime::to_string(mode);
        }
    }
    return out.empty() ? "nothing" : out;
}

// THE POSITIVE CONTROL. Every other test here asserts an absence, and an
// absence is what a broken query returns for everything. If make_default_registry
// ever returns an empty registry, or advertise_loaders stops populating modes,
// this is the test that fails instead of the suite going quietly green while
// asserting nothing.
void test_catalog_is_populated() {
    check(catalog().size() >= 20,
          "the default registry advertises a plausible number of families (" +
              std::to_string(catalog().size()) + ")");

    bool found_streaming_asr = false;
    for (const auto &loader : catalog()) {
        if (advertises(loader, VoiceTaskKind::Asr, RunMode::Streaming)) {
            found_streaming_asr = true;
            break;
        }
    }
    check(found_streaming_asr,
          "the query finds a capability that IS advertised (asr/streaming)");
}

// The AudioEncode and AudioDecode premise. Not "no family does codec" but the
// stronger "the task kind does not exist", which is what makes those two RPCs
// unroutable rather than merely unserved.
//
// Note what is NOT asserted: model_spec/schema.cpp's task whitelist DOES accept
// the string "codec", so a spec declaring it validates and then fails here. That
// is a hole in upstream's own validation, and it is deliberately kept off the
// wire; the refusal rests on this parser, which is the thing routing would have
// to go through.
void test_no_codec_task_kind() {
    bool threw = false;
    try {
        (void)engine::runtime::parse_voice_task_kind("codec");
    } catch (const std::exception &) {
        threw = true;
    }
    check(threw, "no codec task kind: parse_voice_task_kind(\"codec\") throws");

    // The control for the line above: a real name must NOT throw, or the test
    // would pass against a parser that rejected everything.
    bool tts_threw = false;
    try {
        (void)engine::runtime::parse_voice_task_kind("tts");
    } catch (const std::exception &) {
        tts_threw = true;
    }
    check(!tts_threw, "parse_voice_task_kind accepts a real name (\"tts\")");
}

// The VoiceEmbed premise. SpeakerRecognition IS in the enum, and TitaNet and
// ECAPA-TDNN exist as internal conditioning encoders; what is missing is any
// registered family advertising the task, which is what the message says.
void test_no_family_advertises_speaker_recognition() {
    std::string offenders;
    for (const auto &loader : catalog()) {
        if (advertises_any_mode(loader, VoiceTaskKind::SpeakerRecognition)) {
            if (!offenders.empty()) {
                offenders += ", ";
            }
            offenders += loader.family;
        }
    }
    check(offenders.empty(),
          "no family advertises spk" +
              (offenders.empty() ? std::string() : " (found: " + offenders + ")"));
}

// Streaming upstream is real, but does not supply a backend RPC adapter.
void test_streaming_transform_catalog() {
    const VoiceTaskKind tasks[] = {
        VoiceTaskKind::SourceSeparation, VoiceTaskKind::VoiceConversion,
        VoiceTaskKind::Svc, VoiceTaskKind::SpeechToSpeech,
    };
    std::set<std::string> found;
    for (const auto task : tasks) {
        for (const auto &loader : catalog()) {
            if (advertises(loader, task, RunMode::Streaming)) {
                found.insert(loader.family + "/" + engine::runtime::to_string(task));
            }
        }
    }
    const std::set<std::string> expected = {
        "meanvc2/vc", "lfm2_audio/s2s", "lfm3_audio/s2s", "personaplex/s2s",
    };
    check(found == expected,
          "streaming transform catalog is exactly meanvc2/vc and lfm2_audio, lfm3_audio, personaplex/s2s");
}

// The clause the AudioEncode message names by hand: miocodec carries a Codec tag
// in upstream's README, and its loader advertises only vc and s2s. Asserted
// exactly, not as a subset, so a bump that ADDS a codec capability to miocodec
// fails here rather than leaving the message stale.
void test_miocodec_advertises_exactly_vc_and_s2s() {
    const LoaderAdvertisement *miocodec = nullptr;
    for (const auto &loader : catalog()) {
        if (loader.family == "miocodec") {
            miocodec = &loader;
            break;
        }
    }
    if (miocodec == nullptr) {
        check(false, "miocodec is a registered family");
        return;
    }
    check(describe(*miocodec) == "vc/offline, s2s/offline",
          "miocodec advertises exactly vc/offline, s2s/offline (got: " +
              describe(*miocodec) + ")");
}

// The current pin has more than the original two clip-to-clip S2S families.
void test_speech_to_speech_catalog() {
    std::set<std::string> families;
    for (const auto &loader : catalog()) {
        if (advertises_any_mode(loader, VoiceTaskKind::SpeechToSpeech)) {
            families.insert(loader.family);
        }
    }
    const std::set<std::string> expected = {
        "apollo", "audiosr", "builtin_audio_utils", "lfm2_audio", "lfm3_audio",
        "ming3_audio", "miocodec", "personaplex", "reuse", "sam_audio", "sidon",
        "universr", "vevo2",
    };
    std::string found;
    for (const auto &family : families) {
        if (!found.empty()) {
            found += ", ";
        }
        found += family;
    }
    check(families == expected,
          "s2s families match the pinned catalog (got: " +
              (found.empty() ? "nothing" : found) + ")");
}

} // namespace

int main() {
    test_catalog_is_populated();
    test_no_codec_task_kind();
    test_no_family_advertises_speaker_recognition();
    test_streaming_transform_catalog();
    test_miocodec_advertises_exactly_vc_and_s2s();
    test_speech_to_speech_catalog();
    if (failures) {
        fprintf(stderr,
                "%d upstream capability check(s) failed. Review the pinned catalog "
                "and capability_routing.cpp refusal premises together.\n",
                failures);
        return 1;
    }
    fprintf(stderr, "all upstream absence checks passed\n");
    return 0;
}
