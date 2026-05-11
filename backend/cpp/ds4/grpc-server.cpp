// ds4 LocalAI gRPC backend.
//
// Wraps antirez/ds4's `ds4_engine_*` / `ds4_session_*` public API
// (see ds4/ds4.h) over LocalAI's backend.proto. Tool calls, thinking
// mode, and disk KV cache are wired in follow-up commits; this commit
// is just the bind/listen/Health/Free skeleton.

#include "backend.pb.h"
#include "backend.grpc.pb.h"

#include "dsml_parser.h"   // populated in Task 12
#include "dsml_renderer.h" // populated in Task 16
#include "kv_cache.h"      // populated in Task 17

extern "C" {
#include "ds4.h"
}

#include <grpcpp/grpcpp.h>
#include <grpcpp/server.h>
#include <grpcpp/server_builder.h>
#include <grpcpp/ext/proto_server_reflection_plugin.h>

#include <atomic>
#include <chrono>
#include <csignal>
#include <cstring>
#include <iostream>
#include <memory>
#include <mutex>
#include <string>
#include <thread>

using grpc::Server;
using grpc::ServerBuilder;
using grpc::ServerContext;
using grpc::ServerWriter;
using grpc::Status;
using grpc::StatusCode;

namespace {

// Global state - ds4 is single-engine-per-process by design.
std::mutex g_engine_mu;
ds4_engine *g_engine = nullptr;
ds4_session *g_session = nullptr;
int g_ctx_size = 32768;
std::string g_kv_cache_dir; // empty disables disk cache

std::atomic<Server *> g_server{nullptr};

class DS4Backend final : public backend::Backend::Service {
public:
    Status Health(ServerContext *, const backend::HealthMessage *,
                  backend::Reply *reply) override {
        reply->set_message(std::string("OK"));
        return Status::OK;
    }

    Status Free(ServerContext *, const backend::HealthMessage *,
                backend::Result *result) override {
        std::lock_guard<std::mutex> lock(g_engine_mu);
        if (g_session) { ds4_session_free(g_session); g_session = nullptr; }
        if (g_engine)  { ds4_engine_close(g_engine);  g_engine  = nullptr; }
        result->set_success(true);
        return Status::OK;
    }

    // LoadModel / TokenizeString / Predict / PredictStream / Status are
    // added in subsequent tasks. Defaults return UNIMPLEMENTED.
};

void RunServer(const std::string &addr) {
    DS4Backend service;
    grpc::EnableDefaultHealthCheckService(true);
    grpc::reflection::InitProtoReflectionServerBuilderPlugin();

    ServerBuilder builder;
    builder.AddListeningPort(addr, grpc::InsecureServerCredentials());
    builder.RegisterService(&service);
    builder.SetMaxReceiveMessageSize(64 * 1024 * 1024);
    builder.SetMaxSendMessageSize(64 * 1024 * 1024);

    std::unique_ptr<Server> server(builder.BuildAndStart());
    if (!server) {
        std::cerr << "ds4 grpc-server: failed to bind " << addr << "\n";
        std::exit(1);
    }
    g_server = server.get();
    std::cerr << "ds4 grpc-server listening on " << addr << "\n";
    server->Wait();
}

void signal_handler(int) {
    if (auto *srv = g_server.load()) {
        srv->Shutdown(std::chrono::system_clock::now() +
                      std::chrono::seconds(3));
    }
}

} // namespace

int main(int argc, char *argv[]) {
    std::string addr = "127.0.0.1:50051";
    for (int i = 1; i < argc; ++i) {
        std::string a = argv[i];
        const std::string addr_flag = "--addr=";
        if (a.rfind(addr_flag, 0) == 0) addr = a.substr(addr_flag.size());
        else if (a == "--addr" && i + 1 < argc) addr = argv[++i];
        else if (a == "--help" || a == "-h") {
            std::cout << "Usage: grpc-server --addr=HOST:PORT\n";
            return 0;
        }
    }
    std::signal(SIGINT, signal_handler);
    std::signal(SIGTERM, signal_handler);
    RunServer(addr);
    return 0;
}
