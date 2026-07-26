// audio.cpp LocalAI gRPC backend.
//
// Links 0xShug0/audio.cpp's engine_runtime through its public
// include/engine/framework/** headers only. Nothing under upstream's app/,
// src/ or tests/ is used: those are application internals, they are where
// upstream expects churn, and upstream is Apache-2.0 while LocalAI is MIT.
//
// This commit adds LoadModel/Free/Status plus the AudioTranscription, VAD and
// Diarize RPCs. The remaining audio RPCs land in later commits.

#include "backend.pb.h"
#include "backend.grpc.pb.h"

#include "audio_io.h"
#include "audio_units.h"
#include "capability_routing.h"
#include "inference_lane.h"
#include "loaded_model.h"
#include "model_options.h"
#include "result_map.h"

#include <grpcpp/grpcpp.h>
#include <grpcpp/server.h>
#include <grpcpp/server_builder.h>
#include <grpcpp/ext/proto_server_reflection_plugin.h>

#include <atomic>
#include <chrono>
#include <csignal>
#include <cstdint>
#include <cstdlib>
#include <exception>
#include <iostream>
#include <limits>
#include <memory>
#include <mutex>
#include <set>
#include <string>
#include <thread>
#include <utility>
#include <vector>

using grpc::Server;
using grpc::ServerBuilder;
using grpc::ServerContext;
// Do NOT alias grpc::Status as Status: the Status RPC method would shadow the
// type and break every other method that names it as a return type.
using GStatus = ::grpc::Status;

