// Unit tests for capability_routing. Standard library only. The harness
// compiles this as a single translation unit, so the implementation is
// included directly rather than linked.

#include "capability_routing.cpp"

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

using namespace audiocpp_backend;

// Mirrors what supertonic advertises: TTS offline and streaming.
static Capabilities supertonic() {
    return Capabilities{"supertonic",
                        {{Task::Tts, {Mode::Offline, Mode::Streaming}}}};
}

// Mirrors chatterbox: TTS, cloning and voice conversion, offline only.
static Capabilities chatterbox() {
    return Capabilities{"chatterbox",
                        {{Task::Tts, {Mode::Offline}},
                         {Task::VoiceCloning, {Mode::Offline}},
                         {Task::VoiceConversion, {Mode::Offline}}}};
}

// Mirrors nemotron_asr: ASR offline and streaming.
static Capabilities nemotron() {
    return Capabilities{"nemotron_asr",
                        {{Task::Asr, {Mode::Offline, Mode::Streaming}}}};
}

// Mirrors qwen3_asr: ASR offline only.
static Capabilities qwen3_asr() {
    return Capabilities{"qwen3_asr", {{Task::Asr, {Mode::Offline}}}};
}

// Mirrors qwen3_forced_aligner: alignment only.
static Capabilities aligner() {
    return Capabilities{"qwen3_forced_aligner",
                        {{Task::Alignment, {Mode::Offline}}}};
}

// Mirrors htdemucs: separation only.
static Capabilities htdemucs() {
    return Capabilities{"htdemucs", {{Task::SourceSeparation, {Mode::Offline}}}};
}

static void test_plain_tts() {
    const auto r = resolve_route(Rpc::Tts, RequestShape{}, chatterbox());
    check(r.ok, "plain TTS routes");
    check(r.task == Task::Tts, "plain TTS picks Tts, not VoiceCloning");
    check(r.mode == Mode::Offline, "TTS runs offline");
}

static void test_tts_with_voice_reference_prefers_cloning() {
    RequestShape shape;
    shape.has_voice_reference = true;
    const auto r = resolve_route(Rpc::Tts, shape, chatterbox());
    check(r.ok, "TTS with a voice reference routes");
    check(r.task == Task::VoiceCloning, "voice reference prefers VoiceCloning");
}

// supertonic has no VoiceCloning: a voice reference must fall back to Tts
// rather than failing the request.
static void test_tts_voice_reference_falls_back_to_tts() {
    RequestShape shape;
    shape.has_voice_reference = true;
    const auto r = resolve_route(Rpc::Tts, shape, supertonic());
    check(r.ok, "voice reference on a clone-less family still routes");
    check(r.task == Task::Tts, "falls back to Tts");
}

static void test_tts_instructions_prefer_voice_design() {
    RequestShape shape;
    shape.has_instructions = true;
    Capabilities caps{"qwen3_tts",
                      {{Task::Tts, {Mode::Offline}},
                       {Task::VoiceDesign, {Mode::Offline}}}};
    const auto r = resolve_route(Rpc::Tts, shape, caps);
    check(r.ok, "TTS with instructions routes");
    check(r.task == Task::VoiceDesign, "instructions prefer VoiceDesign");
}

// A voice reference is a stronger signal than free-form instructions: cloning
// a specific voice is what the user asked for.
static void test_voice_reference_beats_instructions() {
    RequestShape shape;
    shape.has_voice_reference = true;
    shape.has_instructions = true;
    Capabilities caps{"omnivoice",
                      {{Task::Tts, {Mode::Offline}},
                       {Task::VoiceCloning, {Mode::Offline}},
                       {Task::VoiceDesign, {Mode::Offline}}}};
    const auto r = resolve_route(Rpc::Tts, shape, caps);
    check(r.ok, "both signals present routes");
    check(r.task == Task::VoiceCloning, "voice reference outranks instructions");
}

