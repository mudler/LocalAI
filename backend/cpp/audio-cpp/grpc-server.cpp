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
#include <memory>
#include <mutex>
#include <string>
#include <utility>
#include <vector>

using grpc::Server;
using grpc::ServerBuilder;
using grpc::ServerContext;
// Do NOT alias grpc::Status as Status: the Status RPC method would shadow the
// type and break every other method that names it as a return type.
using GStatus = ::grpc::Status;

namespace {

std::atomic<Server *> g_server{nullptr};

// One model per process, matching every other LocalAI backend. The mutex guards
// the pointer itself, not the model: swapping or dropping it races with the
// handlers that read it, whereas the model's own concurrency is the inference
// lane's job.
std::mutex g_model_mu;
std::unique_ptr<audiocpp_backend::LoadedModel> g_model;

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
        std::lock_guard<std::mutex> lock(g_model_mu);
        response->set_state(g_model ? backend::StatusResponse::READY
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
            // MainGPU carries the device index for GPU backends.
            if (parsed.options.device == 0 && !request->maingpu().empty()) {
                parsed.options.device = std::atoi(request->maingpu().c_str());
            }

            const std::string path = audiocpp_backend::resolve_model_path(
                request->modelpath(), request->modelfile(), request->model());

            auto loaded = std::make_unique<audiocpp_backend::LoadedModel>(
                path, parsed.options);

            // Read everything the reply and the log line need while this scope
            // still owns the model. After the move, `loaded` is null and the
            // global is only safe to touch under the mutex.
            const std::string family = loaded->family();
            const std::string variant = loaded->variant();
            const std::string capabilities =
                audiocpp_backend::describe_capabilities(loaded->capabilities());
            {
                std::lock_guard<std::mutex> lock(g_model_mu);
                g_model = std::move(loaded);
            }

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
        std::lock_guard<std::mutex> lock(g_model_mu);
        // Destroying LoadedModel releases its sessions first, then the model.
        g_model.reset();
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
    g_server = server.get();
    std::cerr << "audio-cpp grpc-server listening on " << addr << "\n";
    server->Wait();
}

void signal_handler(int) {
    if (auto *srv = g_server.load()) {
        srv->Shutdown(std::chrono::system_clock::now() + std::chrono::seconds(3));
    }
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
