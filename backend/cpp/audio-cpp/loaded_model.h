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

#include <functional>
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
    //
    // `model_identity` is ModelOptions.Model verbatim: the UNTRANSLATED
    // controller-side name. It is a constructor argument rather than a setter
    // so identity and model are inseparable. llama-cpp keeps its equivalent in
    // a separate global from the model, which leaves a window where a handler
    // can read one without the other; here a handler that holds the model
    // through snapshot() necessarily holds the identity it was loaded with.
    LoadedModel(const std::string &resolved_path, const ModelOptions &options,
                std::string model_identity);

    LoadedModel(const LoadedModel &) = delete;
    LoadedModel &operator=(const LoadedModel &) = delete;

    const std::string &family() const noexcept { return capabilities_.family; }
    // Empty when the controller predates ModelOptions.ModelIdentity, which the
    // identity check reads as "skip". See check_model_identity in grpc-server.
    const std::string &identity() const noexcept { return identity_; }
    const std::string &variant() const noexcept { return variant_; }
    const std::string &description() const noexcept { return description_; }
    const std::vector<std::string> &languages() const noexcept { return languages_; }
    const Capabilities &capabilities() const noexcept { return capabilities_; }
    bool supports_timestamps() const noexcept { return supports_timestamps_; }
    const engine::runtime::SessionOptions &session_options() const noexcept {
        return session_options_;
    }

    // The model's `task:` option, empty when unset. Every handler must copy it
    // into RequestShape::pinned_task before calling session_for: routing is
    // otherwise derived from the RPC alone, and this is the option's only route
    // from the load to the request that honours it.
    const std::string &pinned_task() const noexcept { return pinned_task_; }

    // Throws the same CapabilityError session_for would throw when this family
    // cannot serve the RPC, and RETURNS THE RESOLVED ROUTE otherwise.
    //
    // The route is returned rather than computed and dropped because a handler
    // often has to know which task it is about to run BEFORE running it.
    // AudioTransform refuses params[stem] on any route but source separation,
    // and reading that off the route costs microseconds where reading it off
    // the result costs a whole inference first. A caller with no such need
    // ignores the value, which is what the three transcription-shaped handlers
    // do.
    //
    // It exists so a refusal does not have to buy a place in the queue first.
    // resolve_route is a pure function of capabilities_, which is fixed at
    // construction and never written again, so unlike the session cache it
    // needs no lane and no lock: a model that cannot transcribe can say so
    // while another request is halfway through a thirty second run. Without
    // this the refusal waits for that run to finish only to be told no.
    //
    // It does NOT replace the routing inside session_for, and must not be made
    // to: session_for still needs the route to key the session cache. The two
    // calls agree because both read the same immutable capabilities. What this
    // one adds is only the ordering, so call it before acquire().
    //
    // Const and lane-free on purpose. If a future edit makes routing depend on
    // mutable state, this must grow the lane parameter its siblings carry.
    Route check_can_serve(Rpc rpc, const RequestShape &shape) const;

    // Routes the RPC and returns the cached session, creating it on first use.
    // Throws CapabilityError when this family cannot serve the RPC, and a plain
    // runtime_error when it can but the session could not be built, which is an
    // environment fault rather than a capability answer.
    //
    // The `lane` parameter is a PROOF OF HOLDING and is otherwise unused: it
    // exists so the rule below is a compile error rather than prose. The
    // session cache is an unsynchronised std::map and the sessions themselves
    // are not reentrant, so this must only be called with the lane held; the
    // lane admits one caller at a time, which is exactly the constraint the
    // sessions impose. Pass the LaneEntry from acquire().
    //
    // NON-CONST reference on purpose, and do not "tidy" it to const. A const
    // reference binds to a temporary, which makes this compile:
    //
    //     auto session = model->session_for(rpc, shape, model->acquire(0));
    //     auto result  = run_offline(session, task, model->acquire(0));
    //
    // and each temporary dies at the end of its own full-expression, so the
    // lane is released between the two calls. That is precisely the split this
    // parameter exists to prevent, and it is the form a future caller is most
    // likely to reach for because it reads as tidy. Requiring an lvalue forces
    // a named entry whose scope spans both calls.
    //
    // What it proves is bounded, so do not over-trust it: it proves A lane was
    // taken, not THIS model's lane. A caller determined to defeat it can
    // construct an entry on an unrelated InferenceLane and pass that. It
    // therefore catches the two mistakes that actually happen, forgetting the
    // lane entirely and taking it after routing, and does not catch lane
    // identity.
    //
    // STATE CONTRACT, and it is the CALLER'S to honour. Sessions are cached per
    // (task, mode), so a streaming session is normally the same warm object the
    // previous stream used, carrying that stream's state. session_for hands it
    // back as it is.
    //
    // Every streaming caller must therefore begin a stream through
    // begin_stream() below, which is prepare() then start_stream() in that
    // order and is the ONLY implementation of that sequence. start_stream's
    // base implementation is a call to reset(), which is what clears the
    // previous stream, and reset() is only legal after prepare(): silero_vad
    // throws "session prepare() must be called before Silero VAD reset()"
    // otherwise. That ordering constraint is also why session_for cannot do
    // this for you. Skipping it does not raise an error, it silently continues
    // the previous stream.
    //
    // Offline sessions need no such care: their interface has no reset and
    // run() takes a whole request.
    Session session_for(Rpc rpc, const RequestShape &shape, LaneEntry &lane);

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
    // Four parameters rather than three so it cannot be confused with the
    // public constructor, whose third argument is also a std::string.
    LoadedModel(const std::string &resolved_path, const ModelOptions &options,
                std::string family, std::string model_identity);

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
    std::string pinned_task_;
    std::string identity_;
    bool supports_timestamps_ = false;
    int wait_budget_ceiling_ms_ = 0;
};