namespace {

// Set by the signal handler, read by the thread that owns the server. The whole
// point of the indirection is that the handler itself does nothing else: see
// signal_handler below.
std::atomic<bool> g_shutdown_requested{false};

// One model per process, matching every other LocalAI backend. The mutex guards
// the pointer itself, not the model: swapping or dropping it races with the
// handlers that read it, whereas the model's own concurrency is the inference
// lane's job.
//
// shared_ptr, not unique_ptr. An audio RPC runs for seconds and cannot hold
// g_model_mu for its duration, so it has to work from a reference taken under
// the lock and used after releasing it. Under a unique_ptr, a Free or a reload
// arriving mid-request destroys the model out from under that reference. Every
// handler instead takes a counted reference through snapshot_for(), so Free
// drops the global's reference and whichever request finishes last destroys the
// model.
std::mutex g_model_mu;
std::shared_ptr<audiocpp_backend::LoadedModel> g_model;

// The raw reference-taking primitive. Returns by value, so the caller owns a
// reference for as long as its local lives, and null when no model is loaded.
// Never return LoadedModel& from here: that is the shape that reintroduces the
// use-after-free.
//
// _unchecked because it performs NO request-level validation. Status is its
// only legitimate caller, because Status takes a HealthMessage, which carries
// no ModelIdentity to check. Every RPC that acts on a model must call
// snapshot_for instead; the name is deliberately unpleasant so that reaching
// for it is a visible decision rather than an omission.
std::shared_ptr<audiocpp_backend::LoadedModel> snapshot_unchecked() {
    std::lock_guard<std::mutex> lock(g_model_mu);
    return g_model;
}

// LocalAI's VADRequest carries raw floats and no sample rate at all. Every
// LocalAI VAD backend treats them as 16 kHz mono (backend/go/silero-vad/vad.go
// and backend/go/sherpa-onnx/backend.go both hardcode 16000) and core/backend
// feeds them from a 16 kHz pipeline, so the same assumption is made here rather
// than left implicit. If the proto ever grows a sample rate field, this is the
// constant to delete.
constexpr int kVadSampleRate = 16000;

// The rate every file-fed speech route reads its input at, and the rate the
// spans that come back are therefore interpreted in. Passed to read_audio_file,
// which resamples only when the file differs.
//
// Two independent reasons, and the second is the one that is easy to miss:
//
//   1. Some families refuse anything else outright. silero_vad throws
//      "Silero VAD 16k model only supports sample_rate=16000" and
//      sortformer_diar throws "Sortformer diar currently requires 16 kHz input
//      audio". That is what made a 44.1 kHz upload return INTERNAL with an
//      engine-internal message instead of an answer.
//   2. The families that do NOT refuse still do not all express their result
//      spans in the input's domain. nemotron_asr builds every word timestamp as
//      token_frame * hop_length * subsampling_factor, which is its own 16 kHz
//      feature domain whatever the input was; the handler then converts those
//      spans with the buffer's rate. Feed it 44.1 kHz and every emitted
//      timestamp is 2.76x too small, with a 200 and no diagnostic. Resampling
//      the input to 16 kHz makes the buffer domain and the span domain the same
//      one, which is the only reason the nanoseconds are right.
//
// The cost, stated plainly: vibevoice_asr resamples internally to 24 kHz, so a
// 48 kHz upload now travels 48 -> 16 -> 24 rather than 48 -> 24, losing the
// 8-12 kHz band it could have kept. That is accepted because a silently wrong
// timestamp is worse than a band-limited one, and because every other LocalAI
// ASR path already feeds 16 kHz. If the framework ever publishes a per-model
// preferred input rate, this constant is what should become that lookup.
constexpr int kSpeechSampleRate = 16000;

// Parses ModelOptions.MainGPU into a device index.
//
// Not std::atoi: it returns 0 for anything unparseable, so "gpu1" or a device
// UUID would silently become device 0 and the model would load on the wrong
// device with no diagnostic anywhere. A refusal the operator can read beats a
// wrong answer they cannot see.
int parse_device_index(const std::string &value) {
    size_t consumed = 0;
    long parsed = 0;
    try {
        parsed = std::stol(value, &consumed);
    } catch (const std::exception &) {
        consumed = 0;
    }
    if (consumed != value.size() || parsed < 0 ||
        parsed > std::numeric_limits<int>::max()) {
        throw audiocpp_backend::ConfigError(
            "audio-cpp: main_gpu must be a non-negative device index, got '" +
            value + "'");
    }
    return static_cast<int>(parsed);
}

// Mirrors pkg/grpc/server.go's checkModelIdentity, backend/cpp/llama-cpp,
// backend/cpp/ds4, backend/cpp/privacy-filter and
// backend/python/common/model_identity.py.
//
// Why every backend has to carry this: in distributed mode a worker can recycle
// a stopped backend's gRPC port for a different model's backend, and the
// controller's liveness-only health probe cannot tell a stale cached route from
// a live one. Only the backend knows which model it actually loaded, so only
// the backend can catch it (#10952). Without this, an audio-cpp process reached
// through a stale route answers with a DIFFERENT model's VAD or diarization
// result and a 200, which is a wrong answer nobody can see.
//
// Either side empty means "skip". The request side is empty for a controller
// that predates the field; the loaded side is empty when such a controller
// performed the load. Neither can judge the other, and a false rejection is
// worse than the miss it prevents.
//
// Templated over the request type because every guarded message exposes
// modelidentity(), and one body keeps the rule identical across RPCs rather
// than letting it drift per handler.
//
// The loaded identity is read off the LoadedModel rather than a separate
// global, which is where this differs from llama-cpp. A handler holding the
// model through snapshot_for() is then necessarily judging against the identity
// THAT model was loaded with, and a concurrent reload cannot swap one without
// the other.
template <typename Request>
GStatus check_model_identity(const audiocpp_backend::LoadedModel &model,
                             const Request *request) {
    if (request == nullptr || request->modelidentity().empty()) {
        return GStatus::OK;
    }
    const std::string &loaded = model.identity();
    if (loaded.empty() || loaded == request->modelidentity()) {
        return GStatus::OK;
    }
    // NOT_FOUND plus this exact sentinel is the cross-language wire contract
    // the router matches on (grpcerrors.ModelMismatchSentinel). The code alone
    // is not enough, since NOT_FOUND is returned for unrelated reasons
    // elsewhere, so the substring "model identity mismatch" must survive
    // verbatim through any edit to this message.
    return GStatus(grpc::StatusCode::NOT_FOUND,
                   "audio-cpp: model identity mismatch: loaded \"" + loaded +
                       "\", requested \"" + request->modelidentity() + "\"");
}

// The ONE way an RPC handler should reach the model. Takes the counted
// reference, refuses when nothing is loaded, and runs the identity check, in
// that order. Returns null with `out` set to the status to return; on success
// returns the model and leaves `out` OK.
//
// This exists as a structural guarantee rather than a convenience. The identity
// check used to be two lines every handler had to remember, and nothing failed
// if a new handler forgot them: there is no C++ equivalent of
// pkg/grpc/model_identity_modalities_test.go, so the convention could only rot.
// Every handler already has to call something to obtain the model, so making
// the guarded call the shortest path means the default is correct and skipping
// it requires deliberately typing snapshot_unchecked.
//
// The order is forced: the identity being compared against belongs to the model
// this call is about to use, so it cannot precede taking the reference. And it
// must precede routing, or a mismatched request to a model that cannot serve
// the RPC leaks UNIMPLEMENTED instead of the NOT_FOUND the router matches on.
template <typename Request>
std::shared_ptr<audiocpp_backend::LoadedModel>
snapshot_for(const Request *request, GStatus &out) {
    auto model = snapshot_unchecked();
    if (model == nullptr) {
        out = GStatus(grpc::StatusCode::FAILED_PRECONDITION,
                      "audio-cpp: no model is loaded; call LoadModel first");
        return nullptr;
    }
    out = check_model_identity(*model, request);
    if (!out.ok()) {
        return nullptr;
    }
    return model;
}

// Builds the TaskRequest for a transcription-shaped RPC. `task` is the ROUTED
// audio.cpp task, not a guess from the request: it decides how
// TranscriptRequest.prompt is used, and routing has already decided it.
//
// The audio is taken by value and moved in. A long recording runs to tens of
// megabytes and the caller has no use for it afterwards; the previous shape,
// a const reference, copied it.
engine::runtime::TaskRequest
build_transcription_request(const backend::TranscriptRequest &request,
                            audiocpp_backend::Task task,
                            engine::runtime::AudioBuffer audio) {
    engine::runtime::TaskRequest task_request;
    task_request.audio_input = std::move(audio);

    if (task == audiocpp_backend::Task::Alignment) {
        // For forced alignment the prompt IS the transcript to align, so it
        // becomes the text input rather than a decoding hint. Set even when
        // empty: an aligner given no text should say so itself rather than be
        // handed an audio-only request it cannot describe.
        engine::runtime::Transcript transcript;
        transcript.text = request.prompt();
        transcript.language = request.language();
        task_request.text_input = transcript;
    } else if (!request.prompt().empty()) {
        // For ASR the prompt is decoding context, the whisper meaning.
        task_request.options["prompt"] = request.prompt();
    }

    if (!request.language().empty()) {
        task_request.options["language"] = request.language();
    }
    if (request.translate()) {
        task_request.options["translate"] = "true";
    }
    if (request.temperature() > 0.0f) {
        task_request.options["temperature"] = std::to_string(request.temperature());
    }
    for (const auto &granularity : request.timestamp_granularities()) {
        // Upstream families read this as a request for word-level timing.
        if (granularity == "word") {
            task_request.options["word_timestamps"] = "true";
        }
    }
    // Every option above is advisory. audio.cpp families look their request
    // options up by name (runtime::find_option) and ignore the rest; the
    // unknown-key refusals upstream does have are on SESSION options, which
    // arrive at load time, not here. So an option a family does not read costs
    // nothing, and none of these can turn a valid request into an error.
    //
    // TranscriptRequest.threads is deliberately NOT forwarded: thread count is
    // a SessionOptions field fixed when the session was created, so a
    // per-request value has nowhere to go and pretending otherwise would be a
    // knob that silently does nothing.
    return task_request;
}

// Duration of a possibly multi-channel buffer, in seconds. Frames, not floats:
// a stereo buffer holds two floats per position and would otherwise report
// twice its real length.
float audio_duration_seconds(const engine::runtime::AudioBuffer &audio) {
    const std::int64_t frames = audiocpp_backend::interleaved_frame_count(
        audio.samples.size(), audio.channels);
    return audiocpp_backend::samples_to_seconds(frames, audio.sample_rate);
}

// Maps a thrown exception onto the gRPC status the client should see.
GStatus to_status(const std::exception &err) {
    if (dynamic_cast<const audiocpp_backend::ConfigError *>(&err) != nullptr) {
        return GStatus(grpc::StatusCode::INVALID_ARGUMENT, err.what());
    }
    if (dynamic_cast<const audiocpp_backend::CapabilityError *>(&err) != nullptr) {
        return GStatus(grpc::StatusCode::UNIMPLEMENTED, err.what());
    }
    // The lane was busy and this caller ran out of patience. UNAVAILABLE, not
    // RESOURCE_EXHAUSTED: the request is fine and retrying later is the right
    // response, which is what UNAVAILABLE tells a client.
    if (dynamic_cast<const audiocpp_backend::LaneUnavailable *>(&err) != nullptr) {
        return GStatus(grpc::StatusCode::UNAVAILABLE, err.what());
    }
    return GStatus(grpc::StatusCode::INTERNAL, err.what());
}

class AudioCppBackend final : public backend::Backend::Service {
public:
    GStatus Health(ServerContext *, const backend::HealthMessage *,
                   backend::Reply *reply) override {
        reply->set_message("OK");
        return GStatus::OK;
    }

