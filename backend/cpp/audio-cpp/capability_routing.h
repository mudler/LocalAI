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

// The LocalAI RPCs this backend serves. The unimplemented ones are not listed:
// they never reach routing.
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

// Canonical audio.cpp CLI short names: gen, tts, clon, vc, svc, s2s, asr,
// align, vad, diar, sep, vdes, spkrec.
const char *task_name(Task task);
const char *mode_name(Mode mode);
const char *rpc_name(Rpc rpc);
bool parse_task_name(const std::string &value, Task &out);

// "asr/offline, asr/streaming", for error messages.
std::string describe_capabilities(const Capabilities &caps);

} // namespace audiocpp_backend
