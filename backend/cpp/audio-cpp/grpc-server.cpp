// audio.cpp LocalAI gRPC backend.
//
// Links 0xShug0/audio.cpp's engine_runtime through its public
// include/engine/framework/** headers only. Nothing under upstream's app/,
// src/ or tests/ is used: those are application internals, they are where
// upstream expects churn, and upstream is Apache-2.0 while LocalAI is MIT.
//
// This commit adds LoadModel/Free/Status over one audio.cpp model. The audio
// RPCs themselves land in later commits.

#include "backend.pb.h"
#include "backend.grpc.pb.h"

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
#include <cstdlib>
#include <exception>
#include <iostream>
#include <limits>
#include <memory>
#include <mutex>
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