    GStatus Status(ServerContext *, const backend::HealthMessage *,
                   backend::StatusResponse *response) override {
        // snapshot_unchecked is right here, and is the only place it is:
        // HealthMessage carries no ModelIdentity, so there is nothing to check.
        response->set_state(snapshot_unchecked()
                                ? backend::StatusResponse::READY
                                : backend::StatusResponse::UNINITIALIZED);
        return GStatus::OK;
    }

    GStatus LoadModel(ServerContext *, const backend::ModelOptions *request,
                      backend::Result *result) override {
        try {
            std::vector<std::string> entries(request->options().begin(),
                                             request->options().end());
            auto parsed = audiocpp_backend::parse_model_options(entries);
            if (!parsed.error.empty()) {
                throw audiocpp_backend::ConfigError(parsed.error);
            }
            // ModelOptions.Threads is the model YAML's threads setting; the
            // explicit threads: option wins when both are set.
            if (parsed.options.threads == 0 && request->threads() > 0) {
                parsed.options.threads = static_cast<int>(request->threads());
            }
            // MainGPU carries the device index for GPU backends, and is the
            // fallback: an explicit device: option wins, matching threads above.
            // device_set rather than `device == 0`, because 0 is a real device
            // index and the value alone cannot say whether anyone chose it.
            if (!parsed.options.device_set && !request->maingpu().empty()) {
                parsed.options.device = parse_device_index(request->maingpu());
            }

            const std::string path = audiocpp_backend::resolve_model_path(
                request->modelpath(), request->modelfile(), request->model());

            // ModelOptions.Model is the UNTRANSLATED controller-side name, and
            // is what every ModelIdentity field on a request is compared
            // against. Passed at construction so the model and the identity it
            // was loaded with are published together.
            auto loaded = std::make_shared<audiocpp_backend::LoadedModel>(
                path, parsed.options, request->model());

            // Read everything the reply and the log line need while this scope
            // still owns the model. After the move, `loaded` is null and the
            // global is only safe to touch under the mutex.
            const std::string family = loaded->family();
            const std::string variant = loaded->variant();
            const std::string capabilities =
                audiocpp_backend::describe_capabilities(loaded->capabilities());
            std::shared_ptr<audiocpp_backend::LoadedModel> replaced;
            {
                std::lock_guard<std::mutex> lock(g_model_mu);
                replaced = std::move(g_model);
                g_model = std::move(loaded);
            }
            // The previous model, if any, is dropped outside the lock, for the
            // same reason Free does it: teardown must not hold up Status.
            replaced.reset();

            // Logged once at load time so an operator can see what the model
            // can do without making a request. ModelMetadata is deliberately
            // not implemented: its response message is chat-template oriented
            // and has no field for family, variant, languages or capabilities,
            // so there is nothing truthful to put in it.
            std::cerr << "audio-cpp: loaded family '" << family << "' variant '"
                      << variant << "' capabilities: " << capabilities << "\n";

            result->set_success(true);
            result->set_message("loaded audio.cpp family " + family);
            return GStatus::OK;
        } catch (const std::exception &err) {
            result->set_success(false);
            result->set_message(err.what());
            // A failed load must also be a gRPC error, or the model loader's
            // backend probe treats this backend as having accepted the model.
            return to_status(err);
        }
    }