static void test_tts_stream_requires_streaming() {
    const auto ok = resolve_route(Rpc::TtsStream, RequestShape{}, supertonic());
    check(ok.ok, "streaming TTS routes on supertonic");
    check(ok.mode == Mode::Streaming, "TTSStream runs in streaming mode");

    const auto bad = resolve_route(Rpc::TtsStream, RequestShape{}, chatterbox());
    check(!bad.ok, "streaming TTS is refused on an offline-only family");
    check(bad.error.find("chatterbox") != std::string::npos,
          "error names the family");
    check(bad.error.find("tts/offline") != std::string::npos,
          "error lists what the family does support");
    check(bad.error.find("TTSStream") != std::string::npos,
          "error names the RPC that was refused");
    check(bad.error.find("tts/streaming") != std::string::npos,
          "error lists the (task, mode) pairs that were tried");
}

static void test_transcription_stream_falls_back_to_offline() {
    const auto streaming =
        resolve_route(Rpc::AudioTranscriptionStream, RequestShape{}, nemotron());
    check(streaming.ok && streaming.mode == Mode::Streaming,
          "streaming ASR uses streaming mode when offered");

    const auto offline =
        resolve_route(Rpc::AudioTranscriptionStream, RequestShape{}, qwen3_asr());
    check(offline.ok, "streaming ASR falls back on an offline-only family");
    check(offline.mode == Mode::Offline, "fallback mode is offline");
    check(offline.task == Task::Asr, "fallback task is still Asr");
}

// Task preference dominates mode preference: it is better to run the right
// task in a fallback mode than the wrong task in the preferred mode. This is
// the only RPC where the two orderings can disagree, because it is the only
// one with more than one acceptable mode.
static void test_task_preference_beats_mode_preference() {
    RequestShape shape;
    shape.has_prompt_text = true;
    Capabilities mixed{"mixed_asr_aligner",
                       {{Task::Asr, {Mode::Offline}},
                        {Task::Alignment, {Mode::Streaming}}}};
    const auto r = resolve_route(Rpc::AudioTranscriptionStream, shape, mixed);
    check(r.ok, "mixed family routes");
    check(r.task == Task::Asr,
          "the preferred task wins even in its fallback mode");
    check(r.mode == Mode::Offline,
          "the fallback mode is accepted to keep the preferred task");
}

// Live transcription is bidirectional and cannot be faked from an offline run.
static void test_live_transcription_has_no_offline_fallback() {
    const auto r =
        resolve_route(Rpc::AudioTranscriptionLive, RequestShape{}, qwen3_asr());
    check(!r.ok, "live transcription is refused on an offline-only family");
}

static void test_alignment_needs_prompt_text() {
    const auto without =
        resolve_route(Rpc::AudioTranscription, RequestShape{}, aligner());
    check(!without.ok, "aligner without a transcript is refused");

    RequestShape shape;
    shape.has_prompt_text = true;
    const auto with = resolve_route(Rpc::AudioTranscription, shape, aligner());
    check(with.ok, "aligner with a transcript routes");
    check(with.task == Task::Alignment, "routes to Alignment");
}

// A real ASR family must not be hijacked to Alignment just because the caller
// passed a prompt: `prompt` is also whisper-style decoding context.
static void test_prompt_does_not_hijack_asr() {
    RequestShape shape;
    shape.has_prompt_text = true;
    const auto r = resolve_route(Rpc::AudioTranscription, shape, nemotron());
    check(r.ok, "ASR with a prompt routes");

    // nemotron advertises Asr alone, so the assertion has to be made against a
    // family that advertises both: otherwise "Asr is preferred" only restates
    // that Asr is the only option, and reversing the preference order passes.
    Capabilities both{"asr_with_aligner",
                      {{Task::Asr, {Mode::Offline}},
                       {Task::Alignment, {Mode::Offline}}}};
    const auto pref = resolve_route(Rpc::AudioTranscription, shape, both);
    check(pref.ok, "a family offering both routes");
    check(pref.task == Task::Asr, "Asr is preferred over Alignment");
}

