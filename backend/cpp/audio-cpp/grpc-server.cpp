// audio.cpp LocalAI gRPC backend.
//
// Links 0xShug0/audio.cpp's engine_runtime through its public
// include/engine/framework/** headers only. Nothing under upstream's app/,
// src/ or tests/ is used: those are application internals, they are where
// upstream expects churn, and upstream is Apache-2.0 while LocalAI is MIT.
//
// This commit adds LoadModel/Free/Status plus the VAD and Diarize RPCs. The
// remaining audio RPCs land in later commits.

#include "backend.pb.h"
#include "backend.grpc.pb.h"

#include "audio_io.h"
#include "audio_units.h"
#include "capability_routing.h"
#include "inference_lane.h"
#include "loaded_model.h"
#include "model_options.h"

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
// handler instead takes a counted reference through snapshot(), so Free drops
// the global's reference and whichever request finishes last destroys the model.
std::mutex g_model_mu;
std::shared_ptr<audiocpp_backend::LoadedModel> g_model;

// The only correct way for a handler to reach the model. Returns by value, so
// the caller owns a reference for as long as its local lives, and null when no
// model is loaded. Never return LoadedModel& from here: that is the shape that
// reintroduces the use-after-free.
std::shared_ptr<audiocpp_backend::LoadedModel> snapshot() {
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
        response->set_state(snapshot() ? backend::StatusResponse::READY
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

            auto loaded = std::make_shared<audiocpp_backend::LoadedModel>(
                path, parsed.options);

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
        // from snapshot(), so the model is destroyed by whichever of them
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

    GStatus VAD(ServerContext *, const backend::VADRequest *request,
                backend::VADResponse *response) override {
        try {
            // snapshot(), and the result is held for the whole call. Free may
            // arrive mid-request; it drops the global's reference only, so this
            // one keeps the model alive until the handler returns.
            const auto model = snapshot();
            if (model == nullptr) {
                return GStatus(grpc::StatusCode::FAILED_PRECONDITION,
                               "audio-cpp: no model is loaded; call LoadModel first");
            }

            audiocpp_backend::RequestShape shape;
            // Without this the `task:` model option is dead: routing is
            // otherwise derived from the RPC alone.
            shape.pinned_task = model->pinned_task();

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
                model->session_for(audiocpp_backend::Rpc::Vad, shape);
            const auto result = audiocpp_backend::run_offline(session, task);

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
            const auto model = snapshot();
            if (model == nullptr) {
                return GStatus(grpc::StatusCode::FAILED_PRECONDITION,
                               "audio-cpp: no model is loaded; call LoadModel first");
            }

            audiocpp_backend::RequestShape shape;
            shape.pinned_task = model->pinned_task();

            // Lane then route then read, in that order, and the read is last on
            // purpose. A family that cannot diarize at all should say so, not
            // complain about the input file first: on a VAD-only model, reading
            // the audio would surface "cannot read /tmp/x.wav" and send the
            // operator hunting a file problem instead of a model choice. The
            // read costs a lane wait in exchange, which is the same wait every
            // capability refusal already pays because session_for needs the lane.
            audiocpp_backend::LaneEntry lane = model->acquire(0);
            const auto session =
                model->session_for(audiocpp_backend::Rpc::Diarize, shape);

            // DiarizeRequest.dst is the INPUT path, despite the field name: the
            // HTTP layer materialises the upload to a temp file and passes the
            // path here. Nothing is written back.
            auto audio = audiocpp_backend::read_audio_file(request->dst());
            const int sample_rate = audio.sample_rate;
            // Read off the buffer before it is moved into the request below.
            // Frames, not floats: a stereo input holds two floats per position
            // and would otherwise report twice its real duration.
            const std::int64_t frames = audiocpp_backend::interleaved_frame_count(
                audio.samples.size(), audio.channels);

            engine::runtime::TaskRequest task;
            // Moved, not copied: a long recording runs to tens of megabytes and
            // this is its only owner.
            task.audio_input = std::move(audio);
            // Forwarded verbatim, and deliberately not validated against what
            // the family can do. audio.cpp's sortformer diarizer takes its
            // speaker count from the model package (the bundled variant is
            // 4-speaker) and reads none of these keys, so today they have no
            // effect. That is the documented LocalAI contract, not a silent
            // failure: core/backend/diarization.go says outright that backends
            // ignore the hints they do not act on. Forwarding them still means a
            // family that does read them gets them, with no change here. The
            // knobs that DO reach sortformer (speaker_threshold,
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

            const auto result = audiocpp_backend::run_offline(session, task);

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
