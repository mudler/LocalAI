// privacy-filter LocalAI gRPC backend.
//
// Thin shim over privacy-filter.cpp's flat C API (include/pf.h): a standalone
// GGML engine for the openai-privacy-filter token-classification model family
// (PII NER). It replaces the llama.cpp-patched TokenClassify path for this one
// model family — same GGUF files, no llama.cpp carry-patches.
//
// Only the RPCs the PII tier needs are implemented: LoadModel, TokenClassify,
// plus Health / Status / Free. Everything else inherits the generated base
// class default (UNIMPLEMENTED).

#include "backend.pb.h"
#include "backend.grpc.pb.h"

#include "pf.h"

#include <grpcpp/grpcpp.h>
#include <grpcpp/server.h>
#include <grpcpp/server_builder.h>
#include <grpcpp/ext/proto_server_reflection_plugin.h>

#include <atomic>
#include <chrono>
#include <csignal>
#include <iostream>
#include <memory>
#include <mutex>
#include <string>

using grpc::Server;
using grpc::ServerBuilder;
using grpc::ServerContext;
// NOTE: do NOT alias grpc::Status as Status — the Status RPC method below would
// shadow the type and break the other method signatures. Use GStatus instead.
using GStatus = ::grpc::Status;
using grpc::StatusCode;

namespace {

// The engine is single-model-per-process: LocalAI spawns one backend process
// per loaded model. g_mu guards (re)load against in-flight classification.
std::mutex          g_mu;
pf_ctx *            g_ctx = nullptr;
std::atomic<Server *> g_server{nullptr};

// Resolve the device string the engine expects ("cpu" / "gpu" / "cuda" /
// "vulkan", optionally ":N"). Priority: an explicit "device:..." in
// ModelOptions.Options, then a non-zero NGPULayers as a coarse "use the GPU"
// signal, else CPU. "gpu" lets the engine pick whichever GPU backend this
// binary was compiled with (CUDA or Vulkan), so the same config works on
// either build; pin "device:cuda"/"device:vulkan" to be explicit.
std::string resolve_device(const backend::ModelOptions * opts) {
    for (const auto & o : opts->options()) {
        const std::string prefix = "device:";
        if (o.rfind(prefix, 0) == 0) {
            return o.substr(prefix.size());
        }
    }
    if (opts->ngpulayers() > 0) {
        return "gpu";
    }
    return "cpu";
}

class PrivacyFilterBackend final : public backend::Backend::Service {
public:
    GStatus Health(ServerContext *, const backend::HealthMessage *,
                   backend::Reply * reply) override {
        reply->set_message("OK");
        return GStatus::OK;
    }

    GStatus Status(ServerContext *, const backend::HealthMessage *,
                   backend::StatusResponse * response) override {
        std::lock_guard<std::mutex> lock(g_mu);
        response->set_state(g_ctx ? backend::StatusResponse::READY
                                  : backend::StatusResponse::UNINITIALIZED);
        return GStatus::OK;
    }

    GStatus LoadModel(ServerContext *, const backend::ModelOptions * request,
                      backend::Result * result) override {
        std::lock_guard<std::mutex> lock(g_mu);

        // ModelFile is the absolute path LocalAI resolves; Model is the bare
        // name. Prefer the former, fall back to the latter.
        const std::string path =
            !request->modelfile().empty() ? request->modelfile() : request->model();
        if (path.empty()) {
            result->set_success(false);
            result->set_message("no model path supplied");
            return GStatus::OK;
        }

        const std::string device = resolve_device(request);

        if (g_ctx) { pf_free(g_ctx); g_ctx = nullptr; }

        pf_ctx * ctx = pf_load(path.c_str(), device.c_str(), request->threads());
        const char * err = pf_last_error(ctx);
        if (err) {
            result->set_success(false);
            result->set_message(std::string("privacy-filter load failed: ") + err);
            pf_free(ctx);
            return GStatus::OK;
        }

        // ContextSize, when set, becomes the per-forward window. The engine
        // ignores values that are too small to window (<= 2*halo) and just
        // runs a single forward, so passing it through is always safe.
        if (request->contextsize() > 0) {
            pf_set_window(ctx, request->contextsize());
        }

        g_ctx = ctx;
        result->set_success(true);
        result->set_message("privacy-filter loaded (" + device + ")");
        return GStatus::OK;
    }

