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
    {Task::Midi, "midi"},
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

// The reasons behind unsupported_surfaces(), spelled once because AudioEncode
// and AudioDecode share theirs. Each is phrased in terms of what upstream does
// and does not have, so a reader can check it against the pinned checkout
// rather than take it on trust. Every one of them was checked against
// audio.cpp e800d435d130dc776baf6f3e6129bb62b1495c89, and one of the four
// claims this backend was planned against did not survive that check: see
// kTransformStreamReason.
//
// A latent upstream inconsistency worth knowing about but deliberately NOT put
// on the wire, because it would mislead: model_spec/schema.cpp's task-string
// whitelist does accept "codec" (and "dialogue"), while
// model_spec/metadata.cpp's parse_task_kind has no branch for either and
// throws "unknown model spec task". So a spec declaring "codec" validates and
// then fails to load. That is a hole in upstream's own validation, not a codec
// task this backend could reach.
const char *const kCodecReason =
    "audio.cpp's VoiceTaskKind has no codec entry, so no family can be asked to "
    "turn PCM into codec frames or back; miocodec carries a Codec tag in "
    "upstream's README but its loader advertises only vc and s2s";
// NOT "streaming exists for tts and asr only", which is what this backend was
// planned to say and is false: silero_vad advertises vad with RunMode::Streaming
// (src/models/silero_vad/session.cpp). The claim that actually holds is the
// narrower one below, about the four tasks AudioTransform routes to.
// The trailing clause is not padding. The premise is an absence, and an absence
// does not on its own make the RPC impossible: an offline sep family could be
// buffered and emitted as a stream, which is what several LocalAI backends do.
// Stopping at "nothing advertises streaming" would imply an impossibility the
// evidence does not support. What is true, and what the caller needs, is that
// this backend declines to dress an offline call up as a streaming one.
const char *const kTransformStreamReason =
    "no audio.cpp family advertises streaming for any task AudioTransform routes "
    "to (sep, vc, svc, s2s); upstream advertises RunMode::Streaming for tts, asr "
    "and vad only, and no conversion or separation family even implements its "
    "IStreamingVoiceTaskSession interface, so a streaming transform here would be "
    "a buffered offline call in disguise, which this backend does not pretend to "
    "offer";
// "clip-to-clip processing against a target voice", NOT "voice conversion". The
// latter is true of miocodec and FALSE of vevo2, whose s2s route is `editing`
// and only `editing`: src/models/vevo2/session.cpp's default_route_for_task maps
// SpeechToSpeech to Editing and route_matches_task accepts nothing else, and
// docs/models/vevo2.md defines that route as "Edit source speech into new target
// text while using the target voice", requiring --target-text. It rewrites what
// was said. vevo2's actual voice conversion is its separate vc task, which is
// why upstream's README tags the family "TTS, Music, VC, Edit". The conclusion
// is unaffected: neither family converses.
const char *const kAudioToAudioReason =
    "LocalAI's contract here is OpenAI-Realtime shaped, an audio conversation "
    "emitting audio, transcript and tool-call deltas from a system prompt and a "
    "tool list; audio.cpp's s2s is offline clip-to-clip processing against a "
    "target voice, declared only by miocodec (voice conversion) and vevo2 "
    "(speech editing), with no conversation, system prompt or tool loop";
const char *const kVoiceEmbedReason =
    "no audio.cpp family advertises the spk (SpeakerRecognition) task, so "
    "nothing in the engine can produce a speaker embedding; the task kind "
    "itself exists upstream, and TitaNet and ECAPA-TDNN exist as internal "
    "conditioning encoders, but neither is registered as a loadable family";

// The tasks an RPC is ever willing to route to, independent of request shape.
//
// DERIVED from task_candidates rather than restated, so a task added to an
// RPC's candidate list cannot become inadmissible as a pin by omission. Setting
// every shape flag yields each RPC's widest list: the per-flag branches only
// reorder the same three tasks for Tts, and only ADD Alignment for
// transcription, so the union is what comes back.
std::vector<Task> admissible_tasks(Rpc rpc) {
    RequestShape widest;
    widest.has_voice_reference = true;
    widest.has_instructions = true;
    widest.has_prompt_text = true;
    return task_candidates(rpc, widest);
}