static void test_audio_transform_prefers_separation() {
    const auto sep =
        resolve_route(Rpc::AudioTransform, RequestShape{}, htdemucs());
    check(sep.ok && sep.task == Task::SourceSeparation, "separation routes");

    // htdemucs advertises separation alone, so the check above cannot fail on
    // ordering. This family advertises both, which is what pins the preference.
    Capabilities sep_and_vc{"sep_and_vc",
                            {{Task::SourceSeparation, {Mode::Offline}},
                             {Task::VoiceConversion, {Mode::Offline}}}};
    const auto pref =
        resolve_route(Rpc::AudioTransform, RequestShape{}, sep_and_vc);
    check(pref.ok && pref.task == Task::SourceSeparation,
          "separation is preferred over voice conversion");

    Capabilities miocodec{"miocodec",
                          {{Task::VoiceConversion, {Mode::Offline}},
                           {Task::SpeechToSpeech, {Mode::Offline}}}};
    const auto vc = resolve_route(Rpc::AudioTransform, RequestShape{}, miocodec);
    check(vc.ok && vc.task == Task::VoiceConversion,
          "voice conversion is preferred over speech-to-speech");
}

static void test_pinned_task_overrides_routing() {
    RequestShape shape;
    shape.pinned_task = "s2s";
    Capabilities miocodec{"miocodec",
                          {{Task::VoiceConversion, {Mode::Offline}},
                           {Task::SpeechToSpeech, {Mode::Offline}}}};
    const auto r = resolve_route(Rpc::AudioTransform, shape, miocodec);
    check(r.ok && r.task == Task::SpeechToSpeech, "pinned task wins");

    RequestShape bad;
    bad.pinned_task = "not-a-task";
    const auto e = resolve_route(Rpc::AudioTransform, bad, miocodec);
    check(!e.ok, "an unknown pinned task is an error");
    check(e.error.find("not-a-task") != std::string::npos,
          "error names the bad task");

    // A pinned task the family does not offer must fail, not silently reroute.
    RequestShape unsupported;
    unsupported.pinned_task = "sep";
    const auto u = resolve_route(Rpc::AudioTransform, unsupported, miocodec);
    check(!u.ok, "a pinned but unsupported task is refused");
}