    GStatus TokenClassify(ServerContext *, const backend::TokenClassifyRequest * request,
                          backend::TokenClassifyResponse * response) override {
        std::lock_guard<std::mutex> lock(g_mu);
        if (!g_ctx) {
            return GStatus(StatusCode::FAILED_PRECONDITION, "Model not loaded");
        }

        const std::string & text = request->text();
        if (text.empty()) {
            return GStatus::OK;  // no text -> no entities
        }

        pf_entity * ents = nullptr;
        size_t      n    = 0;
        if (pf_classify(g_ctx, text.data(), text.size(), request->threshold(), &ents, &n) != 0) {
            const char * err = pf_last_error(g_ctx);
            return GStatus(StatusCode::INTERNAL,
                           std::string("TokenClassify failed: ") + (err ? err : "unknown"));
        }

        // Byte offsets are into the original UTF-8 text; the engine already
        // applied the threshold and whitespace-trimmed span edges.
        for (size_t i = 0; i < n; i++) {
            backend::TokenClassifyEntity * ent = response->add_entities();
            ent->set_entity_group(ents[i].label ? ents[i].label : "");
            ent->set_start(ents[i].start);
            ent->set_end(ents[i].end);
            ent->set_score(ents[i].score);
            ent->set_text(text.substr((size_t) ents[i].start,
                                      (size_t) (ents[i].end - ents[i].start)));
        }
        pf_entities_free(ents, n);
        return GStatus::OK;
    }

    GStatus Free(ServerContext *, const backend::HealthMessage *,
                 backend::Result * result) override {
        std::lock_guard<std::mutex> lock(g_mu);
        if (g_ctx) { pf_free(g_ctx); g_ctx = nullptr; }
        result->set_success(true);
        return GStatus::OK;
    }
};

void RunServer(const std::string & addr) {
    PrivacyFilterBackend service;
    grpc::EnableDefaultHealthCheckService(true);
    grpc::reflection::InitProtoReflectionServerBuilderPlugin();

    ServerBuilder builder;
    builder.AddListeningPort(addr, grpc::InsecureServerCredentials());
    builder.RegisterService(&service);
    builder.SetMaxReceiveMessageSize(64 * 1024 * 1024);
    builder.SetMaxSendMessageSize(64 * 1024 * 1024);

    std::unique_ptr<Server> server(builder.BuildAndStart());
    if (!server) {
        std::cerr << "privacy-filter grpc-server: failed to bind " << addr << "\n";
        std::exit(1);
    }
    g_server = server.get();
    std::cerr << "privacy-filter grpc-server listening on " << addr << "\n";
    server->Wait();
}

void signal_handler(int) {
    if (auto * srv = g_server.load()) {
        srv->Shutdown(std::chrono::system_clock::now() + std::chrono::seconds(3));
    }
}

} // namespace

int main(int argc, char * argv[]) {
    std::string addr = "127.0.0.1:50051";
    for (int i = 1; i < argc; ++i) {
        std::string a = argv[i];
        const std::string addr_flag = "--addr=";
        if (a.rfind(addr_flag, 0) == 0)      addr = a.substr(addr_flag.size());
        else if (a == "--addr" && i + 1 < argc) addr = argv[++i];
        else if (a == "--help" || a == "-h") {
            std::cout << "Usage: grpc-server --addr=HOST:PORT\n";
            return 0;
        }
    }
    std::signal(SIGINT,  signal_handler);
    std::signal(SIGTERM, signal_handler);
    RunServer(addr);
    return 0;
}
