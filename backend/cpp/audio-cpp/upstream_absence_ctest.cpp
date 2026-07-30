// Asserts the ABSENCES that capability_routing.cpp's five refusal reasons rest
// on, against the engine itself rather than against somebody's reading of it.
//
// Those reasons are prose making checkable claims about a pinned third-party
// checkout: "VoiceTaskKind has no codec entry", "no family advertises spk",
// "miocodec advertises only vc and s2s". Prose rots silently across an
// AUDIO_CPP_VERSION bump, and it rots in the worst possible place, since a
// refusal that states a false fact is worse than a bare UNIMPLEMENTED: it will
// be believed. One of the four claims this backend was planned against was
// already false when it was written ("streaming exists for tts and asr only" is
// contradicted by silero_vad). This test is what turns the next such change
// from a silent lie on the wire into a build failure.
//
// Pinned at audio.cpp e800d435d130dc776baf6f3e6129bb62b1495c89. What follows
// was true of that commit; a bump is exactly when it needs to be re-run.
//
// It links engine_runtime and queries make_default_registry(), touching only
// include/engine/framework/**, like every other unit in this backend. It loads
// no model and reads no file: advertise_loaders() is the path-free catalog
// upstream publishes for --list-loaders.
//
// FOUR CAVEATS, so nobody reads more into a green run than it earns:
//
//   1. It cannot assert the AudioToAudioStream reason. That one contrasts
//      LocalAI's OpenAI-Realtime contract (conversation, system prompt, tool
//      loop) with what audio.cpp's s2s families actually do, and semantics are
//      not a queryable property. What IS asserted is the enumerable half: that
//      s2s is advertised by exactly miocodec and vevo2, which is the clause the
//      message names by hand.
//   2. It queries the LOADER catalog, while grpc-server.cpp reads the LOADED
//      model's own capabilities(). The two agree today, cross-checked on the
//      wire: this test asserts miocodec advertises {vc/offline, s2s/offline},
//      and a live LoadModel of miocodec-q8_0.gguf reports exactly
//      "vc/offline, s2s/offline". A family whose loaded capabilities diverged
//      from its advertisement would slip past, but nothing loads without a
//      model file and a ctest cannot depend on one.
//   3. Capabilities need not come from a loader at all.
//      src/framework/model_spec/metadata.cpp's advertised_capabilities() builds
//      a CapabilitySet from a spec's "capabilities"/"tasks"/"modes" keys, which
//      is a second route by which a bump could falsify claim 3. Today no
//      shipped model_specs/*.json carries a top-level "tasks" key and no loader
//      calls that function, so the route is dead. It is covered anyway to the
//      extent that a loader adopting it would surface through advertise_loaders
//      like any other capability, which is why every assertion below queries
//      advertised capabilities rather than loader source.
//   4. An absence test passes trivially when the query is broken, so
//      test_catalog_is_populated below is a POSITIVE control and is not
//      optional. It proves the registry is non-empty and that the query does
//      find a capability it should, before any absence is believed.

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

// The AudioTransformStream premise, and the one that has to be scoped exactly.
// The broad claim "upstream streams tts and asr only" is FALSE: silero_vad
// advertises vad/streaming. The claim that holds is that none of the four tasks
// AudioTransform routes to is advertised streaming by anybody.
//
// NEGATIVE CONTROL: add VoiceTaskKind::Tts to kTransformTasks and this test must
// fail, naming the tts families. A run where that edit stays green means the
// query is broken and every absence above is worthless.
void test_no_streaming_for_the_transform_tasks() {
    const VoiceTaskKind kTransformTasks[] = {
        VoiceTaskKind::SourceSeparation,
        VoiceTaskKind::VoiceConversion,
        VoiceTaskKind::Svc,
        VoiceTaskKind::SpeechToSpeech,
    };

    std::string offenders;
    for (const VoiceTaskKind task : kTransformTasks) {
        for (const auto &loader : catalog()) {
            if (advertises(loader, task, RunMode::Streaming)) {
                if (!offenders.empty()) {
                    offenders += ", ";
                }
                offenders += loader.family;
                offenders += "/";
                offenders += engine::runtime::to_string(task);
            }
        }
    }
    check(offenders.empty(),
          "no streaming for sep/vc/svc/s2s" +
              (offenders.empty() ? std::string() : " (found: " + offenders + ")"));
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

// The "declared only by" clause in the AudioToAudioStream message. Exact set,
// for the same reason as above: a third s2s family would make the message stale
// without making it obviously wrong.
void test_speech_to_speech_is_exactly_miocodec_and_vevo2() {
    std::set<std::string> families;
    for (const auto &loader : catalog()) {
        if (advertises_any_mode(loader, VoiceTaskKind::SpeechToSpeech)) {
            families.insert(loader.family);
        }
    }
    const std::set<std::string> expected = {"miocodec", "vevo2"};
    std::string found;
    for (const auto &family : families) {
        if (!found.empty()) {
            found += ", ";
        }
        found += family;
    }
    check(families == expected,
          "s2s is advertised by exactly miocodec and vevo2 (got: " +
              (found.empty() ? "nothing" : found) + ")");
}

} // namespace

int main() {
    test_catalog_is_populated();
    test_no_codec_task_kind();
    test_no_family_advertises_speaker_recognition();
    test_no_streaming_for_the_transform_tasks();
    test_miocodec_advertises_exactly_vc_and_s2s();
    test_speech_to_speech_is_exactly_miocodec_and_vevo2();
    if (failures) {
        fprintf(stderr,
                "%d upstream absence check(s) failed. A refusal message in "
                "capability_routing.cpp now states something that is not true "
                "of the pinned audio.cpp; fix the message, not this test.\n",
                failures);
        return 1;
    }
    fprintf(stderr, "all upstream absence checks passed\n");
    return 0;
}