// A pin lives on the MODEL, and every one of the nine handlers copies it into
// the shape, so a pin set for one RPC arrives at all of them. It used to
// replace the candidate list wholesale, which turned the other eight into wrong
// 200s rather than errors: nemotron pinned to asr made Vad answer with zero
// segments after a full ASR decode, and silero_vad pinned to vad made
// AudioTranscription answer with empty text and four segments whose spans were
// VAD segments, which the srt/vtt/lrc writers rendered as timed EMPTY cues.
static void test_pin_must_be_admissible_for_the_rpc() {
    Capabilities nemotron_asr{"nemotron_asr",
                              {{Task::Asr, {Mode::Offline, Mode::Streaming}},
                               {Task::Vad, {Mode::Offline}}}};

    // The pin is legitimate on the RPC it was meant for.
    RequestShape asr_pin;
    asr_pin.pinned_task = "asr";
    const auto transcription =
        resolve_route(Rpc::AudioTranscription, asr_pin, nemotron_asr);
    check(transcription.ok && transcription.task == Task::Asr,
          "an admissible pin is still honoured exactly");

    // ...and refused on one that never routes to it, EVEN THOUGH the family
    // advertises the pinned task. That is the whole point: family support is
    // not the question, RPC admissibility is.
    const auto vad = resolve_route(Rpc::Vad, asr_pin, nemotron_asr);
    check(!vad.ok, "an inadmissible pin is refused rather than served");
    check(vad.error.find("asr") != std::string::npos,
          "the refusal names the pinned task");
    check(vad.error.find(rpc_name(Rpc::Vad)) != std::string::npos,
          "the refusal names the RPC that cannot serve it");

    Capabilities silero{"silero_vad", {{Task::Vad, {Mode::Offline}}}};
    RequestShape vad_pin;
    vad_pin.pinned_task = "vad";
    const auto vad_ok = resolve_route(Rpc::Vad, vad_pin, silero);
    check(vad_ok.ok && vad_ok.task == Task::Vad, "vad is admissible on Vad");
    const auto transcribe_vad =
        resolve_route(Rpc::AudioTranscription, vad_pin, silero);
    check(!transcribe_vad.ok,
          "a vad pin cannot make a transcription request return empty cues");

    // Every pin a shipped configuration could sensibly set stays reachable on
    // the RPC that serves it. This is the list the fix was checked against.
    struct AdmissibleCase {
        Rpc rpc;
        const char *task;
    };
    const AdmissibleCase kAdmissible[] = {
        {Rpc::AudioTransform, "svc"},   {Rpc::AudioTransform, "sep"},
        {Rpc::AudioTransform, "vc"},    {Rpc::AudioTransform, "s2s"},
        {Rpc::Tts, "tts"},              {Rpc::Tts, "clon"},
        {Rpc::Tts, "vdes"},             {Rpc::TtsStream, "tts"},
        {Rpc::AudioTranscription, "asr"},
        {Rpc::AudioTranscription, "align"},
        {Rpc::AudioTranscriptionStream, "asr"},
        {Rpc::AudioTranscriptionLive, "asr"},
        {Rpc::Vad, "vad"},              {Rpc::Diarize, "diar"},
        {Rpc::SoundGeneration, "gen"},
    };
    for (const auto &entry : kAdmissible) {
        Task task = Task::Tts;
        check(parse_task_name(entry.task, task),
              std::string("known task name: ") + entry.task);
        // A family that advertises the pinned task offline and nothing else, so
        // the ONLY thing that can refuse the route is the admissibility check.
        Capabilities only{"probe",
                          {{task, {Mode::Offline, Mode::Streaming}}}};
        RequestShape pin;
        pin.pinned_task = entry.task;
        const auto route = resolve_route(entry.rpc, pin, only);
        check(route.ok && route.task == task,
              std::string("pin '") + entry.task + "' stays admissible on " +
                  rpc_name(entry.rpc));
    }

    // And the pins that must NOT cross over, one per RPC pair that was
    // observed producing a wrong 200.
    const AdmissibleCase kInadmissible[] = {
        {Rpc::Vad, "asr"},          {Rpc::Diarize, "asr"},
        {Rpc::AudioTranscription, "vad"}, {Rpc::AudioTranscription, "diar"},
        {Rpc::Tts, "asr"},          {Rpc::Vad, "tts"},
        {Rpc::SoundGeneration, "tts"},    {Rpc::AudioTransform, "asr"},
    };
    for (const auto &entry : kInadmissible) {
        Task task = Task::Tts;
        check(parse_task_name(entry.task, task),
              std::string("known task name: ") + entry.task);
        Capabilities only{"probe",
                          {{task, {Mode::Offline, Mode::Streaming}}}};
        RequestShape pin;
        pin.pinned_task = entry.task;
        const auto route = resolve_route(entry.rpc, pin, only);
        check(!route.ok,
              std::string("pin '") + entry.task + "' is refused on " +
                  rpc_name(entry.rpc));
    }
}

static void test_vad_and_diarize() {
    Capabilities silero{"silero_vad", {{Task::Vad, {Mode::Offline, Mode::Streaming}}}};
    const auto v = resolve_route(Rpc::Vad, RequestShape{}, silero);
    check(v.ok && v.task == Task::Vad && v.mode == Mode::Offline, "VAD routes offline");

    const auto d = resolve_route(Rpc::Diarize, RequestShape{}, silero);
    check(!d.ok, "diarization is refused on a VAD-only family");

    Capabilities sortformer{"sortformer_diar", {{Task::Diarization, {Mode::Offline}}}};
    const auto ok = resolve_route(Rpc::Diarize, RequestShape{}, sortformer);
    check(ok.ok && ok.task == Task::Diarization, "diarization routes");
}

static void test_sound_generation() {
    Capabilities stable{"stable_audio", {{Task::AudioGeneration, {Mode::Offline}}}};
    const auto r = resolve_route(Rpc::SoundGeneration, RequestShape{}, stable);
    check(r.ok && r.task == Task::AudioGeneration, "sound generation routes");
}

