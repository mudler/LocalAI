#include "capability_routing.h"

#include <algorithm>

namespace audiocpp_backend {
namespace {

struct NamedTask {
    Task task;
    const char *name;
};

// Short names are exactly the strings audio.cpp prints and parses in
// framework/runtime/session.cpp, so a name pinned here survives conversion at
// the engine boundary and a name copied out of audio.cpp is accepted here. All
// thirteen have an upstream name; only "spk" is absent from the --task table in
// docs/usage.md.
const NamedTask kTaskNames[] = {
    {Task::Vad, "vad"},
    {Task::Asr, "asr"},
    {Task::Diarization, "diar"},
    {Task::SourceSeparation, "sep"},
    {Task::AudioGeneration, "gen"},
    {Task::Tts, "tts"},
    {Task::VoiceCloning, "clon"},
    {Task::VoiceConversion, "vc"},
    {Task::SpeechToSpeech, "s2s"},
    {Task::Alignment, "align"},
    {Task::VoiceDesign, "vdes"},
    {Task::SpeakerRecognition, "spk"},
    {Task::Svc, "svc"},
};

// Accepted on input but never emitted. "spkrec" was this backend's own earlier
// name for the kind; upstream only ever knew "spk".
const NamedTask kTaskAliases[] = {
    {Task::SpeakerRecognition, "spkrec"},
};

// First match wins, which is safe because a Capabilities value holds at most one
// entry per task: it mirrors upstream runtime::TaskCapability (model.h), which
// pairs one kind with a modes vector, and no loader's supported_tasks list
// repeats a kind.
bool family_supports(const Capabilities &caps, Task task, Mode mode) {
    for (const auto &capability : caps.tasks) {
        if (capability.task != task) {
            continue;
        }
        return std::find(capability.modes.begin(), capability.modes.end(), mode) !=
               capability.modes.end();
    }
    return false;
}

// Mode preference per RPC. Only AudioTranscriptionStream has a fallback: a
// server-streaming transcription can be satisfied by an offline run that emits
// one delta then the final result. Live transcription cannot, because it is
// bidirectional and must consume audio incrementally.
std::vector<Mode> mode_candidates(Rpc rpc) {
    switch (rpc) {
    case Rpc::TtsStream:
    case Rpc::AudioTranscriptionLive:
        return {Mode::Streaming};
    case Rpc::AudioTranscriptionStream:
        return {Mode::Streaming, Mode::Offline};
    default:
        return {Mode::Offline};
    }
}

std::vector<Task> task_candidates(Rpc rpc, const RequestShape &shape) {
    switch (rpc) {
    case Rpc::Tts:
    case Rpc::TtsStream:
        // A supplied speaker clip is the strongest signal: the caller named the
        // voice they want. Free-form instructions come next. Both fall back to
        // plain Tts so a family without the specialised task still answers.
        if (shape.has_voice_reference) {
            return {Task::VoiceCloning, Task::Tts, Task::VoiceDesign};
        }
        if (shape.has_instructions) {
            return {Task::VoiceDesign, Task::Tts, Task::VoiceCloning};
        }
        return {Task::Tts, Task::VoiceCloning, Task::VoiceDesign};
    case Rpc::AudioTranscription:
    case Rpc::AudioTranscriptionStream:
    case Rpc::AudioTranscriptionLive:
        // Asr first: `prompt` is also whisper-style decoding context, so its
        // presence must not hijack a real ASR family into forced alignment.
        if (shape.has_prompt_text) {
            return {Task::Asr, Task::Alignment};
        }
        return {Task::Asr};
    case Rpc::Vad:
        return {Task::Vad};
    case Rpc::Diarize:
        return {Task::Diarization};
    case Rpc::SoundGeneration:
        return {Task::AudioGeneration};
    case Rpc::AudioTransform:
        // Svc is listed for completeness but is unreachable by auto-routing, by
        // design: the only families advertising it (seed_vc, vevo2) also
        // advertise VoiceConversion, which always wins, and no request signal
        // means "this input is singing". Singing voice conversion therefore
        // requires an explicit task:svc pin.
        return {Task::SourceSeparation, Task::VoiceConversion, Task::Svc,
                Task::SpeechToSpeech};
    }
    return {};
}

std::string join_attempts(const std::vector<Task> &tasks,
                          const std::vector<Mode> &modes) {
    std::string out;
    for (const Task task : tasks) {
        for (const Mode mode : modes) {
            if (!out.empty()) {
                out += ", ";
            }
            out += task_name(task);
            out += "/";
            out += mode_name(mode);
        }
    }
    return out;
}

} // namespace

const char *task_name(Task task) {
    for (const auto &entry : kTaskNames) {
        if (entry.task == task) {
            return entry.name;
        }
    }
    return "unknown";
}

const char *mode_name(Mode mode) {
    return mode == Mode::Streaming ? "streaming" : "offline";
}

const char *rpc_name(Rpc rpc) {
    switch (rpc) {
    case Rpc::Tts:
        return "TTS";
    case Rpc::TtsStream:
        return "TTSStream";
    case Rpc::AudioTranscription:
        return "AudioTranscription";
    case Rpc::AudioTranscriptionStream:
        return "AudioTranscriptionStream";
    case Rpc::AudioTranscriptionLive:
        return "AudioTranscriptionLive";
    case Rpc::Vad:
        return "VAD";
    case Rpc::Diarize:
        return "Diarize";
    case Rpc::SoundGeneration:
        return "SoundGeneration";
    case Rpc::AudioTransform:
        return "AudioTransform";
    }
    return "unknown";
}

bool parse_task_name(const std::string &value, Task &out) {
    for (const auto &entry : kTaskNames) {
        if (value == entry.name) {
            out = entry.task;
            return true;
        }
    }
    for (const auto &entry : kTaskAliases) {
        if (value == entry.name) {
            out = entry.task;
            return true;
        }
    }
    return false;
}

std::string describe_capabilities(const Capabilities &caps) {
    std::string out;
    for (const auto &capability : caps.tasks) {
        for (const Mode mode : capability.modes) {
            if (!out.empty()) {
                out += ", ";
            }
            out += task_name(capability.task);
            out += "/";
            out += mode_name(mode);
        }
    }
    if (out.empty()) {
        out = "nothing";
    }
    return out;
}

Route resolve_route(Rpc rpc, const RequestShape &shape,
                    const Capabilities &caps) {
    Route route;

    std::vector<Task> tasks;
    if (!shape.pinned_task.empty()) {
        Task pinned = Task::Tts;
        if (!parse_task_name(shape.pinned_task, pinned)) {
            route.error = "audio-cpp: unknown task option '" + shape.pinned_task +
                          "'. Known tasks: gen, tts, clon, vc, svc, s2s, asr, "
                          "align, vad, diar, sep, vdes, spk";
            return route;
        }
        // A pinned task is honoured exactly. Silently rerouting would make the
        // option meaningless and hide a misconfigured model.
        tasks = {pinned};
    } else {
        tasks = task_candidates(rpc, shape);
    }

    const std::vector<Mode> modes = mode_candidates(rpc);

    // Task-major: prefer the right task in a fallback mode over the wrong task
    // in the preferred mode.
    for (const Task task : tasks) {
        for (const Mode mode : modes) {
            if (family_supports(caps, task, mode)) {
                route.ok = true;
                route.task = task;
                route.mode = mode;
                return route;
            }
        }
    }

    route.error = std::string("audio-cpp: family '") + caps.family +
                  "' cannot serve the " + rpc_name(rpc) + " RPC (tried " +
                  join_attempts(tasks, modes) + "); it supports: " +
                  describe_capabilities(caps);
    return route;
}

} // namespace audiocpp_backend
