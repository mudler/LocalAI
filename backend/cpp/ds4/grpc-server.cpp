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
// NOTE: do NOT alias `grpc::Status` as `Status` - the Status RPC method below
// would shadow the type, breaking the other RPC method declarations that use
// it as a return type. Use GStatus instead.
using GStatus = ::grpc::Status;
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
    ds4cpp::DsmlParser parser;
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

static void apply_events(CollectCtx *c, const std::vector<ds4cpp::ParserEvent> &events) {
    for (const auto &e : events) {
        switch (e.type) {
        case ds4cpp::ParserEvent::CONTENT:
            c->content_buf += e.text;
            break;
        case ds4cpp::ParserEvent::REASONING:
            c->reasoning_buf += e.text;
            break;
        case ds4cpp::ParserEvent::TOOL_START:
            if ((int)c->pending.size() <= e.index)
                c->pending.resize(e.index + 1);
            c->pending[e.index].id = e.tool_id;
            c->pending[e.index].name = e.tool_name;
            break;
        case ds4cpp::ParserEvent::TOOL_ARGS:
            if ((int)c->pending.size() > e.index)
                c->pending[e.index].args += e.text;
            break;
        case ds4cpp::ParserEvent::TOOL_END:
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
    std::vector<ds4cpp::ParserEvent> events;
    c->parser.Feed(chunk, events);
    apply_events(c, events);
    c->tokens++;
}
static void collect_done(void *) {}

struct StreamCtx {
    ds4_engine *engine;
    ServerWriter<backend::Reply> *writer;
    ds4cpp::DsmlParser parser;
    int tokens;
    bool aborted;
    // Track which tool indices we've seen TOOL_START for, so subsequent
    // ARGS deltas can elide the redundant id/name fields.
    std::vector<bool> tool_started;
};

static void stream_emit(void *ud, int token) {
    auto *s = static_cast<StreamCtx *>(ud);
    if (s->aborted) return;
    if (token == ds4_token_eos(s->engine)) return;
    size_t len = 0;
    const char *text = ds4_token_text(s->engine, token, &len);
    if (!text || len == 0) return;
    std::string chunk(text, len);
    std::vector<ds4cpp::ParserEvent> events;
    s->parser.Feed(chunk, events);
    if (events.empty()) { s->tokens++; return; }

    backend::Reply reply;
    auto *delta = reply.add_chat_deltas();
    bool any_field = false;
    for (const auto &e : events) {
        switch (e.type) {
        case ds4cpp::ParserEvent::CONTENT:
            delta->set_content(delta->content() + e.text);
            any_field = true;
            break;
        case ds4cpp::ParserEvent::REASONING:
            delta->set_reasoning_content(delta->reasoning_content() + e.text);
            any_field = true;
            break;
        case ds4cpp::ParserEvent::TOOL_START: {
            if ((int)s->tool_started.size() <= e.index)
                s->tool_started.resize(e.index + 1, false);
            s->tool_started[e.index] = true;
            auto *tc = delta->add_tool_calls();
            tc->set_index(e.index);
            tc->set_id(e.tool_id);
            tc->set_name(e.tool_name);
            any_field = true;
            break;
        }
        case ds4cpp::ParserEvent::TOOL_ARGS: {
            auto *tc = delta->add_tool_calls();
            tc->set_index(e.index);
            tc->set_arguments(e.text);
            any_field = true;
            break;
        }
        case ds4cpp::ParserEvent::TOOL_END:
            // No marker delta needed - the Go side closes the tool call on
            // the final aggregator pass.
            break;
        }
    }
    reply.set_message(chunk);
    reply.set_tokens(1);
    if (any_field) {
        if (!s->writer->Write(reply)) s->aborted = true;
    }
    s->tokens++;
}
static void stream_done(void *) {}

// Per-thread RNG seed for ds4_session_sample. Initialized lazily from
// system_clock; ds4 owns the random walk after that.
static uint64_t *get_rng() {
    static thread_local uint64_t seed = 0;
    if (seed == 0) {
        seed = static_cast<uint64_t>(
            std::chrono::system_clock::now().time_since_epoch().count());
        if (seed == 0) seed = 1;
    }
    return &seed;
}

struct SampleParams {
    float temperature;
    int top_k;
    float top_p;
    float min_p;
};

// Compute the effective sampling parameters for the next token, mirroring
// ds4_server.c:7102-7115:
//   - thinking mode enabled -> override (T=1, top_k=0, top_p=1, min_p=0)
//   - inside DSML structural position (tool-call markers) -> force T=0
//   - otherwise -> the request's user-supplied sampling settings
// The parser argument carries state from tokens emitted so far; its
// IsInDsmlStructural() predicts the next token's classification.
static SampleParams compute_sample_params(const backend::PredictOptions *request,
                                          const ds4cpp::DsmlParser &parser,
                                          bool think_enabled);

static ds4_think_mode parse_think_mode(const backend::PredictOptions *request) {
    // Per the vllm backend convention, "enable_thinking" gates thinking on/off,
    // and "reasoning_effort" picks the strength when on.
    const auto &md = request->metadata();
    auto et = md.find("enable_thinking");
    bool enabled = true; // default ON per ds4-server
    if (et != md.end()) enabled = (et->second == "true" || et->second == "1");
    if (!enabled) return DS4_THINK_NONE;
    auto re = md.find("reasoning_effort");
    if (re != md.end() && (re->second == "max" || re->second == "xhigh"))
        return DS4_THINK_MAX;
    return DS4_THINK_HIGH;
}

static SampleParams compute_sample_params(const backend::PredictOptions *request,
                                          const ds4cpp::DsmlParser &parser,
                                          bool think_enabled) {
    SampleParams p = {
        request->temperature(),
        request->topk(),
        request->topp(),
        request->minp(),
    };
    if (think_enabled) {
        // Match ds4-server: thinking mode wants creativity in the reasoning
        // pass and the trailing content, so the entire generation overrides
        // sampling unless DSML structural bytes take over below.
        p.temperature = 1.0f;
        p.top_k = 0;
        p.top_p = 1.0f;
        p.min_p = 0.0f;
    }
    if (parser.IsInDsmlStructural()) {
        // Tool-call structural bytes (tags, markers, headers) must parse
        // cleanly. Force greedy regardless of user/thinking settings.
        p.temperature = 0.0f;
    }
    return p;
}

// Build the rendered text for cache keying. We feed the same text the model
// will see; that lets the cache survive small client-side reformatting of
// chat history (the cache is keyed on bytes, not tokens).
static std::string render_prompt_text(const backend::PredictOptions *request) {
    // Two-mode: either the raw prompt or the chat-template path. We mirror
    // build_prompt's branching but accumulate text (not tokens) so we can
    // SHA1 it for the cache key. ds4_session caches a tokens-indexed
    // checkpoint, but the disk format keys on bytes per ds4-server's design.
    if (!request->usetokenizertemplate() || request->messages_size() == 0) {
        return request->prompt();
    }
    std::string out;
    const std::string sys_role = "system";
    for (const auto &m : request->messages()) {
        if (m.role() == sys_role) { out += "[sys] " + m.content() + "\n"; break; }
    }
    for (const auto &m : request->messages()) {
        if (m.role() == sys_role) continue;
        out += "[" + m.role() + "] " + m.content() + "\n";
    }
    return out;
}

ds4cpp::KvCache g_kv_cache;

// Try to recover prefill state for `rendered`. Returns the matched prefix length.
static size_t maybe_load_cache(const std::string &rendered) {
    if (!g_kv_cache.enabled() || !g_session) return 0;
    return g_kv_cache.LoadLongestPrefix(g_session, rendered, g_ctx_size);
}

static void maybe_save_cache(const std::string &rendered) {
    if (g_kv_cache.enabled() && g_session) {
        g_kv_cache.Save(g_session, rendered, g_ctx_size);
    }
}

static void build_prompt(ds4_engine *engine, const backend::PredictOptions *request,
                         ds4_tokens *out) {
    if (!request->usetokenizertemplate() || request->messages_size() == 0) {
        ds4_tokenize_text(engine, request->prompt().c_str(), out);
        return;
    }
    // Chat-template path: render via ds4's helpers.
    ds4_chat_begin(engine, out);

    ds4_think_mode think = parse_think_mode(request);

    // ds4_encode_chat_prompt is convenient when there is exactly one
    // system+user pair, but for arbitrary turn lists we use the granular
    // append helpers. Pull the first system message (if any), then append
    // every other message in order.
    const std::string sys_role = "system";
    std::string system_text;
    for (const auto &m : request->messages()) {
        if (m.role() == sys_role) { system_text = m.content(); break; }
    }
    // Inject the tools manifest into the system prompt when tools are present.
    // ds4 was trained to emit DSML tool calls ONLY when this preamble is in
    // the system message - without it, the model has no idea tools exist and
    // the e2e tool-call test will fail. The renderer lives in dsml_renderer
    // and is a verbatim port of ds4_server.c's append_tools_prompt_text.
    std::string tools_manifest;
    if (!request->tools().empty()) {
        tools_manifest = ds4cpp::RenderToolsManifest(request->tools());
    }
    if (!system_text.empty() || !tools_manifest.empty()) {
        std::string combined = system_text;
        if (!tools_manifest.empty()) {
            if (!combined.empty()) combined += "\n\n";
            combined += tools_manifest;
        }
        ds4_chat_append_message(engine, out, "system", combined.c_str());
    }
    for (const auto &m : request->messages()) {
        if (m.role() == sys_role) continue;
        if (m.role() == "assistant" && !m.tool_calls().empty()) {
            std::string combined = m.content();
            combined += ds4cpp::RenderAssistantToolCalls(m.tool_calls());
            ds4_chat_append_message(engine, out, "assistant", combined.c_str());
        } else if (m.role() == "tool") {
            std::string body = ds4cpp::RenderToolResult(m.tool_call_id(), m.content());
            ds4_chat_append_message(engine, out, "user", body.c_str());
        } else {
            ds4_chat_append_message(engine, out, m.role().c_str(), m.content().c_str());
        }
    }
    ds4_chat_append_assistant_prefix(engine, out, think);
}

class DS4Backend final : public backend::Backend::Service {
public:
    GStatus Health(ServerContext *, const backend::HealthMessage *,
                  backend::Reply *reply) override {
        reply->set_message(std::string("OK"));
        return GStatus::OK;
    }

    GStatus Free(ServerContext *, const backend::HealthMessage *,
                backend::Result *result) override {
        std::lock_guard<std::mutex> lock(g_engine_mu);
        if (g_session) { ds4_session_free(g_session); g_session = nullptr; }
        if (g_engine)  { ds4_engine_close(g_engine);  g_engine  = nullptr; }
        result->set_success(true);
        return GStatus::OK;
    }

    GStatus LoadModel(ServerContext *, const backend::ModelOptions *request,
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
            return GStatus::OK;
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

        g_kv_cache.SetDir(g_kv_cache_dir);

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
            return GStatus::OK;
        }

        g_ctx_size = request->contextsize() > 0 ? request->contextsize() : 32768;
        rc = ds4_session_create(&g_session, g_engine, g_ctx_size);
        if (rc != 0 || !g_session) {
            ds4_engine_close(g_engine);
            g_engine = nullptr;
            result->set_success(false);
            result->set_message("ds4_session_create failed (rc=" + std::to_string(rc) + ")");
            return GStatus::OK;
        }

        result->set_success(true);
        result->set_message("loaded " + model_path);
        return GStatus::OK;
    }

    GStatus TokenizeString(ServerContext *, const backend::PredictOptions *request,
                          backend::TokenizationResponse *response) override {
        std::lock_guard<std::mutex> lock(g_engine_mu);
        if (!g_engine) return GStatus(StatusCode::FAILED_PRECONDITION, "ds4: model not loaded");
        ds4_tokens out = {};
        ds4_tokenize_text(g_engine, request->prompt().c_str(), &out);
        for (int i = 0; i < out.len; ++i) response->add_tokens(out.v[i]);
        response->set_length(out.len);
        ds4_tokens_free(&out);
        return GStatus::OK;
    }

    GStatus Predict(ServerContext *, const backend::PredictOptions *request,
                   backend::Reply *reply) override {
        std::lock_guard<std::mutex> lock(g_engine_mu);
        if (!g_engine || !g_session) {
            return GStatus(StatusCode::FAILED_PRECONDITION, "ds4: model not loaded");
        }
        ds4_tokens prompt = {};
        build_prompt(g_engine, request, &prompt);
        int n_predict = request->tokens() > 0 ? request->tokens() : 256;

        CollectCtx collect = {g_engine, "", {}, reply, 0, {}, "", ""};
        std::string cache_key = render_prompt_text(request);
        size_t cache_hit = maybe_load_cache(cache_key);
        (void)cache_hit; // future: skip prompt prefix if hit covers full prompt

        // Manual generation loop on g_session. When MTP speculative weights
        // were loaded (LoadModel option 'mtp_path:'), we use the
        // ds4_session_eval_speculative_argmax path which may accept N>1
        // tokens per outer iteration. Otherwise per-token argmax + eval.
        // Either way g_session advances so the disk KV cache picks up a
        // real checkpoint after the call (see maybe_save_cache below).
        char err[256] = {0};
        int rc = ds4_session_sync(g_session, &prompt, err, sizeof(err));
        int prompt_len = prompt.len;
        ds4_tokens_free(&prompt);
        if (rc == 0) {
            const int eos = ds4_token_eos(g_engine);
            const int draft_max = ds4_engine_mtp_draft_tokens(g_engine);
            const bool think_enabled = ds4_think_mode_enabled(parse_think_mode(request));
            int produced = 0;
            while (produced < n_predict) {
                SampleParams sp = compute_sample_params(request, collect.parser, think_enabled);
                int first;
                if (sp.temperature <= 0.0f) {
                    first = ds4_session_argmax(g_session);
                } else {
                    first = ds4_session_sample(g_session,
                                               sp.temperature, sp.top_k,
                                               sp.top_p, sp.min_p, get_rng());
                }
                if (first == eos) break;
                // MTP only when sampling is greedy (ds4-server gate).
                if (draft_max > 0 && sp.temperature <= 0.0f) {
                    constexpr int kAcceptedMax = 8;
                    int accepted[kAcceptedMax];
                    int cap = std::min(kAcceptedMax, draft_max + 1);
                    int n = ds4_session_eval_speculative_argmax(
                        g_session, first, draft_max, eos,
                        accepted, cap, err, sizeof(err));
                    if (n < 0) { rc = -1; break; }
                    bool stop = false;
                    for (int j = 0; j < n; ++j) {
                        if (accepted[j] == eos) { stop = true; break; }
                        collect_emit(&collect, accepted[j]);
                        if (++produced >= n_predict) { stop = true; break; }
                    }
                    if (stop) break;
                } else {
                    collect_emit(&collect, first);
                    if (++produced >= n_predict) break;
                    rc = ds4_session_eval(g_session, first, err, sizeof(err));
                    if (rc != 0) break;
                }
            }
            collect_done(&collect);
        }
        maybe_save_cache(cache_key);

        // Flush any buffered parser state.
        std::vector<ds4cpp::ParserEvent> events;
        collect.parser.Flush(events);
        apply_events(&collect, events);

        if (rc != 0) {
            return GStatus(StatusCode::INTERNAL,
                          std::string("ds4 generation failed: ") + err);
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
        return GStatus::OK;
    }

    GStatus PredictStream(ServerContext *, const backend::PredictOptions *request,
                         ServerWriter<backend::Reply> *writer) override {
        std::lock_guard<std::mutex> lock(g_engine_mu);
        if (!g_engine || !g_session) {
            return GStatus(StatusCode::FAILED_PRECONDITION, "ds4: model not loaded");
        }
        ds4_tokens prompt = {};
        build_prompt(g_engine, request, &prompt);
        int n_predict = request->tokens() > 0 ? request->tokens() : 256;

        StreamCtx s = {g_engine, writer, {}, 0, false, {}};
        std::string cache_key = render_prompt_text(request);
        size_t cache_hit = maybe_load_cache(cache_key);
        (void)cache_hit;

        // Manual loop on g_session - see Predict() above for the rationale.
        // MTP speculative path used when ds4_engine_mtp_draft_tokens > 0.
        char err[256] = {0};
        int rc = ds4_session_sync(g_session, &prompt, err, sizeof(err));
        ds4_tokens_free(&prompt);
        if (rc == 0) {
            const int eos = ds4_token_eos(g_engine);
            const int draft_max = ds4_engine_mtp_draft_tokens(g_engine);
            const bool think_enabled = ds4_think_mode_enabled(parse_think_mode(request));
            int produced = 0;
            while (produced < n_predict && !s.aborted) {
                SampleParams sp = compute_sample_params(request, s.parser, think_enabled);
                int first;
                if (sp.temperature <= 0.0f) {
                    first = ds4_session_argmax(g_session);
                } else {
                    first = ds4_session_sample(g_session,
                                               sp.temperature, sp.top_k,
                                               sp.top_p, sp.min_p, get_rng());
                }
                if (first == eos) break;
                if (draft_max > 0 && sp.temperature <= 0.0f) {
                    constexpr int kAcceptedMax = 8;
                    int accepted[kAcceptedMax];
                    int cap = std::min(kAcceptedMax, draft_max + 1);
                    int n = ds4_session_eval_speculative_argmax(
                        g_session, first, draft_max, eos,
                        accepted, cap, err, sizeof(err));
                    if (n < 0) { rc = -1; break; }
                    bool stop = false;
                    for (int j = 0; j < n; ++j) {
                        if (accepted[j] == eos) { stop = true; break; }
                        stream_emit(&s, accepted[j]);
                        if (s.aborted) { stop = true; break; }
                        if (++produced >= n_predict) { stop = true; break; }
                    }
                    if (stop) break;
                } else {
                    stream_emit(&s, first);
                    if (s.aborted || ++produced >= n_predict) break;
                    rc = ds4_session_eval(g_session, first, err, sizeof(err));
                    if (rc != 0) break;
                }
            }
            stream_done(&s);
        }
        maybe_save_cache(cache_key);

        // Flush parser state.
        std::vector<ds4cpp::ParserEvent> events;
        s.parser.Flush(events);
        if (!events.empty() && !s.aborted) {
            backend::Reply reply;
            auto *delta = reply.add_chat_deltas();
            for (const auto &e : events) {
                if (e.type == ds4cpp::ParserEvent::CONTENT) {
                    delta->set_content(delta->content() + e.text);
                } else if (e.type == ds4cpp::ParserEvent::REASONING) {
                    delta->set_reasoning_content(delta->reasoning_content() + e.text);
                }
            }
            s.writer->Write(reply);
        }

        if (rc != 0 && !s.aborted) {
            return GStatus(StatusCode::INTERNAL,
                          std::string("ds4 generation failed: ") + err);
        }
        return GStatus::OK;
    }

    GStatus Status(ServerContext *, const backend::HealthMessage *,
                  backend::StatusResponse *response) override {
        std::lock_guard<std::mutex> lock(g_engine_mu);
        response->set_state(g_engine ? backend::StatusResponse::READY
                                     : backend::StatusResponse::UNINITIALIZED);
        return GStatus::OK;
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
