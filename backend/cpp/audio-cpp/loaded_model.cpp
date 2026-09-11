#include "loaded_model.h"

#include "family_gate.h"

#include "engine/framework/assets/tensor_source.h"

#include <algorithm>
#include <cstddef>
#include <cstdint>
#include <filesystem>
#include <utility>

#if defined(__APPLE__)
#include <mach-o/dyld.h>
#include <cstdint>
#include <vector>
#elif defined(__linux__)
#include <unistd.h>
#endif

namespace audiocpp_backend {

// --------------------------------------------------------------------------
// Enum coupling
//
// audiocpp_backend::Task mirrors engine::runtime::VoiceTaskKind positionally so
// capability_routing can stay stdlib-only and testable without an audio.cpp
// checkout. Nothing about that mirroring is enforced by the type system, and a
// drift is silent in the worst possible way: every unit still compiles, every
// test still passes, and the backend runs a different task than the one the
// caller asked for.
//
// Two mechanisms pin it, and both are needed because they catch different edits:
//
//   1. The assertions below pin every enumerator's value on both sides. An
//      insertion or a reorder anywhere before the last member shifts the values
//      after it and fails the build here.
//   2. An enumerator APPENDED after the last one shifts nothing, so no value
//      assertion can see it. What sees it is the switch in from_engine_task,
//      which covers the engine enum with no `default:` label. CMakeLists.txt
//      compiles this file with -Werror=switch so that omission is an error
//      rather than a warning nobody reads.
//
// Neither mechanism catches a pure RENAME of an upstream enumerator, but that
// does not need catching: the switch stops naming an enumerator that exists and
// the build fails on its own.
// --------------------------------------------------------------------------

namespace {

constexpr int kEngine(engine::runtime::VoiceTaskKind kind) {
    return static_cast<int>(kind);
}
constexpr int kMirror(Task task) { return static_cast<int>(task); }

} // namespace

static_assert(kEngine(engine::runtime::VoiceTaskKind::Vad) == 0, "VoiceTaskKind drifted");
static_assert(kEngine(engine::runtime::VoiceTaskKind::Asr) == 1, "VoiceTaskKind drifted");
static_assert(kEngine(engine::runtime::VoiceTaskKind::Diarization) == 2, "VoiceTaskKind drifted");
static_assert(kEngine(engine::runtime::VoiceTaskKind::SourceSeparation) == 3, "VoiceTaskKind drifted");
static_assert(kEngine(engine::runtime::VoiceTaskKind::AudioGeneration) == 4, "VoiceTaskKind drifted");
static_assert(kEngine(engine::runtime::VoiceTaskKind::Tts) == 5, "VoiceTaskKind drifted");
static_assert(kEngine(engine::runtime::VoiceTaskKind::VoiceCloning) == 6, "VoiceTaskKind drifted");
static_assert(kEngine(engine::runtime::VoiceTaskKind::VoiceConversion) == 7, "VoiceTaskKind drifted");
static_assert(kEngine(engine::runtime::VoiceTaskKind::SpeechToSpeech) == 8, "VoiceTaskKind drifted");
static_assert(kEngine(engine::runtime::VoiceTaskKind::Alignment) == 9, "VoiceTaskKind drifted");
static_assert(kEngine(engine::runtime::VoiceTaskKind::VoiceDesign) == 10, "VoiceTaskKind drifted");
static_assert(kEngine(engine::runtime::VoiceTaskKind::SpeakerRecognition) == 11, "VoiceTaskKind drifted");
// The last member. Pinning it pins the member count too, as long as the
// enumerators stay contiguous and unassigned, which upstream's declaration is.
static_assert(kEngine(engine::runtime::VoiceTaskKind::Svc) == 12, "VoiceTaskKind drifted");
static_assert(kEngine(engine::runtime::VoiceTaskKind::Midi) == 13,
              "engine::runtime::VoiceTaskKind gained, lost or reordered a member. "
              "audiocpp_backend::Task mirrors it positionally: update capability_routing.h, "
              "to_engine_task and from_engine_task together, then move this pin.");

static_assert(kMirror(Task::Vad) == 0, "Task drifted from VoiceTaskKind");
static_assert(kMirror(Task::Asr) == 1, "Task drifted from VoiceTaskKind");
static_assert(kMirror(Task::Diarization) == 2, "Task drifted from VoiceTaskKind");
static_assert(kMirror(Task::SourceSeparation) == 3, "Task drifted from VoiceTaskKind");
static_assert(kMirror(Task::AudioGeneration) == 4, "Task drifted from VoiceTaskKind");
static_assert(kMirror(Task::Tts) == 5, "Task drifted from VoiceTaskKind");
static_assert(kMirror(Task::VoiceCloning) == 6, "Task drifted from VoiceTaskKind");
static_assert(kMirror(Task::VoiceConversion) == 7, "Task drifted from VoiceTaskKind");
static_assert(kMirror(Task::SpeechToSpeech) == 8, "Task drifted from VoiceTaskKind");
static_assert(kMirror(Task::Alignment) == 9, "Task drifted from VoiceTaskKind");
static_assert(kMirror(Task::VoiceDesign) == 10, "Task drifted from VoiceTaskKind");
static_assert(kMirror(Task::SpeakerRecognition) == 11, "Task drifted from VoiceTaskKind");
static_assert(kMirror(Task::Svc) == 12, "Task drifted from VoiceTaskKind");
static_assert(kMirror(Task::Midi) == 13, "Task drifted from VoiceTaskKind");

static_assert(static_cast<int>(engine::runtime::RunMode::Offline) == 0, "RunMode drifted");
static_assert(static_cast<int>(engine::runtime::RunMode::Streaming) == 1,
              "engine::runtime::RunMode gained, lost or reordered a member. "
              "audiocpp_backend::Mode mirrors it positionally.");
static_assert(static_cast<int>(Mode::Offline) == 0, "Mode drifted from RunMode");
static_assert(static_cast<int>(Mode::Streaming) == 1, "Mode drifted from RunMode");

namespace {

engine::core::BackendType parse_backend_type(const std::string &value) {
    if (value == "cuda") {
        return engine::core::BackendType::Cuda;
    }
    if (value == "vulkan") {
        return engine::core::BackendType::Vulkan;
    }
    if (value == "metal") {
        return engine::core::BackendType::Metal;
    }
    if (value == "best") {
        return engine::core::BackendType::BestAvailable;
    }
    if (value == "cpu" || value.empty()) {
        return engine::core::BackendType::Cpu;
    }
    throw ConfigError("audio-cpp: unknown backend option '" + value +
                      "'. Known backends: cpu, cuda, vulkan, metal, best");
}

std::filesystem::path executable_directory() {
#if defined(__APPLE__)
    std::uint32_t size = 0;
    _NSGetExecutablePath(nullptr, &size);
    std::vector<char> buffer(size + 1, '\0');
    if (_NSGetExecutablePath(buffer.data(), &size) != 0) {
        return std::filesystem::current_path();
    }
    return std::filesystem::path(buffer.data()).parent_path();
#elif defined(__linux__)
    std::error_code ec;
    const auto self = std::filesystem::read_symlink("/proc/self/exe", ec);
    if (ec) {
        return std::filesystem::current_path();
    }
    return self.parent_path();
#else
    return std::filesystem::current_path();
#endif
}

// Runs the load gate. Throws ConfigError rather than returning a decision,
// because the only caller is a delegating constructor whose member initializer
// list has nowhere to put a failure.
std::string require_family(const std::string &resolved_path,
                           const ModelOptions &options) {
    const std::filesystem::path path(resolved_path);
    std::error_code ec;
    if (!std::filesystem::exists(path, ec)) {
        throw ConfigError("audio-cpp: model path does not exist: " + resolved_path);
    }

    const bool is_gguf = path_looks_like_gguf(resolved_path);
    // Only a GGUF is asked for embedded metadata. A directory has no single
    // file to read it from, and probing one would make the gate's refusal
    // depend on which file happened to be inside.
    const std::string embedded =
        is_gguf ? read_gguf_family(resolved_path) : std::string();

    const FamilyDecision decision =
        decide_family(is_gguf, embedded, options.family);
    if (!decision.ok) {
        throw ConfigError(decision.error);
    }
    return decision.family;
}

// Refuses a GGUF whose weights are stored in a dtype the family cannot survive.
//
// The POLICY lives in family_gate's weight_dtype_is_supported, which is
// stdlib-only and therefore testable; this is only the part that needs a file
// and an engine to read one. See the table there for why an entry exists and
// what has to be run before deleting it.
//
// Only GGUF paths are inspected. A directory of safetensors carries its dtypes
// per file and has not been tested against this failure, so it is passed
// through rather than guessed at.
void require_supported_weight_dtypes(const std::string &family,
                                     const std::string &resolved_path) {
    // Asked as "is there an entry", not as "is the description non-empty": an
    // entry with an empty allow list describes a family that can run nothing,
    // and reading the description would skip the check on exactly that entry
    // while weight_dtype_is_supported refused every dtype. No such entry exists
    // today; the two questions are different ones and only one of them is this
    // guard's.
    if (!family_has_weight_dtype_allow_list(family) ||
        !path_looks_like_gguf(resolved_path)) {
        return;
    }

    std::string offending_dtype;
    std::string offending_tensor;
    try {
        const auto source =
            engine::assets::open_tensor_source(std::filesystem::path(resolved_path));
        if (source == nullptr) {
            return;
        }
        for (const auto &tensor : source->tensors()) {
            if (!weight_dtype_is_supported(family, tensor.dtype)) {
                offending_dtype = tensor.dtype;
                offending_tensor = tensor.name;
                break;
            }
        }
    } catch (const std::exception &) {
        // Unreadable as a tensor source. Not this guard's problem to report:
        // the registry load below produces a message naming the real fault, and
        // refusing here would turn every unusual packaging into this error.
        return;
    }

    if (offending_dtype.empty()) {
        return;
    }
    throw ConfigError(
        "audio-cpp: family '" + family + "' cannot run weights stored as '" +
        offending_dtype + "' (tensor '" + offending_tensor + "' in " +
        resolved_path +
        "); it aborts the backend process on the first request rather than "
        "failing the request. Use the 'orig' GGUF package, whose weights are " +
        supported_weight_dtypes(family) + ".");
}

} // namespace

engine::runtime::VoiceTaskKind to_engine_task(Task task) {
    using K = engine::runtime::VoiceTaskKind;
    // An explicit switch, never a cast: a cast would keep compiling through
    // exactly the drift the assertions above exist to catch.
    switch (task) {
    case Task::Vad:                return K::Vad;
    case Task::Asr:                return K::Asr;
    case Task::Diarization:        return K::Diarization;
    case Task::SourceSeparation:   return K::SourceSeparation;
    case Task::AudioGeneration:    return K::AudioGeneration;
    case Task::Tts:                return K::Tts;
    case Task::VoiceCloning:       return K::VoiceCloning;
    case Task::VoiceConversion:    return K::VoiceConversion;
    case Task::SpeechToSpeech:     return K::SpeechToSpeech;
    case Task::Alignment:          return K::Alignment;
    case Task::VoiceDesign:        return K::VoiceDesign;
    case Task::SpeakerRecognition: return K::SpeakerRecognition;
    case Task::Svc:                return K::Svc;
    case Task::Midi:               return K::Midi;
    }
    // Unreachable for any valid enumerator. No `default:` label, so -Wswitch
    // still reports a member this switch stops covering.
    return K::Vad;
}

Task from_engine_task(engine::runtime::VoiceTaskKind kind) {
    using K = engine::runtime::VoiceTaskKind;
    switch (kind) {
    case K::Vad:                return Task::Vad;
    case K::Asr:                return Task::Asr;
    case K::Diarization:        return Task::Diarization;
    case K::SourceSeparation:   return Task::SourceSeparation;
    case K::AudioGeneration:    return Task::AudioGeneration;
    case K::Tts:                return Task::Tts;
    case K::VoiceCloning:       return Task::VoiceCloning;
    case K::VoiceConversion:    return Task::VoiceConversion;
    case K::SpeechToSpeech:     return Task::SpeechToSpeech;
    case K::Alignment:          return Task::Alignment;
    case K::VoiceDesign:        return Task::VoiceDesign;
    case K::SpeakerRecognition: return Task::SpeakerRecognition;
    case K::Svc:                return Task::Svc;
    case K::Midi:               return Task::Midi;
    }
    return Task::Vad;
}

engine::runtime::RunMode to_engine_mode(Mode mode) {
    using M = engine::runtime::RunMode;
    switch (mode) {
    case Mode::Offline:   return M::Offline;
    case Mode::Streaming: return M::Streaming;
    }
    return M::Offline;
}

Mode from_engine_mode(engine::runtime::RunMode mode) {
    using M = engine::runtime::RunMode;
    switch (mode) {
    case M::Offline:   return Mode::Offline;
    case M::Streaming: return Mode::Streaming;
    }
    return Mode::Offline;
}

Capabilities to_capabilities(const std::string &family,
                             const engine::runtime::CapabilitySet &set) {
    Capabilities caps;
    caps.family = family;
    caps.tasks.reserve(set.supported_tasks.size());
    for (const auto &supported : set.supported_tasks) {
        TaskCapability capability;
        capability.task = from_engine_task(supported.task);
        capability.modes.reserve(supported.modes.size());
        for (const auto mode : supported.modes) {
            capability.modes.push_back(from_engine_mode(mode));
        }
        caps.tasks.push_back(std::move(capability));
    }
    return caps;
}

std::string read_gguf_family(const std::string &path) {
    try {
        const auto spec =
            engine::assets::read_gguf_embedded_model_spec(std::filesystem::path(path));
        if (spec.has_value()) {
            return spec->family;
        }
    } catch (...) {
        // A file that is not a readable GGUF simply has no family. The load
        // gate turns that into a clear refusal; a throw here would surface as
        // an opaque internal error during backend probing.
    }
    return {};
}

std::string resolve_model_path(const std::string &model_path_dir,
                               const std::string &model_file,
                               const std::string &model_name) {
    const std::string candidate = !model_file.empty() ? model_file : model_name;

    // The bundled: form is looked for in BOTH fields, and in ModelOptions.Model
    // FIRST, because that is the only field it survives in. LocalAI fills
    // ModelFile by joining ModelPath onto the configured model string
    // (pkg/model/loader.go, LoadModelWithFile), so a model YAML saying
    // `model: bundled:silero_vad` arrives here as ModelFile
    // "/models/bundled:silero_vad" and Model "bundled:silero_vad". Testing
    // `candidate` alone therefore made the zero-download VAD path reachable only
    // from a hand-written LoadModel call that left ModelFile empty, and every
    // model YAML using it failed with "model path does not exist".
    const std::string bundled_prefix = "bundled:";
    for (const std::string *field : {&model_name, &model_file}) {
        if (field->rfind(bundled_prefix, 0) == 0) {
            const std::string name = field->substr(bundled_prefix.size());
            return (executable_directory() / "assets" / name).string();
        }
    }

    std::filesystem::path path(candidate);
    if (path.is_absolute() || model_path_dir.empty()) {
        return path.string();
    }
    return (std::filesystem::path(model_path_dir) / path).string();
}

LoadedModel::LoadedModel(const std::string &resolved_path,
                         const ModelOptions &options,
                         std::string model_identity)
    : LoadedModel(resolved_path, options, require_family(resolved_path, options),
                  std::move(model_identity)) {}

LoadedModel::LoadedModel(const std::string &resolved_path,
                         const ModelOptions &options, std::string family,
                         std::string model_identity)
    : lane_(family), registry_(engine::runtime::make_default_registry()),
      identity_(std::move(model_identity)) {
    if (!registry_.supports_family(family)) {
        throw ConfigError("audio-cpp: unknown audio.cpp family '" + family + "'");
    }

    // Before the load, for the same reason parse_backend_type runs before it:
    // a refusal a metadata read can produce should not cost a full model load.
    // More importantly it must precede the FIRST REQUEST, since that is where
    // an unsupported dtype aborts the process rather than failing.
    require_supported_weight_dtypes(family, resolved_path);

    // Session options are built BEFORE the load, because parse_backend_type
    // rejects an unknown backend name. Validating after the load would make
    // `backend:cudaa` cost a full model load, on a fault a string comparison
    // could have caught.
    session_options_.backend.type = parse_backend_type(options.backend);
    session_options_.backend.device = options.device;
    if (options.threads > 0) {
        session_options_.backend.threads = options.threads;
    }
    for (const auto &entry : options.session_options) {
        session_options_.options[entry.first] = entry.second;
    }

    pinned_task_ = options.task;
    wait_budget_ceiling_ms_ = options.busy_timeout_ms;
    live_idle_timeout_ms_ = options.live_idle_timeout_ms;

    engine::runtime::ModelLoadRequest request;
    request.model_path = std::filesystem::path(resolved_path);
    request.family_hint = family;
    if (!options.model_spec_override.empty()) {
        request.model_spec_override =
            std::filesystem::path(options.model_spec_override);
    }
    for (const auto &entry : options.load_options) {
        request.options[entry.first] = entry.second;
    }

    try {
        model_ = registry_.load(request);
    } catch (const std::exception &err) {
        throw ConfigError("audio-cpp: failed to load family '" + family +
                          "' from " + resolved_path + ": " + err.what());
    }
    if (model_ == nullptr) {
        throw ConfigError("audio-cpp: the registry returned no model for " +
                          resolved_path);
    }

    const auto &metadata = model_->metadata();
    const auto &engine_caps = model_->capabilities();
    variant_ = metadata.variant;
    description_ = metadata.description;
    languages_ = engine_caps.languages;
    supports_timestamps_ = engine_caps.supports_timestamps;
    capabilities_ = to_capabilities(family, engine_caps);
}

Route LoadedModel::check_can_serve(Rpc rpc, const RequestShape &shape) const {
    const Route route = resolve_route(rpc, shape, capabilities_);
    if (!route.ok) {
        throw CapabilityError(route.error);
    }
    // Returned so a handler can act on the task before running it. It is the
    // same route session_for will resolve, since both read the immutable
    // capabilities_ from the same shape.
    return route;
}

LoadedModel::Session LoadedModel::session_for(Rpc rpc, const RequestShape &shape,
                                              LaneEntry &lane) {
    // Proof of holding only. Nothing here reads it, and nothing should: its
    // whole job is to make a caller that has not taken the lane fail to
    // compile. Non-const so it cannot bind to an inline acquire(), whose
    // temporary would be released at the end of this call.
    (void)lane;
    const Route route = resolve_route(rpc, shape, capabilities_);
    if (!route.ok) {
        throw CapabilityError(route.error);
    }

    const SessionKey key{static_cast<int>(route.task), static_cast<int>(route.mode)};
    auto found = sessions_.find(key);
    const bool cache_hit = found != sessions_.end();
    if (!cache_hit) {
        engine::runtime::TaskSpec spec;
        spec.task = to_engine_task(route.task);
        spec.mode = to_engine_mode(route.mode);
        std::unique_ptr<engine::runtime::IVoiceTaskSession> created;
        try {
            created = model_->create_task_session(spec, session_options_);
        } catch (const std::exception &err) {
            // NOT a CapabilityError. The family said it supports this pair, and
            // a throw from here is overwhelmingly an environment fault: a ggml
            // backend .so that package.sh did not ship, an out of memory, a CUDA
            // device that is not there. UNIMPLEMENTED would tell LocalAI and
            // every client "this model cannot do this, never retry", and send an
            // operator hunting a capability bug instead of a packaging one. A
            // plain runtime_error maps to INTERNAL, which is what a fixable
            // deployment fault should look like.
            throw std::runtime_error(
                std::string("audio-cpp: family '") + capabilities_.family +
                "' advertises " + task_name(route.task) + "/" +
                mode_name(route.mode) + " but refused to create the session: " +
                err.what());
        }
        if (created == nullptr) {
            // A null return with no throw is the family declining, which is a
            // genuine capability answer and stays UNIMPLEMENTED.
            throw CapabilityError(std::string("audio-cpp: family '") +
                                  capabilities_.family +
                                  "' returned no session for " +
                                  task_name(route.task) + "/" +
                                  mode_name(route.mode));
        }
        found = sessions_.emplace(key, std::move(created)).first;
    }

    Session session;
    session.task = route.task;
    session.mode = route.mode;
    engine::runtime::IVoiceTaskSession *raw = found->second.get();
    if (route.mode == Mode::Streaming) {
        session.streaming =
            dynamic_cast<engine::runtime::IStreamingVoiceTaskSession *>(raw);
        if (session.streaming == nullptr) {
            throw CapabilityError(std::string("audio-cpp: family '") +
                                  capabilities_.family +
                                  "' advertises " + task_name(route.task) +
                                  "/streaming but its session is not streaming");
        }
        // Deliberately NOT reset here, though a cached streaming session does
        // carry state across chunks. reset() is not callable at this point:
        // silero_vad's implementation throws "session prepare() must be called
        // before Silero VAD reset()", so resetting on a cache hit would turn an
        // ordinary second fetch into a hard error, which is worse than the leak
        // it would prevent.
        //
        // The state is instead cleared by the sequence every streaming caller
        // owes anyway. IStreamingVoiceTaskSession::start_stream's base
        // implementation IS a call to reset(), so a caller that runs
        // prepare(...) then start_stream(...) at the top of each stream gets a
        // clean session for free. See the STATE CONTRACT in loaded_model.h.
    } else {
        session.offline =
            dynamic_cast<engine::runtime::IOfflineVoiceTaskSession *>(raw);
        if (session.offline == nullptr) {
            throw CapabilityError(std::string("audio-cpp: family '") +
                                  capabilities_.family +
                                  "' advertises " + task_name(route.task) +
                                  "/offline but its session is not offline");
        }
    }
    return session;
}

LaneEntry LoadedModel::acquire(int requested_timeout_ms) {
    // Constructed straight into the return value. C++17 requires that, which is
    // what lets an immovable type be returned at all; a named local here would
    // not compile.
    return LaneEntry(lane_,
                     resolve_wait_budget_ms(wait_budget_ceiling_ms_,
                                            requested_timeout_ms));
}

std::unique_ptr<LaneEntry> LoadedModel::acquire_owned(int requested_timeout_ms) {
    return std::make_unique<LaneEntry>(
        lane_,
        resolve_wait_budget_ms(wait_budget_ceiling_ms_, requested_timeout_ms));
}

engine::runtime::TaskResult run_offline(const LoadedModel::Session &session,
                                        const engine::runtime::TaskRequest &request,
                                        LaneEntry &lane) {
    // Proof of holding only, as in session_for.
    (void)lane;
    if (session.offline == nullptr) {
        throw CapabilityError("audio-cpp: no offline session for this request");
    }
    session.offline->prepare(engine::runtime::build_preparation_request(request));
    return session.offline->run(request);
}

namespace {

engine::runtime::IStreamingVoiceTaskSession &
require_streaming(const LoadedModel::Session &session) {
    if (session.streaming == nullptr) {
        throw CapabilityError("audio-cpp: no streaming session for this request");
    }
    return *session.streaming;
}

// Clears the stream event sink on every exit from the driver, including the
// exception path. The session is CACHED and outlives the call that installed
// the sink, so a std::function left behind holding references into that call's
// frame is called with dangling captures by whoever streams next.
class ScopedStreamSink {
public:
    ScopedStreamSink(engine::runtime::IStreamingVoiceTaskSession &session,
                     engine::runtime::StreamEventCallback sink)
        : session_(session) {
        session_.set_stream_event_sink(std::move(sink));
    }
    ~ScopedStreamSink() { session_.set_stream_event_sink(nullptr); }

