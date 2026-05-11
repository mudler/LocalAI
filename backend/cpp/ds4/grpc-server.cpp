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
#include <vector>

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

static void append_token_text(ds4_engine *engine, int token, std::string &out) {
    size_t len = 0;
    const char *text = ds4_token_text(engine, token, &len);
    if (text && len > 0) out.append(text, len);
}

struct CollectCtx {
    ds4_engine *engine;
    std::string raw_buf;  // exact raw bytes for Reply.message
    ds4_backend::DsmlParser parser;
    backend::Reply *reply;
    int tokens;

    // Per-tool aggregation: accumulate ChatDelta tool_calls so we emit one
    // delta with all calls, mirroring how vllm's non-streaming path returns.
    struct Pending {
        std::string id;
        std::string name;
        std::string args;
    };
    std::vector<Pending> pending;

    std::string content_buf;
    std::string reasoning_buf;
};

static void apply_events(CollectCtx *c, const std::vector<ds4_backend::ParserEvent> &events) {
    for (const auto &e : events) {
        switch (e.type) {
        case ds4_backend::ParserEvent::CONTENT:
            c->content_buf += e.text;
            break;
        case ds4_backend::ParserEvent::REASONING:
            c->reasoning_buf += e.text;
            break;
        case ds4_backend::ParserEvent::TOOL_START:
            if ((int)c->pending.size() <= e.index)
                c->pending.resize(e.index + 1);
            c->pending[e.index].id = e.tool_id;
            c->pending[e.index].name = e.tool_name;
            break;
        case ds4_backend::ParserEvent::TOOL_ARGS:
            if ((int)c->pending.size() > e.index)
                c->pending[e.index].args += e.text;
            break;
        case ds4_backend::ParserEvent::TOOL_END:
            // No-op for non-streaming: the final delta is emitted at the end.
            break;
        }
    }
}

static void collect_emit(void *ud, int token) {
    auto *c = static_cast<CollectCtx *>(ud);
    if (token == ds4_token_eos(c->engine)) return;
    size_t len = 0;
    const char *text = ds4_token_text(c->engine, token, &len);
    if (!text || len == 0) return;
    std::string chunk(text, len);
    c->raw_buf += chunk;
    std::vector<ds4_backend::ParserEvent> events;
    c->parser.Feed(chunk, events);
    apply_events(c, events);
    c->tokens++;
}
static void collect_done(void *) {}

struct StreamCtx {
    ds4_engine *engine;
    ServerWriter<backend::Reply> *writer;
    int tokens;
    bool aborted;
};
static void stream_emit(void *ud, int token) {
    auto *s = static_cast<StreamCtx *>(ud);
    if (s->aborted) return;
    if (token == ds4_token_eos(s->engine)) return;
    std::string text;
    append_token_text(s->engine, token, text);
    if (text.empty()) return;
    backend::Reply chunk;
    chunk.set_message(text);
    chunk.set_tokens(1);
    if (!s->writer->Write(chunk)) s->aborted = true;
    s->tokens++;
}
static void stream_done(void *) {}

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

    Status TokenizeString(ServerContext *, const backend::PredictOptions *request,
                          backend::TokenizationResponse *response) override {
        std::lock_guard<std::mutex> lock(g_engine_mu);
        if (!g_engine) return Status(StatusCode::FAILED_PRECONDITION, "ds4: model not loaded");
        ds4_tokens out = {};
        ds4_tokenize_text(g_engine, request->prompt().c_str(), &out);
        for (int i = 0; i < out.len; ++i) response->add_tokens(out.v[i]);
        response->set_length(out.len);
        ds4_tokens_free(&out);
        return Status::OK;
    }

    Status Predict(ServerContext *, const backend::PredictOptions *request,
                   backend::Reply *reply) override {
        std::lock_guard<std::mutex> lock(g_engine_mu);
        if (!g_engine || !g_session) {
            return Status(StatusCode::FAILED_PRECONDITION, "ds4: model not loaded");
        }
        ds4_tokens prompt = {};
        ds4_tokenize_text(g_engine, request->prompt().c_str(), &prompt);
        int n_predict = request->tokens() > 0 ? request->tokens() : 256;

        CollectCtx collect = {g_engine, "", {}, reply, 0, {}, "", ""};
        int rc = ds4_engine_generate_argmax(g_engine, &prompt, n_predict, g_ctx_size,
                                            collect_emit, collect_done, &collect,
                                            nullptr, nullptr);
        int prompt_len = prompt.len;
        ds4_tokens_free(&prompt);

        // Flush any buffered parser state.
        std::vector<ds4_backend::ParserEvent> events;
        collect.parser.Flush(events);
        apply_events(&collect, events);

        if (rc != 0) {
            return Status(StatusCode::INTERNAL,
                          "ds4_engine_generate_argmax rc=" + std::to_string(rc));
        }

        // Emit one ChatDelta with content/reasoning/tool_calls.
        auto *delta = reply->add_chat_deltas();
        delta->set_content(collect.content_buf);
        delta->set_reasoning_content(collect.reasoning_buf);
        for (size_t i = 0; i < collect.pending.size(); ++i) {
            auto *tc = delta->add_tool_calls();
            tc->set_index(static_cast<int32_t>(i));
            tc->set_id(collect.pending[i].id);
            tc->set_name(collect.pending[i].name);
            tc->set_arguments(collect.pending[i].args);
        }

        reply->set_message(collect.raw_buf);
        reply->set_tokens(collect.tokens);
        reply->set_prompt_tokens(prompt_len);
        return Status::OK;
    }

    Status PredictStream(ServerContext *, const backend::PredictOptions *request,
                         ServerWriter<backend::Reply> *writer) override {
        std::lock_guard<std::mutex> lock(g_engine_mu);
        if (!g_engine || !g_session) {
            return Status(StatusCode::FAILED_PRECONDITION, "ds4: model not loaded");
        }
        ds4_tokens prompt = {};
        ds4_tokenize_text(g_engine, request->prompt().c_str(), &prompt);
        int n_predict = request->tokens() > 0 ? request->tokens() : 256;
        StreamCtx s = {g_engine, writer, 0, false};
        int rc = ds4_engine_generate_argmax(g_engine, &prompt, n_predict, g_ctx_size,
                                            stream_emit, stream_done, &s,
                                            nullptr, nullptr);
        ds4_tokens_free(&prompt);
        if (rc != 0 && !s.aborted) {
            return Status(StatusCode::INTERNAL,
                          "ds4_engine_generate_argmax rc=" + std::to_string(rc));
        }
        return Status::OK;
    }

    Status Status(ServerContext *, const backend::HealthMessage *,
                  backend::StatusResponse *response) override {
        std::lock_guard<std::mutex> lock(g_engine_mu);
        response->set_state(g_engine ? backend::StatusResponse::READY
                                     : backend::StatusResponse::UNINITIALIZED);
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