static void test_names_round_trip() {
    const Task all[] = {Task::Vad, Task::Asr, Task::Diarization,
                        Task::SourceSeparation, Task::AudioGeneration, Task::Tts,
                        Task::VoiceCloning, Task::VoiceConversion,
                        Task::SpeechToSpeech, Task::Alignment, Task::VoiceDesign,
                        Task::SpeakerRecognition, Task::Svc, Task::Midi};
    for (const Task t : all) {
        Task parsed = Task::Vad;
        const bool ok = parse_task_name(task_name(t), parsed);
        check(ok && parsed == t,
              std::string("task name round-trips: ") + task_name(t));
    }
    check(std::string(mode_name(Mode::Offline)) == "offline", "offline name");
    check(std::string(mode_name(Mode::Streaming)) == "streaming", "streaming name");

    // The emitted name must be the one audio.cpp itself prints and parses
    // (framework/runtime/session.cpp), because `task:` is user-facing: a name
    // copied out of audio.cpp has to be accepted here, and a name pinned here
    // has to survive conversion at the engine boundary.
    check(std::string(task_name(Task::SpeakerRecognition)) == "spk",
          "speaker recognition emits upstream's name 'spk'");

    Task pinned = Task::Vad;
    check(parse_task_name("spk", pinned) && pinned == Task::SpeakerRecognition,
          "'spk' parses to SpeakerRecognition");

    // Accepted as a legacy alias so configs written against the earlier name
    // keep working, but never emitted.
    Task alias = Task::Vad;
    check(parse_task_name("spkrec", alias) && alias == Task::SpeakerRecognition,
          "'spkrec' is still accepted as an alias");
}

static void test_describe_capabilities() {
    const std::string described = describe_capabilities(nemotron());
    check(described.find("asr/offline") != std::string::npos,
          "description lists asr/offline");
    check(described.find("asr/streaming") != std::string::npos,
          "description lists asr/streaming");
}

static void test_empty_capabilities() {
    const auto r = resolve_route(Rpc::Tts, RequestShape{}, Capabilities{"mystery", {}});
    check(!r.ok, "a family advertising nothing is refused");
    check(r.error.find("mystery") != std::string::npos, "error names the family");
}

// The table is indexed by UnsupportedRpc's underlying value, so a reordering of
// either list silently pairs an RPC with another's reason. Nothing else would
// catch that: both sides still compile and every message still reads plausibly.
static void test_unsupported_surface_table_matches_the_enum() {
    check(unsupported_surfaces().size() == 5,
          "all five unsupported surfaces are tabulated");
    check(std::string(unsupported_surface(UnsupportedRpc::AudioEncode).rpc) ==
              "AudioEncode",
          "UnsupportedRpc::AudioEncode indexes AudioEncode");
    check(std::string(unsupported_surface(UnsupportedRpc::AudioDecode).rpc) ==
              "AudioDecode",
          "UnsupportedRpc::AudioDecode indexes AudioDecode");
    check(std::string(
              unsupported_surface(UnsupportedRpc::AudioTransformStream).rpc) ==
              "AudioTransformStream",
          "UnsupportedRpc::AudioTransformStream indexes AudioTransformStream");
    check(std::string(
              unsupported_surface(UnsupportedRpc::AudioToAudioStream).rpc) ==
              "AudioToAudioStream",
          "UnsupportedRpc::AudioToAudioStream indexes AudioToAudioStream");
    check(std::string(unsupported_surface(UnsupportedRpc::VoiceEmbed).rpc) ==
              "VoiceEmbed",
          "UnsupportedRpc::VoiceEmbed indexes VoiceEmbed");

    // Two entries may share a reason (the codec pair does), but two entries
    // naming the same RPC would mean one of the five is unreachable.
    for (size_t i = 0; i < unsupported_surfaces().size(); ++i) {
        for (size_t j = i + 1; j < unsupported_surfaces().size(); ++j) {
            check(std::string(unsupported_surfaces()[i].rpc) !=
                      unsupported_surfaces()[j].rpc,
                  std::string("no duplicate RPC name at ") + std::to_string(i) +
                      "/" + std::to_string(j));
        }
    }
}