std::string join_task_names(const std::vector<Task> &tasks) {
    std::string out;
    for (const Task task : tasks) {
        if (!out.empty()) {
            out += ", ";
        }
        out += task_name(task);
    }
    if (out.empty()) {
        out = "nothing";
    }
    return out;
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

const std::vector<UnsupportedSurface> &unsupported_surfaces() {
    // Ordered as UnsupportedRpc declares them. unsupported_surface() names each
    // index in a switch rather than casting the enum, so the order is checked at
    // compile time rather than trusted.
    static const std::vector<UnsupportedSurface> kSurfaces = {
        {"AudioEncode", kCodecReason},
        {"AudioDecode", kCodecReason},
        {"AudioTransformStream", kTransformStreamReason},
        {"AudioToAudioStream", kAudioToAudioReason},
        {"VoiceEmbed", kVoiceEmbedReason},
    };
    return kSurfaces;
}

// A switch with NO default label, deliberately. -Wswitch is on under -Wall, so a
// sixth UnsupportedRpc added without a case here is a BUILD diagnostic, which is
// the only place this class of mistake can be caught for free: a positional
// static_cast<size_t>(rpc) would compile fine and read past the end of the table
// at run time, on the one code path whose entire job is to be diagnosable. The
// table stays a table because the tests iterate it.
//
// The trailing return is unreachable through the enum and exists only for a
// caller that hands over a value outside it, which is already undefined
// behaviour by the time it arrives.
const UnsupportedSurface &unsupported_surface(UnsupportedRpc rpc) {
    const std::vector<UnsupportedSurface> &surfaces = unsupported_surfaces();
    switch (rpc) {
    case UnsupportedRpc::AudioEncode:
        return surfaces[0];
    case UnsupportedRpc::AudioDecode:
        return surfaces[1];
    case UnsupportedRpc::AudioTransformStream:
        return surfaces[2];
    case UnsupportedRpc::AudioToAudioStream:
        return surfaces[3];
    case UnsupportedRpc::VoiceEmbed:
        return surfaces[4];
    }
    return surfaces[0];
}

std::string unsupported_surface_message(const Capabilities &caps, const char *rpc,
                                        const char *reason) {
    return std::string("audio-cpp: the ") + rpc +
           " RPC is not available through this backend because " + reason +
           ". Loaded family '" + caps.family +
           "' supports: " + describe_capabilities(caps);
}

std::string unsupported_surface_message(const char *rpc, const char *reason) {
    return std::string("audio-cpp: the ") + rpc +
           " RPC is not available through this backend because " + reason +
           ". No model is loaded, so there is no family to list; loading one "
           "would not change this answer";
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
        // A pin is honoured exactly, but ONLY on an RPC that could have routed
        // to it anyway. It used to replace the candidate list wholesale for
        // every RPC, and because the model's `task:` option is copied into the
        // shape by all nine handlers, one pin bled across all nine surfaces and
        // produced wrong 200s rather than errors: nemotron with task:asr made
        // Vad return 200 with zero segments after a full ASR decode, so 14
        // seconds of speech was reported as silence, and silero_vad with
        // task:vad made AudioTranscription return 200 with empty text and four
        // segments whose spans were VAD segments, which the srt/vtt/lrc writers
        // then rendered as a well formed subtitle file of four timed EMPTY
        // cues. Refusing is what the docs already promise: "if the family
        // cannot serve it, the request is refused rather than rerouted".
        //
        // Every legitimate pin survives, because a pin only ever names the task
        // its own RPC already routes to: svc is in AudioTransform's candidates,
        // tts/clon/vdes in TTS's, asr in transcription's, vad and diar in
        // theirs.
        const std::vector<Task> admissible = admissible_tasks(rpc);
        if (std::find(admissible.begin(), admissible.end(), pinned) ==
            admissible.end()) {
            route.error = std::string("audio-cpp: this model pins task '") +
                          task_name(pinned) + "', which the " + rpc_name(rpc) +
                          " RPC never routes to (it routes to " +
                          join_task_names(admissible) +
                          "). Remove the task option to reach this RPC, or call "
                          "the RPC the pinned task serves";
            return route;
        }
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
