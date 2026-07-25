// audio.cpp LocalAI gRPC backend.
//
// Links 0xShug0/audio.cpp's engine_runtime through its public
// include/engine/framework/** headers only. Nothing under upstream's app/,
// src/ or tests/ is used: those are application internals, they are where
// upstream expects churn, and upstream is Apache-2.0 while LocalAI is MIT.
//
// This commit is the bind/listen/Health/Status skeleton. Model loading and the
// audio RPCs land in later commits.

#include "backend.pb.h"
#include "backend.grpc.pb.h"

#include <grpcpp/grpcpp.h>
#include <grpcpp/server.h>
#include <grpcpp/server_builder.h>
#include <grpcpp/ext/proto_server_reflection_plugin.h>

#include <atomic>
#include <chrono>
#include <csignal>
#include <cstdlib>
#include <iostream>
#include <memory>
#include <string>

using grpc::Server;
using grpc::ServerBuilder;
using grpc::ServerContext;
// Do NOT alias grpc::Status as Status: the Status RPC method would shadow the
// type and break every other method that names it as a return type.
using GStatus = ::grpc::Status;

namespace {

std::atomic<Server *> g_server{nullptr};

class AudioCppBackend final : public backend::Backend::Service {
public:
    GStatus Health(ServerContext *, const backend::HealthMessage *,
                   backend::Reply *reply) override {
        reply->set_message("OK");
        return GStatus::OK;
    }

    GStatus Status(ServerContext *, const backend::HealthMessage *,
                   backend::StatusResponse *response) override {
        response->set_state(backend::StatusResponse::UNINITIALIZED);
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