// There is no out-of-range test for unsupported_surface(). It switches over the
// enumerators with no default label, so a sixth UnsupportedRpc without a case is
// a -Wswitch diagnostic at build time and cannot reach a run-time check at all.

// Every entry, not just the one somebody remembered to cover. A refusal that
// drops the family, the RPC or the reason is a refusal the caller cannot act
// on, which is the entire point of this surface existing.
static void test_every_unsupported_surface_message_is_diagnosable() {
    for (const auto &surface : unsupported_surfaces()) {
        const std::string label = std::string(" [") + surface.rpc + "]";
        const std::string loaded =
            unsupported_surface_message(nemotron(), surface.rpc, surface.reason);

        check(loaded.find(surface.rpc) != std::string::npos,
              "message names the RPC" + label);
        check(std::string(surface.reason).size() > 20 &&
                  loaded.find(surface.reason) != std::string::npos,
              "message gives a substantive upstream reason" + label);
        check(loaded.find("nemotron_asr") != std::string::npos,
              "message names the loaded family" + label);
        check(loaded.find("asr/offline") != std::string::npos &&
                  loaded.find("asr/streaming") != std::string::npos,
              "message lists what the family does support" + label);

        // The no-model form keeps the two facts that do not depend on a model
        // and drops only the one that does, so the caller still learns why.
        const std::string unloaded =
            unsupported_surface_message(surface.rpc, surface.reason);
        check(unloaded.find(surface.rpc) != std::string::npos,
              "no-model message names the RPC" + label);
        check(unloaded.find(surface.reason) != std::string::npos,
              "no-model message gives the upstream reason" + label);
        check(unloaded.find("nemotron_asr") == std::string::npos,
              "no-model message names no family" + label);
        // Without this the caller's obvious next move is to load a model and
        // retry, which cannot work: the refusal is a property of the engine.
        check(unloaded.find("would not change this answer") != std::string::npos,
              "no-model message says loading a model would not help" + label);
    }
}