    GStatus Free(ServerContext *, const backend::HealthMessage *,
                 backend::Result *result) override {
        // Drops the global's reference. Any handler still running holds its own
        // from snapshot_for(), so the model is destroyed by whichever of them
        // finishes last rather than underneath one of them. Destroying
        // LoadedModel releases its sessions first, then the model.
        std::shared_ptr<audiocpp_backend::LoadedModel> released;
        {
            std::lock_guard<std::mutex> lock(g_model_mu);
            released = std::move(g_model);
        }
        // Released outside the lock: if this is the last reference, the model
        // and its sessions are torn down here, and that must not block Status
        // or a fresh LoadModel behind g_model_mu.
        released.reset();
        result->set_success(true);
        return GStatus::OK;
    }

    GStatus AudioTranscription(ServerContext *,
                               const backend::TranscriptRequest *request,
                               backend::TranscriptResult *response) override {
        try {
            GStatus refusal = GStatus::OK;
            const auto model = snapshot_for(request, refusal);
            if (model == nullptr) {
                return refusal;
            }

            audiocpp_backend::RequestShape shape;
            // has_prompt_text is what lets a family that can only align be
            // reached through this RPC at all, so it is not decoration.
            shape.has_prompt_text = !request->prompt().empty();
            shape.pinned_task = model->pinned_task();

            // Capability refusal before the lane and before the file read, for
            // the reasons spelled out in Diarize.
            model->check_can_serve(audiocpp_backend::Rpc::AudioTranscription, shape);

            // TranscriptRequest.dst is the INPUT audio path, despite the field
            // name. The HTTP layer materialises the upload to a temp file and
            // passes the path; nothing is written back.
            auto audio = audiocpp_backend::read_audio_file(request->dst(),
                                                           kSpeechSampleRate);
            // Both read before the buffer is moved into the request below. The
            // rate is the BUFFER's, which after read_audio_file is
            // kSpeechSampleRate and not necessarily the file's, and it is the
            // domain the result spans come back in.
            const int sample_rate = audio.sample_rate;
            const float duration = audio_duration_seconds(audio);

            audiocpp_backend::LaneEntry lane = model->acquire(0);
            const auto session = model->session_for(
                audiocpp_backend::Rpc::AudioTranscription, shape, lane);

            // session.task, not the request: for Asr the prompt is decoding
            // context, for Alignment it is the transcript to align, and routing
            // has already decided which of those this is.
            const auto task_request =
                build_transcription_request(*request, session.task, std::move(audio));
            const auto result =
                audiocpp_backend::run_offline(session, task_request, lane);

            // text comes from result.text_output verbatim, never from the
            // segments. See THE RULE in result_map.h.
            audiocpp_backend::fill_transcript_result(result, sample_rate, duration,
                                                     response);
            // eou stays false. It marks a decode that ended on the model's
            // end-of-utterance token, which is a cache-aware STREAMING concept;
            // an offline run over a whole file has no turn to yield.
            return GStatus::OK;
        } catch (const std::exception &err) {
            return to_status(err);
        }
    }

