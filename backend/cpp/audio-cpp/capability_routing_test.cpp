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
                        Task::SpeakerRecognition, Task::Svc};
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
    test_vad_and_diarize();
    test_sound_generation();
    test_names_round_trip();
    test_describe_capabilities();
    test_empty_capabilities();
    if (failures) {
        fprintf(stderr, "%d check(s) failed\n", failures);
        return 1;
    }
    fprintf(stderr, "all capability_routing checks passed\n");
    return 0;
}
