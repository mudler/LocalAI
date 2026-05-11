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

// Parse a "key:value" option string. Returns empty when no colon.
static std::pair<std::string, std::string> split_option(const std::string &opt) {
    auto colon = opt.find(':');
    if (colon == std::string::npos) return {opt, ""};
    return {opt.substr(0, colon), opt.substr(colon + 1)};
}

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

    Status LoadModel(ServerContext *, const backend::ModelOptions *request,
                     backend::Result *result) override {
        std::lock_guard<std::mutex> lock(g_engine_mu);

        if (g_engine) {
            if (g_session) { ds4_session_free(g_session); g_session = nullptr; }
            ds4_engine_close(g_engine);
            g_engine = nullptr;
        }

        std::string model_path = request->modelfile();
        if (model_path.empty()) model_path = request->model();
        if (model_path.empty()) {
            result->set_success(false);
            result->set_message("ds4: ModelOptions.Model or .ModelFile must be set");
            return Status::OK;
        }

        std::string mtp_path;
        int mtp_draft = 0;
        float mtp_margin = 3.0f;
        for (const auto &opt : request->options()) {
            auto [k, v] = split_option(opt);
            if (k == "mtp_path") mtp_path = v;
            else if (k == "mtp_draft") mtp_draft = std::stoi(v);
            else if (k == "mtp_margin") mtp_margin = std::stof(v);
            else if (k == "kv_cache_dir") g_kv_cache_dir = v;
        }

        ds4_engine_options opt = {};
        opt.model_path = model_path.c_str();
        opt.mtp_path = mtp_path.empty() ? nullptr : mtp_path.c_str();
        opt.n_threads = request->threads() > 0 ? request->threads() : 0;
        opt.mtp_draft_tokens = mtp_draft;
        opt.mtp_margin = mtp_margin;
        opt.directional_steering_file = nullptr;
        opt.warm_weights = false;
        opt.quality = false;

#if defined(DS4_NO_GPU)
        opt.backend = DS4_BACKEND_CPU;
#elif defined(__APPLE__)
        opt.backend = DS4_BACKEND_METAL;
#else
        opt.backend = DS4_BACKEND_CUDA;
#endif

        int rc = ds4_engine_open(&g_engine, &opt);
        if (rc != 0 || !g_engine) {
            result->set_success(false);
            result->set_message("ds4_engine_open failed (rc=" + std::to_string(rc) + ")");
            return Status::OK;
        }

        g_ctx_size = request->contextsize() > 0 ? request->contextsize() : 32768;
        rc = ds4_session_create(&g_session, g_engine, g_ctx_size);
        if (rc != 0 || !g_session) {
            ds4_engine_close(g_engine);
            g_engine = nullptr;
            result->set_success(false);
            result->set_message("ds4_session_create failed (rc=" + std::to_string(rc) + ")");
            return Status::OK;
        }

        result->set_success(true);
        result->set_message("loaded " + model_path);
        return Status::OK;
    }
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