    ScopedStreamSink(const ScopedStreamSink &) = delete;
    ScopedStreamSink &operator=(const ScopedStreamSink &) = delete;

private:
    engine::runtime::IStreamingVoiceTaskSession &session_;
};

// Frames per chunk to feed a streaming session, from its own policy.
//
// FRAMES, not floats. preferred_audio_chunk_samples is a per-channel count
// everywhere upstream sets it (nemotron_asr uses its frontend sample rate,
// i.e. one second), and vibevoice_asr refuses a chunk whose float count is not
// divisible by its channel count, so slicing on floats would both mis-size the
// window and hand a family a half frame.
std::int64_t chunk_frames_for(const engine::runtime::StreamingPolicy &policy,
                              int sample_rate) {
    if (policy.preferred_audio_chunk_samples > 0) {
        return policy.preferred_audio_chunk_samples;
    }
    // higgs_audio_stt states its window in seconds (4.0) and leaves the sample
    // count at zero, so this branch is real rather than defensive.
    if (policy.preferred_audio_chunk_seconds > 0.0 && sample_rate > 0) {
        const auto frames = static_cast<std::int64_t>(
            policy.preferred_audio_chunk_seconds * static_cast<double>(sample_rate));
        if (frames > 0) {
            return frames;
        }
    }
    // The interface's own default, from IStreamingVoiceTaskSession::streaming_policy.
    return 512;
}

} // namespace

void begin_stream(const LoadedModel::Session &session,
                  const engine::runtime::TaskRequest &request, LaneEntry &lane) {
    // Proof of holding only, as in session_for.
    (void)lane;
    auto &streaming = require_streaming(session);
    // Order is load-bearing: start_stream's reset() is illegal before prepare().
    streaming.prepare(engine::runtime::build_preparation_request(request));
    streaming.start_stream(request);
}

engine::runtime::TaskResult run_streaming_pull(
    const LoadedModel::Session &session,
    const engine::runtime::TaskRequest &request,
    const std::function<void(const engine::runtime::StreamEvent &)> &on_event,
    LaneEntry &lane) {
    auto &streaming = require_streaming(session);
    begin_stream(session, request, lane);
    while (const auto event = streaming.next_stream_event()) {
        if (on_event) {
            on_event(*event);
        }
        // No pinned family sets is_final on a pulled event, so this is not what
        // ends the loop today; the nullopt above is. Honoured anyway, because a
        // family that does set it is saying the stream is over and pulling once
        // more would be asking a finished session for another chunk.
        if (event->is_final) {
            break;
        }
    }
    return streaming.finish_stream();
}

engine::runtime::TaskResult run_streaming_audio(
    const LoadedModel::Session &session,
    const engine::runtime::TaskRequest &request,
    const engine::runtime::AudioBuffer &audio,
    const std::function<void(const engine::runtime::StreamEvent &)> &on_event,
    LaneEntry &lane) {
    auto &streaming = require_streaming(session);

    const int channels = audio.channels > 0 ? audio.channels : 1;
    // REFUSED, not rounded away, and checked before anything is touched so a
    // refusal leaves no half-started stream on a cached session.
    //
    // An interleaved buffer whose float count is not a whole number of frames
    // is a truncated input, and the integer division below would silently drop
    // the tail floats: they would never be fed, never reach the transcript, and
    // nothing would say so. Upstream refuses the same condition rather than
    // tolerating it, in two places: vibevoice_asr's audio_frame_count throws
    // "VibeVoice-ASR audio samples must be divisible by channel count"
    // (session.cpp:70-76), and its process_audio_chunk throws the same with
    // "streamed" in the text about the chunks this driver hands it
    // (session.cpp:742-747).
    //
    // ConfigError, i.e. INVALID_ARGUMENT, because the buffer came from the
    // caller's file. read_audio_file's positive-rate path always answers mono
    // and so cannot reach this, but its native-rate path passes the reader's
    // sample count through unchanged, and a driver does not get to assume which
    // path its caller took.
    if (audio.samples.size() % static_cast<std::size_t>(channels) != 0) {
        throw ConfigError(
            "audio-cpp: streaming input is not a whole number of frames: " +
            std::to_string(audio.samples.size()) + " samples across " +
            std::to_string(channels) + " channels");
    }

    // Installed BEFORE the stream begins, so a family that reports during
    // start_stream is not silently dropped, and destroyed after finish_stream,
    // because nemotron_asr emits every one of its partials from inside
    // finalize().
    ScopedStreamSink sink(streaming,
                          [&on_event](const engine::runtime::StreamEvent &event) {
                              if (on_event) {
                                  on_event(event);
                              }
                          });

    begin_stream(session, request, lane);

    const auto total_frames =
        static_cast<std::int64_t>(audio.samples.size() / static_cast<size_t>(channels));
    const std::int64_t chunk_frames =
        chunk_frames_for(streaming.streaming_policy(), audio.sample_rate);

    for (std::int64_t offset = 0; offset < total_frames; offset += chunk_frames) {
        const std::int64_t end = std::min(offset + chunk_frames, total_frames);
        engine::runtime::AudioChunk chunk;
        chunk.sample_rate = audio.sample_rate;
        chunk.channels = channels;
        // A FRAME index, which is what every span in a returned event is
        // expressed in. vibevoice_asr adds the chunk's own frame count to it to
        // offset the spans it reports, so a float index here would place every
        // span of a stereo stream at twice its real time.
        chunk.start_sample = offset;
        chunk.samples.assign(
            audio.samples.begin() + static_cast<std::ptrdiff_t>(offset * channels),
            audio.samples.begin() + static_cast<std::ptrdiff_t>(end * channels));
        const auto event = streaming.process_audio_chunk(chunk);
        if (on_event) {
            on_event(event);
        }
    }
    return streaming.finish_stream();
}

engine::runtime::TaskResult run_streaming_live(
    const LoadedModel::Session &session,
    const engine::runtime::TaskRequest &request,
    const std::function<bool(std::vector<float> &)> &next_frames,
    const std::function<void(const engine::runtime::StreamEvent &)> &on_event,
    LaneEntry &lane) {
    auto &streaming = require_streaming(session);

    // The contract is the only thing that says what rate and layout the frames
    // about to arrive are in, and prepare() needs it: see the header.
    if (!request.audio_input.has_value()) {
        throw ConfigError(
            "audio-cpp: a live streaming request carries no audio contract");
    }
    const int sample_rate = request.audio_input->sample_rate;
    const int channels =
        request.audio_input->channels > 0 ? request.audio_input->channels : 1;

    // Installed BEFORE the stream begins and cleared on every exit, including
    // the exception path, for the reasons spelled out in run_streaming_audio.
    ScopedStreamSink sink(streaming,
                          [&on_event](const engine::runtime::StreamEvent &event) {
                              if (on_event) {
                                  on_event(event);
                              }
                          });

    begin_stream(session, request, lane);

    const std::int64_t chunk_frames =
        chunk_frames_for(streaming.streaming_policy(), sample_rate);
    // chunk_frames_for never returns a non-positive count, so this is never
    // zero and the accumulation loop below always terminates.
    const std::size_t chunk_floats = static_cast<std::size_t>(chunk_frames) *
                                     static_cast<std::size_t>(channels);

    std::int64_t fed_frames = 0;
    const auto feed = [&](std::vector<float> samples) {
        engine::runtime::AudioChunk chunk;
        chunk.sample_rate = sample_rate;
        chunk.channels = channels;
        // A FRAME index, counted across the whole stream: vibevoice_asr offsets
        // every span it reports by it, so restarting it per chunk would put
        // every word at the top of the recording.
        chunk.start_sample = fed_frames;
        chunk.samples = std::move(samples);
        fed_frames +=
            static_cast<std::int64_t>(chunk.samples.size()) / channels;
        const auto event = streaming.process_audio_chunk(chunk);
        if (on_event) {
            on_event(event);
        }
    };

    std::vector<float> pending;
    std::vector<float> incoming;
    while (true) {
        incoming.clear();
        if (!next_frames(incoming)) {
            break;
        }
        pending.insert(pending.end(), incoming.begin(), incoming.end());
        while (pending.size() >= chunk_floats) {
            std::vector<float> window(pending.begin(),
                                      pending.begin() +
                                          static_cast<std::ptrdiff_t>(chunk_floats));
            pending.erase(pending.begin(),
                          pending.begin() +
                              static_cast<std::ptrdiff_t>(chunk_floats));
            feed(std::move(window));
        }
    }

    if (!pending.empty()) {
        // The tail is whatever did not fill a window. Refused rather than
        // truncated when it is not a whole number of frames, exactly as in
        // run_streaming_audio: the division above would drop the stray floats
        // from the transcript with no diagnostic. Unreachable for a mono live
        // stream, which is every live stream today.
        if (pending.size() % static_cast<std::size_t>(channels) != 0) {
            throw ConfigError(
                "audio-cpp: live stream ended mid-frame: " +
                std::to_string(pending.size()) + " trailing samples across " +
                std::to_string(channels) + " channels");
        }
        feed(std::move(pending));
    }

    if (fed_frames == 0) {
        // Nothing was spoken. See the header: finalizing an empty stream is not
        // legal for every family, and an empty transcript is the truthful
        // answer rather than an engine-internal INTERNAL.
        return engine::runtime::TaskResult{};
    }
    return streaming.finish_stream();
}

} // namespace audiocpp_backend
