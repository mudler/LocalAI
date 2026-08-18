#pragma once

// Decides which audio.cpp (task, mode) pair serves a given LocalAI RPC, or
// produces the capability error when none can. Standard library only, so this
// unit is tested without an audio.cpp checkout; loaded_model.cpp converts
// to and from engine::runtime types at the boundary.

#include <string>
#include <vector>

namespace audiocpp_backend {

// Mirrors engine::runtime::VoiceTaskKind, same members and same order.
enum class Task {
    Vad,
    Asr,
    Diarization,
    SourceSeparation,
    AudioGeneration,
    Tts,
    VoiceCloning,
    VoiceConversion,
    SpeechToSpeech,
    Alignment,
    VoiceDesign,
    SpeakerRecognition,
    Svc,
    Midi,
};

// Mirrors engine::runtime::RunMode.
enum class Mode { Offline, Streaming };

struct TaskCapability {
    Task task = Task::Vad;
    std::vector<Mode> modes;
};

struct Capabilities {
    std::string family;
    std::vector<TaskCapability> tasks;
};

// The LocalAI RPCs this backend serves. The ones it cannot serve at all are in
// UnsupportedRpc below rather than here: they never reach routing, because no
// family could satisfy them.
enum class Rpc {
    Tts,
    TtsStream,
    AudioTranscription,
    AudioTranscriptionStream,
    AudioTranscriptionLive,
    Vad,
    Diarize,
    SoundGeneration,
    AudioTransform,
};

struct RequestShape {
    // A speaker reference clip was supplied (TTSRequest.voice resolved to audio).
    bool has_voice_reference = false;
    // TTSRequest.instructions is set.
    bool has_instructions = false;
    // TranscriptRequest.prompt is set.
    bool has_prompt_text = false;
    // The model's `task:` option, empty when unset. Overrides routing.
    std::string pinned_task;
};

struct Route {
    bool ok = false;
    Task task = Task::Tts;
    Mode mode = Mode::Offline;
    // Set when ok is false. Suitable verbatim as an UNIMPLEMENTED message.
    std::string error;
};

Route resolve_route(Rpc rpc, const RequestShape &shape, const Capabilities &caps);

// Canonical audio.cpp short names: gen, tts, clon, vc, svc, s2s, asr, align,
// vad, diar, sep, vdes, spk. parse_task_name additionally accepts "spkrec" as
// a legacy alias; task_name only ever emits "spk".
const char *task_name(Task task);
const char *mode_name(Mode mode);
const char *rpc_name(Rpc rpc);
bool parse_task_name(const std::string &value, Task &out);

// "asr/offline, asr/streaming", for error messages.
std::string describe_capabilities(const Capabilities &caps);

// The RPCs in LocalAI's backend contract that audio.cpp has no counterpart for,
// as opposed to the ones in Rpc above, which a particular family may or may not
// be able to serve. Nothing routes to these: the refusal is a property of the
// engine, not of the loaded model, so loading a different family cannot change
// it.
//
// This is deliberately NOT deferred work. Each entry names the upstream
// limitation that keeps it out of Rpc, and each becomes an ordinary routing
// entry the day upstream lifts that limitation.
enum class UnsupportedRpc {
    AudioEncode,
    AudioDecode,
    AudioTransformStream,
    AudioToAudioStream,
    VoiceEmbed,
};

struct UnsupportedSurface {
    // The RPC's name as backend.proto spells it, for the message.
    const char *rpc;
    // Why audio.cpp cannot serve it, in terms of what upstream does and does
    // not have. Stated so a caller can tell "not built yet" from "not possible".
    const char *reason;
};

// The table behind the five refusals. Exposed whole so a test can assert every
// entry rather than the one somebody remembered to cover, and so the reasons
// are data in one place instead of string literals hand-copied into handlers.
const std::vector<UnsupportedSurface> &unsupported_surfaces();
const UnsupportedSurface &unsupported_surface(UnsupportedRpc rpc);

// Message for an RPC this backend cannot serve at all, as opposed to one this
// particular family cannot serve. `reason` states the upstream limitation.
std::string unsupported_surface_message(const Capabilities &caps, const char *rpc,
                                        const char *reason);

// Same, for the no-model-loaded case. Says so explicitly rather than naming an
// empty family, and says that loading one would not help, because the caller's
// obvious next move otherwise is to load a model and try again.
std::string unsupported_surface_message(const char *rpc, const char *reason);

} // namespace audiocpp_backend