// The reasons are the load-bearing half of this feature and each was checked
// against the pinned upstream checkout. Pinning the distinguishing phrase here
// means a later edit that guts one into a generic "not supported" fails rather
// than passes quietly.
static void test_unsupported_reasons_name_the_upstream_limitation() {
    const auto &encode = unsupported_surface(UnsupportedRpc::AudioEncode);
    const auto &decode = unsupported_surface(UnsupportedRpc::AudioDecode);
    check(std::string(encode.reason).find("VoiceTaskKind") != std::string::npos &&
              std::string(encode.reason).find("codec") != std::string::npos,
          "the AudioEncode reason names the missing VoiceTaskKind entry");
    // miocodec is the family a reader will reach for first, because upstream's
    // README tags it Codec. Naming it and its actual advertised tasks is what
    // stops the next person re-deriving the same dead end.
    check(std::string(encode.reason).find("miocodec") != std::string::npos,
          "the AudioEncode reason disposes of miocodec's README Codec tag");
    check(std::string(encode.reason) == decode.reason,
          "AudioEncode and AudioDecode refuse for the same reason");

    const auto &transform =
        unsupported_surface(UnsupportedRpc::AudioTransformStream);
    // The reason must be scoped to the tasks AudioTransform routes to. The
    // broader claim, "upstream streams tts and asr only", is FALSE: silero_vad
    // advertises vad with RunMode::Streaming. A refusal resting on a false
    // premise is worse than a bare UNIMPLEMENTED, because it will be believed.
    check(std::string(transform.reason).find("sep, vc, svc, s2s") !=
              std::string::npos,
          "the AudioTransformStream reason is scoped to the routed tasks");
    check(std::string(transform.reason).find("tts, asr and vad") !=
              std::string::npos,
          "the AudioTransformStream reason counts vad among the streaming tasks");
    check(std::string(transform.reason).find("tts and asr only") ==
              std::string::npos,
          "the AudioTransformStream reason does not repeat the refuted claim");
    // An absence is not an impossibility. A sep family could be buffered and
    // emitted as a stream, so the reason has to say this backend declines to
    // rather than cannot, or it overreaches on a true premise.
    check(std::string(transform.reason).find("buffered offline call in disguise") !=
              std::string::npos,
          "the AudioTransformStream reason does not overclaim impossibility");

    const auto &s2s = unsupported_surface(UnsupportedRpc::AudioToAudioStream);
    check(std::string(s2s.reason).find("Realtime") != std::string::npos &&
              std::string(s2s.reason).find("clip-to-clip") != std::string::npos,
          "the AudioToAudioStream reason contrasts the two contracts");
    // Naming both s2s families, and what each of them actually does, makes the
    // claim checkable. It must NOT say s2s is voice conversion full stop: that
    // is true of miocodec and false of vevo2, whose s2s route is `editing` and
    // rewrites the spoken content against a target voice.
    check(std::string(s2s.reason).find("miocodec (voice conversion)") !=
                  std::string::npos &&
              std::string(s2s.reason).find("vevo2 (speech editing)") !=
                  std::string::npos,
          "the AudioToAudioStream reason names each s2s family's actual task");
    check(std::string(s2s.reason).find("s2s is offline voice conversion") ==
              std::string::npos,
          "the AudioToAudioStream reason does not miscast vevo2 as conversion");

    const auto &embed = unsupported_surface(UnsupportedRpc::VoiceEmbed);
    check(std::string(embed.reason).find("spk") != std::string::npos,
          "the VoiceEmbed reason names the task no family advertises");
    // spk IS a VoiceTaskKind upstream; what is missing is any family that
    // advertises it. Saying the kind does not exist would be false, and would
    // send a reader looking in the wrong place.
    check(std::string(embed.reason).find("no audio.cpp family") !=
              std::string::npos,
          "the VoiceEmbed reason blames the families, not the enum");
    // The speaker encoders DO exist upstream, as conditioning modules inside
    // TTS and VC families. Not saying so invites "but audio.cpp ships TitaNet".
    check(std::string(embed.reason).find("TitaNet") != std::string::npos,
          "the VoiceEmbed reason disposes of the internal speaker encoders");
    Task parsed = Task::Vad;
    check(parse_task_name("spk", parsed) && parsed == Task::SpeakerRecognition,
          "spk is a real task kind, so the reason must not claim otherwise");
}

// A family advertising nothing still gets a message that reads, rather than one
// trailing off after "supports: ".
static void test_unsupported_surface_message_with_empty_capabilities() {
    const auto &embed = unsupported_surface(UnsupportedRpc::VoiceEmbed);
    const std::string message = unsupported_surface_message(
        Capabilities{"mystery", {}}, embed.rpc, embed.reason);
    check(message.find("mystery") != std::string::npos,
          "empty-capability message still names the family");
    check(message.find("supports: nothing") != std::string::npos,
          "empty-capability message says the family supports nothing");
}

int main() {
    test_plain_tts();
    test_tts_with_voice_reference_prefers_cloning();
    test_tts_voice_reference_falls_back_to_tts();
    test_tts_instructions_prefer_voice_design();
    test_voice_reference_beats_instructions();
    test_tts_stream_requires_streaming();
    test_transcription_stream_falls_back_to_offline();
    test_task_preference_beats_mode_preference();
    test_live_transcription_has_no_offline_fallback();
    test_alignment_needs_prompt_text();
    test_prompt_does_not_hijack_asr();
    test_audio_transform_prefers_separation();
    test_pinned_task_overrides_routing();
    test_pin_must_be_admissible_for_the_rpc();
    test_vad_and_diarize();
    test_sound_generation();
    test_names_round_trip();
    test_describe_capabilities();
    test_empty_capabilities();
    test_unsupported_surface_table_matches_the_enum();
    test_every_unsupported_surface_message_is_diagnosable();
    test_unsupported_reasons_name_the_upstream_limitation();
    test_unsupported_surface_message_with_empty_capabilities();
    if (failures) {
        fprintf(stderr, "%d check(s) failed\n", failures);
        return 1;
    }
    fprintf(stderr, "all capability_routing checks passed\n");
    return 0;
}