// Prepares and runs an offline session. prepare() is called for every run
// rather than once per session, because SessionPreparationRequest is derived
// from the request itself (audio contract, text, voice condition) and not from
// the model: a second request with a different sample rate or length would
// otherwise run against the first request's contract.
//
// `lane` is a PROOF OF HOLDING, unused at runtime, for the same reason
// session_for takes one: the session is not reentrant and prepare() mutates it,
// so running without the lane is a data race. Making it a parameter turns that
// into a compile error instead of a comment. Non-const for the same reason as
// session_for's: a const reference would bind to `model.acquire(0)` written
// inline, and that temporary dies at the end of this call, releasing the lane
// before the caller's next one.
//
// Throws CapabilityError when the session is not an offline one. That should be
// unreachable through session_for, which already refuses a non-offline session
// for an offline route, and is checked anyway because the alternative is a null
// dereference.
engine::runtime::TaskResult run_offline(const LoadedModel::Session &session,
                                        const engine::runtime::TaskRequest &request,
                                        LaneEntry &lane);

// THE ONE IMPLEMENTATION of the streaming state obligation described in
// session_for's STATE CONTRACT: prepare(), then start_stream(), in that order.
//
// It is a function rather than a comment because the obligation is invisible
// when it is broken. Streaming sessions are CACHED per (task, mode), so the
// object a second stream gets is the warm one the first stream left behind,
// still holding its audio, its tokens and its started flag. What clears it is
// start_stream, whose base implementation IS a reset() and whose seven family
// overrides (nemotron_asr, vibevoice_asr, higgs_audio_stt, voxtral_realtime,
// supertonic, omnivoice, voxcpm2) every one call reset() as their first
// statement, verified in the pinned checkout. Nothing in the type system pins
// that. A future override that dropped the reset would break every call site
// at once with no compile error and no exception, only a second transcript
// that begins with the first one's audio, so the fewer call sites there are to
// break, the better: this is the only one.
//
// prepare() must come first and cannot be folded into session_for, because
// reset() is illegal before prepare() (silero_vad throws "session prepare()
// must be called before Silero VAD reset()"), and because the preparation
// request is derived from the REQUEST, not the model: build_preparation_request
// reads the audio contract, the text and the voice condition off it, so a
// second stream with a different sample rate or length would otherwise run
// against the first stream's contract.
//
// `lane` is a PROOF OF HOLDING, unused at runtime, exactly as in run_offline.
//
// Throws CapabilityError when the session is not a streaming one.
void begin_stream(const LoadedModel::Session &session,
                  const engine::runtime::TaskRequest &request, LaneEntry &lane);

// Drives a streaming session that takes NO incremental input, which is the TTS
// shape (StreamingInputKind::None, StreamingOutputKind::PullEvents): begin the
// stream, pull events until the session says there are no more, then finish.
//
// NO STREAM EVENT SINK IS INSTALLED HERE, and that is deliberate rather than an
// omission. voxcpm2's start_stream runs the whole synthesis and pushes every
// chunk to the sink, then its next_stream_event replays those same chunks out
// of the stored result, so a sink on this path would put every chunk of audio
// on the wire twice. supertonic and omnivoice ignore set_stream_event_sink
// outright. The pull loop is therefore the single delivery channel.
//
// The returned TaskResult is the session's own merged whole for all three
// families, NOT a tail the pull loop missed. A caller that already emitted the
// pulled events must not also emit its audio; see the TTSStream handler.
engine::runtime::TaskResult run_streaming_pull(
    const LoadedModel::Session &session,
    const engine::runtime::TaskRequest &request,
    const std::function<void(const engine::runtime::StreamEvent &)> &on_event,
    LaneEntry &lane);

// Drives a streaming session that CONSUMES audio chunks, which is the ASR shape
// (StreamingInputKind::AudioChunks): begin the stream, feed the buffer in
// policy-sized chunks, then finalize.
//
// A STREAM EVENT SINK IS INSTALLED HERE, and it is not optional: nemotron_asr
// reports its partial text ONLY through the sink, and only from inside
// finalize(), because its decode does not start until the audio is complete.
// Without the sink that family streams a transcript with no partials at all.
// The sink is cleared again before returning, including on the exception path:
// the session is cached and outlives this call, so a sink left holding a
// reference to the caller's frame is a use after free waiting for the next
// stream.
//
// Both delivery channels are consumed, the sink and the value process_audio_chunk
// returns, because the families do not agree on which they use, and
// voxtral_realtime uses BOTH for the same event. The duplicate that produces is
// absorbed by TranscriptDeltaTracker in stream_delta.h rather than here.
engine::runtime::TaskResult run_streaming_audio(
    const LoadedModel::Session &session,
    const engine::runtime::TaskRequest &request,
    const engine::runtime::AudioBuffer &audio,
    const std::function<void(const engine::runtime::StreamEvent &)> &on_event,
    LaneEntry &lane);

} // namespace audiocpp_backend