    // Verifying this by hand: silero_vad is a SPEECH detector, so a synthetic
    // stimulus does not exercise it. A 220 Hz sine, broadband noise and a
    // harmonic buzz all return zero segments, correctly. Use real speech, which
    // upstream bundles and which therefore needs no download:
    //   audio.cpp/assets/resources/sample_16k.wav  (16 kHz mono, 14.07 s)
    // Taking 1 s from offset 8000 samples (0.5 s in), padded with 1 s of
    // silence either side, yields exactly one segment at start=0.9940
    // end=2.0460. A different excerpt of the same file shifts the start,
    // because it changes where speech actually begins inside the window.
    GStatus VAD(ServerContext *, const backend::VADRequest *request,
                backend::VADResponse *response) override {
        try {
            // snapshot_for, and the result is held for the whole call. Free may
            // arrive mid-request; it drops the global's reference only, so this
            // one keeps the model alive until the handler returns. It also
            // performs the not-loaded and identity checks, so a stale route is
            // refused before any work happens.
            GStatus refusal = GStatus::OK;
            const auto model = snapshot_for(request, refusal);
            if (model == nullptr) {
                return refusal;
            }

            audiocpp_backend::RequestShape shape;
            // Without this the `task:` model option is dead: routing is
            // otherwise derived from the RPC alone.
            shape.pinned_task = model->pinned_task();

            // Before the lane. A model that cannot do VAD at all answers
            // immediately instead of queueing behind somebody else's run only
            // to be refused; routing is pure and needs no lane. session_for
            // routes again below and reaches the same answer.
            model->check_can_serve(audiocpp_backend::Rpc::Vad, shape);

            engine::runtime::TaskRequest task;
            task.audio_input = audiocpp_backend::buffer_from_mono(
                std::vector<float>(request->audio().begin(), request->audio().end()),
                kVadSampleRate);

            // The lane is taken BEFORE session_for, not after. session_for
            // reads and writes an unsynchronised session cache and prepare()
            // mutates the session itself, so both belong inside the lane; see
            // the note on session_for in loaded_model.h. Bound to a named local
            // because LaneEntry is immovable and C++17 elides the return.
            audiocpp_backend::LaneEntry lane = model->acquire(0);
            const auto session =
                model->session_for(audiocpp_backend::Rpc::Vad, shape, lane);
            const auto result =
                audiocpp_backend::run_offline(session, task, lane);

            // VADSegment.start/end are float SECONDS, not sample indices.
            for (const auto &segment : result.speech_segments) {
                auto *out = response->add_segments();
                out->set_start(audiocpp_backend::samples_to_seconds(
                    segment.span.start_sample, kVadSampleRate));
                out->set_end(audiocpp_backend::samples_to_seconds(
                    segment.span.end_sample, kVadSampleRate));
            }
            return GStatus::OK;
        } catch (const std::exception &err) {
            return to_status(err);
        }
    }

