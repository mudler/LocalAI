#include "loaded_model.h"

#include "family_gate.h"

#include "engine/framework/assets/tensor_source.h"

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
static_assert(kEngine(engine::runtime::VoiceTaskKind::Svc) == 12,
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

    const std::string bundled_prefix = "bundled:";
    if (candidate.rfind(bundled_prefix, 0) == 0) {
        const std::string name = candidate.substr(bundled_prefix.size());
        return (executable_directory() / "assets" / name).string();
    }

    std::filesystem::path path(candidate);
    if (path.is_absolute() || model_path_dir.empty()) {
        return path.string();
    }
    return (std::filesystem::path(model_path_dir) / path).string();
}

LoadedModel::LoadedModel(const std::string &resolved_path,
                         const ModelOptions &options)
    : LoadedModel(resolved_path, options, require_family(resolved_path, options)) {}

LoadedModel::LoadedModel(const std::string &resolved_path,
                         const ModelOptions &options, std::string family)
    : lane_(family), registry_(engine::runtime::make_default_registry()) {
    if (!registry_.supports_family(family)) {
        throw ConfigError("audio-cpp: unknown audio.cpp family '" + family + "'");
    }

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

    session_options_.backend.type = parse_backend_type(options.backend);
    session_options_.backend.device = options.device;
    if (options.threads > 0) {
        session_options_.backend.threads = options.threads;
    }
    for (const auto &entry : options.session_options) {
        session_options_.options[entry.first] = entry.second;
    }

    wait_budget_ceiling_ms_ = options.busy_timeout_ms;
}

LoadedModel::Session LoadedModel::session_for(Rpc rpc, const RequestShape &shape) {
    const Route route = resolve_route(rpc, shape, capabilities_);
    if (!route.ok) {
        throw CapabilityError(route.error);
    }

    const SessionKey key{static_cast<int>(route.task), static_cast<int>(route.mode)};
    auto found = sessions_.find(key);
    if (found == sessions_.end()) {
        engine::runtime::TaskSpec spec;
        spec.task = to_engine_task(route.task);
        spec.mode = to_engine_mode(route.mode);
        std::unique_ptr<engine::runtime::IVoiceTaskSession> created;
        try {
            created = model_->create_task_session(spec, session_options_);
        } catch (const std::exception &err) {
            throw CapabilityError(
                std::string("audio-cpp: family '") + capabilities_.family +
                "' advertises " + task_name(route.task) + "/" +
                mode_name(route.mode) + " but refused to create the session: " +
                err.what());
        }
        if (created == nullptr) {
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

} // namespace audiocpp_backend
