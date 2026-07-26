#pragma once

// Owns one audio.cpp model for the life of the process, plus a lazily created
// session per (task, mode) so the same model answers both TTS and TTSStream.
// This is the only unit that converts between the stdlib-only mirror types and
// engine::runtime types.

#include "capability_routing.h"
#include "inference_lane.h"
#include "model_options.h"

#include "engine/framework/runtime/model.h"
#include "engine/framework/runtime/registry.h"
#include "engine/framework/runtime/session.h"

#include <map>
#include <memory>
#include <stdexcept>
#include <string>
#include <utility>
#include <vector>

namespace audiocpp_backend {

// User-fixable configuration problem. grpc-server maps this to INVALID_ARGUMENT.
class ConfigError : public std::runtime_error {
public:
    using std::runtime_error::runtime_error;
};

// The family cannot serve the requested RPC. Maps to UNIMPLEMENTED.
class CapabilityError : public std::runtime_error {
public:
    using std::runtime_error::runtime_error;
};

engine::runtime::VoiceTaskKind to_engine_task(Task task);
engine::runtime::RunMode to_engine_mode(Mode mode);
Task from_engine_task(engine::runtime::VoiceTaskKind kind);
Mode from_engine_mode(engine::runtime::RunMode mode);
Capabilities to_capabilities(const std::string &family,
                             const engine::runtime::CapabilitySet &set);

// Reads audiocpp.model_spec.family from a GGUF. Returns an empty string when
// the file is not a GGUF, carries no audio.cpp spec, or cannot be read. Never
// throws: an unreadable file is the load gate's problem, not a crash.
std::string read_gguf_family(const std::string &path);

// Builds the absolute model path from LocalAI's (ModelPath, ModelFile, Model)
// triple. A model file of the form "bundled:<name>" resolves to
// <executable dir>/assets/<name>, which is where package.sh puts upstream's
// bundled silero_vad and marblenet_vad assets.
std::string resolve_model_path(const std::string &model_path_dir,
                               const std::string &model_file,
                               const std::string &model_name);

class LoadedModel {
public:
    struct Session {
        Task task = Task::Tts;
        Mode mode = Mode::Offline;
        // Exactly one of these is non-null, matching the resolved mode.
        engine::runtime::IOfflineVoiceTaskSession *offline = nullptr;
        engine::runtime::IStreamingVoiceTaskSession *streaming = nullptr;
    };

    // Throws ConfigError when the path does not exist, the family cannot be
    // determined, or the registry rejects the family.
    LoadedModel(const std::string &resolved_path, const ModelOptions &options);

    LoadedModel(const LoadedModel &) = delete;
    LoadedModel &operator=(const LoadedModel &) = delete;

    const std::string &family() const noexcept { return capabilities_.family; }
    const std::string &variant() const noexcept { return variant_; }
    const std::string &description() const noexcept { return description_; }
    const std::vector<std::string> &languages() const noexcept { return languages_; }
    const Capabilities &capabilities() const noexcept { return capabilities_; }
    bool supports_timestamps() const noexcept { return supports_timestamps_; }
    const engine::runtime::SessionOptions &session_options() const noexcept {
        return session_options_;
    }

    // Routes the RPC and returns the cached session, creating it on first use.
    // Throws CapabilityError when this family cannot serve the RPC.
    //
    // Call it only while holding this model's lane. The session cache is not
    // itself synchronised, and it does not need to be: the lane admits one
    // caller at a time, which is the same constraint the sessions themselves
    // impose.
    Session session_for(Rpc rpc, const RequestShape &shape);

    // Takes the inference lane, or throws LaneUnavailable. Serializes runs
    // against this model. `requested_timeout_ms` is a per-request wait hint
    // where a value <= 0 means "use the model's configured ceiling"; a hint may
    // only tighten that ceiling, never loosen it.
    //
    // LaneEntry is deliberately immovable, so bind the result to a named local
    // in the scope the inference happens in:
    //
    //     LaneEntry entry = model.acquire(request_hint_ms);
    //
    // which C++17 initializes in place. A handler that has to keep the lane
    // beyond one scope, for instance in a member that outlives the call that
    // took it, wants acquire_owned instead.
    LaneEntry acquire(int requested_timeout_ms);

    // Same lane, heap-allocated so it can be stored or handed on. Prefer
    // acquire: this one adds a null state that the scoped form does not have.
    std::unique_ptr<LaneEntry> acquire_owned(int requested_timeout_ms);

private:
    // Keyed by the enum values so the map needs no custom comparator.
    using SessionKey = std::pair<int, int>;

    // The public constructor runs the load gate, then delegates here. The
    // detour exists because lane_ has to be built from the family in the member
    // initializer list, and the family is only known after the gate has run.
    LoadedModel(const std::string &resolved_path, const ModelOptions &options,
                std::string family);

    // MEMBER ORDER IS LOAD-BEARING BELOW THIS LINE. Members are destroyed in
    // reverse declaration order.
    //
    // lane_ is first so it is destroyed last: nothing that runs during teardown
    // can then find a lane that has already gone.
    InferenceLane lane_;
    // registry_ before model_: the registry owns the loader that produced the
    // model, and the model may hold loader-owned state.
    engine::runtime::ModelRegistry registry_;
    // model_ before sessions_, so sessions_ is destroyed FIRST and the model
    // second. A session is created from the model and must not outlive it. Do
    // not reorder these two.
    std::unique_ptr<engine::runtime::ILoadedVoiceModel> model_;
    std::map<SessionKey, std::unique_ptr<engine::runtime::IVoiceTaskSession>> sessions_;

    engine::runtime::SessionOptions session_options_;
    Capabilities capabilities_;
    std::string variant_;
    std::string description_;
    std::vector<std::string> languages_;
    bool supports_timestamps_ = false;
    int wait_budget_ceiling_ms_ = 0;
};

} // namespace audiocpp_backend