    GStatus Diarize(ServerContext *, const backend::DiarizeRequest *request,
                    backend::DiarizeResponse *response) override {
        try {
            GStatus refusal = GStatus::OK;
            const auto model = snapshot_for(request, refusal);
            if (model == nullptr) {
                return refusal;
            }

            audiocpp_backend::RequestShape shape;
            shape.pinned_task = model->pinned_task();

            // Route, then read, then lane, in that order, and every step of it
            // is deliberate.
            //
            // The capability refusal comes first because a family that cannot
            // diarize at all should say so, not complain about the input file:
            // on a VAD-only model, reading the audio first would surface
            // "cannot read /tmp/x.wav" and send the operator hunting a file
            // problem instead of a model choice.
            //
            // It also comes before the lane, which is the part that used to be
            // impossible. Routing needs no lane, so the refusal no longer waits
            // out somebody else's thirty second run to be told no, and neither
            // does the file read.
            model->check_can_serve(audiocpp_backend::Rpc::Diarize, shape);

            // DiarizeRequest.dst is the INPUT path, despite the field name: the
            // HTTP layer materialises the upload to a temp file and passes the
            // path here. Nothing is written back.
            //
            // Read at 16 kHz mono: sortformer refuses any other rate, and the
            // HTTP layer copies the upload byte for byte, so whatever the user
            // posted is what arrives. See kSpeechSampleRate.
            auto audio = audiocpp_backend::read_audio_file(request->dst(),
                                                           kSpeechSampleRate);
            const int sample_rate = audio.sample_rate;
            // Read off the buffer before it is moved into the request below.
            // Frames, not floats: a stereo input holds two floats per position
            // and would otherwise report twice its real duration.
            const std::int64_t frames = audiocpp_backend::interleaved_frame_count(
                audio.samples.size(), audio.channels);

            audiocpp_backend::LaneEntry lane = model->acquire(0);
            const auto session =
                model->session_for(audiocpp_backend::Rpc::Diarize, shape, lane);

            engine::runtime::TaskRequest task;
            // Moved, not copied: a long recording runs to tens of megabytes and
            // this is its only owner.
            task.audio_input = std::move(audio);
            // READ THIS BEFORE WIRING THE FIRST DIARIZATION FAMILY. This
            // forwarding is currently DEAD for sortformer, the only diarizer
            // audio.cpp ships, and from the caller's side that is a silent
            // wrong answer, not a soft hint:
            //
            //   - backend.proto documents num_speakers as "exact speaker count
            //     if known (>0 forces)". A caller who asks for 2 and gets the
            //     model's 4 labels back with a 200 and no diagnostic has been
            //     given a wrong answer, so core/backend/diarization.go's
            //     "backends ignore what they don't act on" does not cover it.
            //   - sortformer takes its speaker count from the model package
            //     (assets.model_config.modules.num_speakers; the bundled
            //     variant is 4-speaker), and its postprocess config is parsed
            //     from runtime::SessionOptions, i.e. load time, not from
            //     TaskRequest.options at all.
            //   - worse, its unknown-option check only inspects keys prefixed
            //     "sortformer_diar.", so a bare "num_speakers" here is not even
            //     a recognised key: it is dropped without a trace.
            //
            // The keys are still forwarded, because a family that does read
            // them then works with no change here. But the family that lands
            // MUST either honour num_speakers or refuse it with
            // INVALID_ARGUMENT. No refusal path is added now because there is
            // no diarization family wired yet to test one against, and an
            // untested refusal is its own hazard.
            //
            // Also dropped today, and worth revisiting at the same time:
            // clustering_threshold, min_duration_on and min_duration_off.
            // The two min_duration_* fields are NOT family-specific: they are
            // generic post-filters over the returned turns (drop a turn shorter
            // than min_duration_on, merge two turns of the same speaker
            // separated by less than min_duration_off), so the handler could
            // honour them in about six lines whatever the family does.
            //
            // The knobs that DO reach sortformer (speaker_threshold,
            // speaker_min_frames, speaker_pad_frames, session_len_sec) are
            // session options and arrive through the model YAML's
            // `session.<key>:` entries at load time, not per request.
            if (request->num_speakers() > 0) {
                task.options["num_speakers"] =
                    std::to_string(request->num_speakers());
            }
            if (request->min_speakers() > 0) {
                task.options["min_speakers"] =
                    std::to_string(request->min_speakers());
            }
            if (request->max_speakers() > 0) {
                task.options["max_speakers"] =
                    std::to_string(request->max_speakers());
            }

            const auto result =
                audiocpp_backend::run_offline(session, task, lane);

            // Emitted verbatim, in the order the model produced them. NOT
            // sorted, merged or de-overlapped: sortformer binarizes each speaker
            // independently, so a turn nested inside another speaker's turn is
            // correct output for overlapped speech. LocalAI is overlap-tolerant
            // downstream (core/backend/diarization.go passes segments through
            // and the RTTM renderer handles overlap), so smoothing here would
            // destroy real information.
            std::set<std::string> speakers;
            int id = 0;
            for (const auto &turn : result.speaker_turns) {
                auto *out = response->add_segments();
                out->set_id(id++);
                // Seconds, like VADSegment. Only TranscriptSegment/Word are ns.
                //
                // Converted against the INPUT rate, which is correct and has
                // been checked against upstream rather than assumed: every
                // TimeSpan sortformer emits is built as
                // llround(seconds * 16000.0) (postprocess.cpp). The read above
                // guarantees the buffer is 16 kHz, so the span domain and the
                // input domain are the same one, and this is not a place where
                // a resampling family could silently scale every timestamp by a
                // constant.
                out->set_start(audiocpp_backend::samples_to_seconds(
                    turn.span.start_sample, sample_rate));
                out->set_end(audiocpp_backend::samples_to_seconds(
                    turn.span.end_sample, sample_rate));
                out->set_speaker(turn.speaker_id);
                // text stays EMPTY, including when include_text is set.
                // audio.cpp's SpeakerTurn carries a span and a speaker label
                // only, and TaskResult has no per-segment text anywhere, so
                // there is nothing truthful to put here.
                speakers.insert(turn.speaker_id);
            }
            response->set_num_speakers(static_cast<int>(speakers.size()));
            response->set_duration(
                audiocpp_backend::samples_to_seconds(frames, sample_rate));

            // Only set when the family bundles transcription, which no
            // diarization-only family does; kept because a combined family
            // would fill it in and the field is documented as optional.
            if (result.text_output.has_value()) {
                response->set_language(result.text_output->language);
            }
            return GStatus::OK;
        } catch (const std::exception &err) {
            return to_status(err);
        }
    }
};

void RunServer(const std::string &addr) {
    AudioCppBackend service;
    grpc::EnableDefaultHealthCheckService(true);
    grpc::reflection::InitProtoReflectionServerBuilderPlugin();

    ServerBuilder builder;
    builder.AddListeningPort(addr, grpc::InsecureServerCredentials());
    builder.RegisterService(&service);
    // Audio payloads (PCM buffers, encoded frames) are far larger than the
    // 4 MiB gRPC default.
    builder.SetMaxReceiveMessageSize(256 * 1024 * 1024);
    builder.SetMaxSendMessageSize(256 * 1024 * 1024);

    std::unique_ptr<Server> server(builder.BuildAndStart());
    if (!server) {
        std::cerr << "audio-cpp grpc-server: failed to bind " << addr << "\n";
        std::exit(1);
    }
    std::cerr << "audio-cpp grpc-server listening on " << addr << "\n";

    // Wait() runs off the main thread so the main thread stays free to notice
    // the shutdown flag and call Shutdown itself, outside any signal context.
    std::thread serving([&server] { server->Wait(); });

    // Polled rather than waited on: notifying a condition variable from a
    // signal handler is not async-signal-safe either, so a condition variable
    // would move the same defect rather than fix it. Ten wakeups a second on an
    // otherwise idle process is not worth a more elaborate scheme.
    while (!g_shutdown_requested.load(std::memory_order_relaxed)) {
        std::this_thread::sleep_for(std::chrono::milliseconds(100));
    }

    // In-flight RPCs get three seconds to drain before the server stops hard.
    server->Shutdown(std::chrono::system_clock::now() + std::chrono::seconds(3));
    serving.join();
}

void signal_handler(int) {
    // Sets a lock-free atomic and returns. Nothing else belongs here.
    //
    // Calling grpc::Server::Shutdown directly from a signal handler, which is
    // what this used to do, takes an absl::Mutex. That is not async-signal-safe:
    // the handler can interrupt a thread that already holds the same mutex, and
    // abseil's deadlock detector sees the reentrant acquisition and aborts the
    // process. The observed result was exit 134 and a "dying due to potential
    // deadlock" stack on every SIGTERM, which is exactly the crash signature a
    // real teardown bug would have to compete with.
    g_shutdown_requested.store(true, std::memory_order_relaxed);
}

} // namespace

int main(int argc, char *argv[]) {
    std::string addr = "127.0.0.1:50051";
    for (int i = 1; i < argc; ++i) {
        std::string a = argv[i];
        const std::string addr_flag = "--addr=";
        if (a.rfind(addr_flag, 0) == 0) {
            addr = a.substr(addr_flag.size());
        } else if (a == "--addr" && i + 1 < argc) {
            addr = argv[++i];
        } else if (a == "--help" || a == "-h") {
            std::cout << "Usage: grpc-server --addr=HOST:PORT\n";
            return 0;
        }
    }
    std::signal(SIGINT, signal_handler);
    std::signal(SIGTERM, signal_handler);
    RunServer(addr);
    return 0;
}
