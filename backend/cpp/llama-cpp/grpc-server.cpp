// llama.cpp gRPC C++ backend server
//
// Ettore Di Giacinto <mudler@localai.io> and llama.cpp authors
//
// This is a gRPC server for llama.cpp compatible with the LocalAI proto
// Note: this is a re-adaptation of the original llama.cpp example/server.cpp for HTTP (https://github.com/ggerganov/llama.cpp/tree/master/examples/server),
// but modified to work with gRPC
//

#include "server-task.cpp"
#include "server-queue.cpp"
#include "server-common.cpp"
// server-chat.cpp exists only in llama.cpp after the upstream refactor that
// split OAI/Anthropic/Responses/transcription conversion helpers out of
// server-common.cpp. When present, server-context.cpp and server-task.cpp
// above call into it, so we must pull its definitions into this TU or the
// link fails. __has_include keeps the source compatible with older pins.
#if __has_include("server-chat.cpp")
#include "server-chat.cpp"
#endif
// server-schema.cpp exists only in llama.cpp after the upstream refactor that
// extracted the JSON request-schema evaluation (previously the static
// server_task::params_from_json_cmpl) into server_schema::eval_llama_cmpl_schema.
// server-context.cpp and grpc-server.cpp both call into it, so its definitions
// must be part of this translation unit or the link fails. __has_include keeps
// the source compatible with older pins/forks (e.g. llama-cpp-turboquant) that
// predate the split and still expose params_from_json_cmpl (see the guarded
// call sites below).
#if __has_include("server-schema.cpp")
#define LOCALAI_HAS_SERVER_SCHEMA 1
#include "server-schema.cpp"
#endif
// server-stream.cpp exists only in llama.cpp after the upstream refactor that
// added the SSE stream-resumption layer (stream_session/stream_pipe_producer).
// server-context.cpp calls into it (spipe->cleanup(), stream_aware_should_stop,
// stream_session_attach_pipe), so its definitions must be part of this
// translation unit or the link fails with "undefined reference to
// stream_pipe_producer::cleanup()". The file is self-contained (its only
// external symbols come from server-common, already pulled in above) and the
// http route-handler factories it also defines are unused here but harmless.
// __has_include keeps the source compatible with older pins/forks that predate
// the split.
#if __has_include("server-stream.cpp")
#include "server-stream.cpp"
#endif
#include "server-context.cpp"

// LocalAI

#include "backend.pb.h"
#include "backend.grpc.pb.h"
#include "common.h"
#include "arg.h"
#include "chat-auto-parser.h"
#include "message_content.h"
#include <getopt.h>
#include <grpcpp/ext/proto_server_reflection_plugin.h>
#include <grpcpp/grpcpp.h>
#include <grpcpp/health_check_service_interface.h>
#include <grpcpp/security/server_credentials.h>
#include <regex>
#include <algorithm>
#include <atomic>
#include <cmath>
#include <cstdlib>
#include <fstream>
#include <iterator>
#include <list>
#include <map>
#include <mutex>
#include <signal.h>
#include <thread>

#if defined(_WIN32)
#include <windows.h>
#endif

#include "parent_watch.h" // best-effort parent-death backstop (see header)


using grpc::Server;
using grpc::ServerBuilder;
using grpc::ServerContext;
using grpc::Status;

// gRPC bearer token auth for distributed mode.
// Reads LOCALAI_GRPC_AUTH_TOKEN from the environment. When set, rejects
// requests without a matching "authorization: Bearer <token>" metadata header.

// Cached auth token — empty means auth is disabled.
static std::string g_grpc_auth_token;

// Minimal constant-time comparison (avoids OpenSSL dependency)
static int ct_memcmp(const void* a, const void* b, size_t n) {
    const unsigned char* pa = static_cast<const unsigned char*>(a);
    const unsigned char* pb = static_cast<const unsigned char*>(b);
    unsigned char result = 0;
    for (size_t i = 0; i < n; i++) {
        result |= pa[i] ^ pb[i];
    }
    return result;
}

// Returns OK when auth is disabled or the token matches.
static grpc::Status checkAuth(grpc::ServerContext* context) {
    if (g_grpc_auth_token.empty()) {
        return grpc::Status::OK;
    }
    auto metadata = context->client_metadata();
    auto it = metadata.find("authorization");
    if (it != metadata.end()) {
        std::string expected = "Bearer " + g_grpc_auth_token;
        std::string got(it->second.data(), it->second.size());
        if (expected.size() == got.size() &&
            ct_memcmp(expected.data(), got.data(), expected.size()) == 0) {
            return grpc::Status::OK;
        }
    }
    return grpc::Status(grpc::StatusCode::UNAUTHENTICATED, "invalid token");
}

// Minimal base64 encoder. The C++ backend already pulls in base64_decode from
// llama.cpp's server-common.cpp, but no encoder is exposed — and we need one to
// hand audio bytes to the existing PredictOptions.audios path (which expects
// base64-encoded strings, just like images).
static std::string base64_encode_bytes(const unsigned char* data, size_t len) {
    static const char tbl[] =
        "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    std::string out;
    out.reserve(((len + 2) / 3) * 4);
    for (size_t i = 0; i < len; i += 3) {
        uint32_t triple = (uint32_t(data[i]) << 16);
        if (i + 1 < len) triple |= (uint32_t(data[i + 1]) << 8);
        if (i + 2 < len) triple |= uint32_t(data[i + 2]);
        out.push_back(tbl[(triple >> 18) & 0x3F]);
        out.push_back(tbl[(triple >> 12) & 0x3F]);
        out.push_back(i + 1 < len ? tbl[(triple >> 6) & 0x3F] : '=');
        out.push_back(i + 2 < len ? tbl[triple & 0x3F]        : '=');
    }
    return out;
}

// END LocalAI


/////////////////////////////////
////////////////////////////////
//////// LOCALAI code starts below here
/////////////////////////////////
////////////////////////////////

bool loaded_model; // TODO: add a mutex for this, but happens only once loading the model

// Score bypasses the slot loop (see the comment on Score below) so it
// must not run concurrently with any slot-loop RPC. These counters
// are a defence-in-depth tripwire — ModelConfig.Validate already
// rejects llama-cpp configs that mix score with chat/completion/
// embeddings, so a healthy deployment never trips them. seq_cst is
// load-bearing for the increment-then-check pattern below.
static std::atomic<int> slot_loop_inflight{0};
static std::atomic<int> score_inflight{0};

// Increment-then-check, not check-then-increment: two simultaneous
// racers both observe the other's increment and both abort cleanly.
// Reversed, both could see zero and proceed.
struct conflict_guard {
    std::atomic<int>& self;
    conflict_guard(const char* rpc, std::atomic<int>& self_, std::atomic<int>& other, const char* other_name)
        : self(self_) {
        self.fetch_add(1, std::memory_order_seq_cst);
        int o = other.load(std::memory_order_seq_cst);
        if (o > 0) {
            fprintf(stderr,
                "FATAL: %s called with %s=%d. The llama-cpp backend cannot "
                "service Score and slot-loop RPCs concurrently — Score "
                "bypasses the slot loop and races the llama_context. Bind "
                "Score-using features to a model dedicated to scoring "
                "(known_usecases: [score] with no chat/completion/embeddings).\n",
                rpc, other_name, o);
            std::abort();
        }
    }
    ~conflict_guard() {
        self.fetch_sub(1, std::memory_order_seq_cst);
    }
};

static std::function<void(int)> shutdown_handler;
static std::atomic_flag is_terminating = ATOMIC_FLAG_INIT;

static inline void signal_handler(int signal) {
    if (is_terminating.test_and_set()) {
        // in case it hangs, we can force terminate the server by hitting Ctrl+C twice
        // this is for better developer experience, we can remove when the server is stable enough
        fprintf(stderr, "Received second interrupt, terminating immediately.\n");
        exit(1);
    }

    shutdown_handler(signal);
}

// Forward declarations
static void start_llama_server(server_context& ctx_server);
static json parse_options(bool streaming, const backend::PredictOptions* predict, const common_params& params_base, llama_context* ctx);
static ggml_type kv_cache_type_from_str(const std::string & s);
static std::string get_all_kv_cache_types();
static void add_rpc_devices(std::string servers);
static void params_parse(server_context& ctx_server, const backend::ModelOptions* request, common_params & params);

static void start_llama_server(server_context& ctx_server) {

    LOG_INF("%s: starting llama server\n", __func__);

    LOG_INF("%s: waiting for model to be loaded\n", __func__);
    // Wait for model to be loaded first
    while (!loaded_model) {
        std::this_thread::sleep_for(std::chrono::milliseconds(100));
    }

    LOG_INF("%s: model loaded\n", __func__);

    // print sample chat example to make it clear which template is used
    // LOG_INF("%s: chat template, chat_template: %s, example_format: '%s'\n", __func__,
    //     common_chat_templates_source(ctx_server.impl->chat_params.tmpls.get()),
    //     common_chat_format_example(ctx_server.impl->chat_params.tmpls.get(), ctx_server.impl->params_base.use_jinja).c_str(), ctx_server.impl->params_base.default_template_kwargs);

    // Keep the chat templates initialized in load_model() so they can be used when UseTokenizerTemplate is enabled
    // Templates will only be used conditionally in Predict/PredictStream when UseTokenizerTemplate is true and Messages are provided

    shutdown_handler = [&](int) {
        // this will unblock start_loop()
        ctx_server.terminate();
    };

    // TODO: refactor in common/console
#if defined (__unix__) || (defined (__APPLE__) && defined (__MACH__))
    struct sigaction sigint_action;
    sigint_action.sa_handler = signal_handler;
    sigemptyset (&sigint_action.sa_mask);
    sigint_action.sa_flags = 0;
    sigaction(SIGINT, &sigint_action, NULL);
    sigaction(SIGTERM, &sigint_action, NULL);
#elif defined (_WIN32)
    auto console_ctrl_handler = +[](DWORD ctrl_type) -> BOOL {
        return (ctrl_type == CTRL_C_EVENT) ? (signal_handler(SIGINT), true) : false;
    };
    SetConsoleCtrlHandler(reinterpret_cast<PHANDLER_ROUTINE>(console_ctrl_handler), true);
#endif

    // this call blocks the main thread until ctx_server.terminate() is called
    ctx_server.start_loop();
}

json parse_options(bool streaming, const backend::PredictOptions* predict, const common_params& params_base, llama_context* ctx)
{

    // Create now a json data from the prediction options instead
    //
    json data;
    data["stream"] = streaming;
    data["cache_prompt"] = predict->promptcacheall();
    data["n_predict"] = predict->tokens() == 0 ? -1 : predict->tokens();
    data["top_k"] = predict->topk();
    data["top_p"] = predict->topp();
    data["typical_p"] = predict->typicalp();
    data["temperature"] = predict->temperature();
    data["repeat_last_n"] = predict->repeat();
    data["repeat_penalty"] = predict->penalty();
    data["frequency_penalty"] = predict->frequencypenalty();
    data["presence_penalty"] = predict->presencepenalty();
    data["mirostat"] = predict->mirostat();
    data["mirostat_tau"] = predict->mirostattau();
    data["mirostat_eta"] = predict->mirostateta();
    data["n_keep"] = predict->nkeep();
    data["seed"] = predict->seed();
    data["min_p"] = predict->minp();


    std::string grammar_str = predict->grammar();



    if (!grammar_str.empty()) {
        data["grammar"] = grammar_str;
        SRV_INF("Using grammar: %s\n", grammar_str.c_str());
    }

    // Only set prompt if UseTokenizerTemplate is false or if no Messages are provided
    // When UseTokenizerTemplate is true and Messages are provided, prompt will be set via chat templates in Predict/PredictStream
    if (!predict->usetokenizertemplate() || predict->messages_size() == 0) {
        data["prompt"] = predict->prompt();
    }

    // Extract tools and tool_choice from proto and add to data JSON
    SRV_INF("[TOOLS DEBUG] parse_options: Checking for tools in proto, tools().empty()=%d, tools().size()=%zu\n",
            predict->tools().empty() ? 1 : 0, predict->tools().size());
    if (!predict->tools().empty()) {
        SRV_INF("[TOOLS DEBUG] parse_options: Tools string from proto (first 500 chars): %s\n",
                predict->tools().substr(0, std::min<size_t>(500, predict->tools().size())).c_str());
        try {
            // Parse tools JSON string and add to data
            json tools_json = json::parse(predict->tools());
            data["tools"] = tools_json;
            SRV_INF("Extracted tools from proto: %s\n", predict->tools().c_str());
            // Debug: Log tools count and names
            if (tools_json.is_array()) {
                SRV_INF("[TOOLS DEBUG] parse_options: Successfully parsed %zu tools from Go layer\n", tools_json.size());
                for (size_t i = 0; i < tools_json.size(); i++) {
                    if (tools_json[i].contains("function") && tools_json[i]["function"].contains("name")) {
                        SRV_INF("[TOOLS DEBUG] parse_options: Tool %zu: %s\n", i, tools_json[i]["function"]["name"].get<std::string>().c_str());
                    } else if (tools_json[i].contains("name")) {
                        SRV_INF("[TOOLS DEBUG] parse_options: Tool %zu: %s\n", i, tools_json[i]["name"].get<std::string>().c_str());
                    }
                }
            } else {
                SRV_WRN("[TOOLS DEBUG] parse_options: Parsed tools JSON is not an array: %s\n", tools_json.dump().c_str());
            }
        } catch (const json::parse_error& e) {
            SRV_WRN("Failed to parse tools JSON from proto: %s\n", e.what());
            SRV_WRN("[TOOLS DEBUG] parse_options: Tools string that failed to parse: %s\n", predict->tools().c_str());
        }
    } else {
        SRV_INF("%s", "[TOOLS DEBUG] parse_options: No tools received from Go layer (predict->tools() is empty)\n");
    }

    // Debug: Verify tools are in data after extraction
    if (data.contains("tools")) {
        SRV_INF("[TOOLS DEBUG] parse_options: Tools successfully added to data, count: %zu\n",
                data["tools"].is_array() ? data["tools"].size() : 0);
    } else {
        SRV_INF("%s", "[TOOLS DEBUG] parse_options: WARNING - Tools NOT in data after extraction!\n");
    }
    if (!predict->toolchoice().empty()) {
        try {
            // Parse tool_choice JSON string
            json tool_choice_json = json::parse(predict->toolchoice());
            // tool_choice can be a string ("auto", "none", "required") or an object
            // Store it as-is (string or object) so we can convert object to "required" later when adding to body_json
            if (tool_choice_json.is_string()) {
                data["tool_choice"] = tool_choice_json.get<std::string>();
                SRV_DBG("[TOOLS DEBUG] Received tool_choice from Go layer: %s\n", tool_choice_json.get<std::string>().c_str());
            } else {
                // Store object as-is so we can detect it later and convert to "required"
                data["tool_choice"] = tool_choice_json;
                SRV_DBG("[TOOLS DEBUG] Received tool_choice object from Go layer: %s\n", tool_choice_json.dump().c_str());
            }
            SRV_INF("Extracted tool_choice from proto: %s\n", predict->toolchoice().c_str());
        } catch (const json::parse_error& e) {
            // If parsing fails, treat as string
            data["tool_choice"] = predict->toolchoice();
            SRV_INF("Extracted tool_choice as string: %s\n", predict->toolchoice().c_str());
        }
    }

    // Extract logprobs and top_logprobs from proto and add to JSON data
    // Following server.cpp pattern: logprobs maps to n_probs when provided
    if (predict->logprobs() > 0) {
        data["logprobs"] = predict->logprobs();
        // Map logprobs to n_probs (following server.cpp line 369 pattern)
        // n_probs will be set by params_from_json_cmpl if logprobs is provided
        data["n_probs"] = predict->logprobs();
        SRV_INF("Using logprobs: %d\n", predict->logprobs());
    }
    if (predict->toplogprobs() > 0) {
        data["top_logprobs"] = predict->toplogprobs();
        SRV_INF("Using top_logprobs: %d\n", predict->toplogprobs());
    }

    // Extract logit_bias from proto and add to JSON data
    if (!predict->logitbias().empty()) {
        try {
            // Parse logit_bias JSON string from proto
            json logit_bias_json = json::parse(predict->logitbias());
            // Add to data - llama.cpp server expects it as an object (map)
            data["logit_bias"] = logit_bias_json;
            SRV_INF("Using logit_bias: %s\n", predict->logitbias().c_str());
        } catch (const json::parse_error& e) {
            SRV_ERR("Failed to parse logit_bias JSON from proto: %s\n", e.what());
        }
    }

    data["ignore_eos"] = predict->ignoreeos();
    data["embeddings"] = predict->embeddings();

    // Speculative decoding per-request overrides
    // NDraft maps to speculative.n_max (maximum draft tokens per speculation step)
    if (predict->ndraft() > 0) {
        data["speculative.n_max"] = predict->ndraft();
    }

    // Add the correlationid to json data
    data["correlation_id"] = predict->correlationid();

    // for each image in the request, add the image data
    //
    for (int i = 0; i < predict->images_size(); i++) {
        data["image_data"].push_back(json
            {
                {"id", i},
                {"data",    predict->images(i)},
            });
    }

    // for each audio in the request, add the audio data
    for (int i = 0; i < predict->audios_size(); i++) {
        data["audio_data"].push_back(json
            {
                {"id", i},
                {"data",    predict->audios(i)},
            });
    }

    // for each video in the request, add the video data
    for (int i = 0; i < predict->videos_size(); i++) {
        data["video_data"].push_back(json
            {
                {"id", i},
                {"data",    predict->videos(i)},
            });
    }

    data["stop"] = predict->stopprompts();
    // data["n_probs"] = predict->nprobs();
    //TODO: images,

    // Serialize grammar triggers from server context to JSON array
    if (!params_base.sampling.grammar_triggers.empty()) {
        json grammar_triggers = json::array();
        for (const auto& trigger : params_base.sampling.grammar_triggers) {
            json trigger_json;
            trigger_json["value"] = trigger.value;
            // Always serialize as WORD type since upstream converts WORD to TOKEN internally
            trigger_json["type"] = static_cast<int>(COMMON_GRAMMAR_TRIGGER_TYPE_WORD);
            grammar_triggers.push_back(trigger_json);
        }
        data["grammar_triggers"] = grammar_triggers;
    }

    // Serialize preserved tokens from server context to JSON array
    if (!params_base.sampling.preserved_tokens.empty()) {
        json preserved_tokens = json::array();
        for (const auto& token : params_base.sampling.preserved_tokens) {
            preserved_tokens.push_back(common_token_to_piece(ctx, token));
        }
        data["preserved_tokens"] = preserved_tokens;
    }

    return data;
}


const std::vector<ggml_type> kv_cache_types = {
    GGML_TYPE_F32,
    GGML_TYPE_F16,
    GGML_TYPE_BF16,
    GGML_TYPE_Q8_0,
    GGML_TYPE_Q4_0,
    GGML_TYPE_Q4_1,
    GGML_TYPE_IQ4_NL,
    GGML_TYPE_Q5_0,
    GGML_TYPE_Q5_1,
};

static ggml_type kv_cache_type_from_str(const std::string & s) {
    for (const auto & type : kv_cache_types) {
        if (ggml_type_name(type) == s) {
            return type;
        }
    }
    throw std::runtime_error("Unsupported cache type: " + s);
}

static std::string get_all_kv_cache_types() {
    std::ostringstream msg;
    for (const auto & type : kv_cache_types) {
        msg << ggml_type_name(type) << (&type == &kv_cache_types.back() ? "" : ", ");
    }
    return msg.str();
}

// Adds an RPC server
// Description here: https://github.com/ggml-org/llama.cpp/blob/master/tools/rpc/README.md
static void add_rpc_devices(std::string servers) {
    auto rpc_servers = string_split<std::string>(servers, ',');
    // Trim whitespace to allow more flexible configurations, such as having entries on separate lines.
    for (std::string & server : rpc_servers)
    {
        server.erase(0, server.find_first_not_of(" \t\n\r"));
        server.erase(server.find_last_not_of(" \t\n\r") + 1);
    }
    if (rpc_servers.empty()) {
        throw std::invalid_argument("no RPC servers specified");
    }
    ggml_backend_reg_t rpc_reg = ggml_backend_reg_by_name("RPC");
    if (!rpc_reg) {
        throw std::invalid_argument("failed to find RPC backend");
    }
    typedef ggml_backend_reg_t (*ggml_backend_rpc_add_server_t)(const char * endpoint);
    ggml_backend_rpc_add_server_t ggml_backend_rpc_add_server_fn = (ggml_backend_rpc_add_server_t) ggml_backend_reg_get_proc_address(rpc_reg, "ggml_backend_rpc_add_server");
    if (!ggml_backend_rpc_add_server_fn) {
        throw std::invalid_argument("failed to find RPC add server function");
    }
    for (const auto & server : rpc_servers) {
        ggml_backend_reg_t reg = ggml_backend_rpc_add_server_fn(server.c_str());
        ggml_backend_register(reg);
    }
}

static void params_parse(server_context& /*ctx_server*/, const backend::ModelOptions* request,
                                common_params & params) {

    // this is comparable to: https://github.com/ggerganov/llama.cpp/blob/d9b33fe95bd257b36c84ee5769cc048230067d6f/examples/server/server.cpp#L1809

    params.model.path = request->modelfile();
    if (!request->mmproj().empty()) {
      params.mmproj.path = request->mmproj();
    }

    // Draft model for speculative decoding
    if (!request->draftmodel().empty()) {
        params.speculative.draft.mparams.path = request->draftmodel();
        // Default to draft type if a draft model is set but no explicit type.
        // Upstream made the speculative type a vector (ggml-org/llama.cpp#22838)
        // and renamed COMMON_SPECULATIVE_TYPE_DRAFT -> ..._DRAFT_SIMPLE (#22964).
        const bool no_spec_type = params.speculative.types.empty() ||
            (params.speculative.types.size() == 1 && params.speculative.types[0] == COMMON_SPECULATIVE_TYPE_NONE);
        if (no_spec_type) {
            params.speculative.types = { COMMON_SPECULATIVE_TYPE_DRAFT_SIMPLE };
        }
    }

    //  params.model_alias ??
    params.model_alias.insert(request->modelfile());
    if (!request->cachetypekey().empty()) {
        params.cache_type_k = kv_cache_type_from_str(request->cachetypekey());
    }
    if (!request->cachetypevalue().empty()) {
        params.cache_type_v = kv_cache_type_from_str(request->cachetypevalue());
    }
    params.n_ctx = request->contextsize();
    //params.memory_f16 = request->f16memory();
    params.cpuparams.n_threads = request->threads();
    params.n_gpu_layers = request->ngpulayers();
    params.n_batch = request->nbatch();
    //params.verbosity = INT_MAX;
    // Enable all debug logs by setting verbosity threshold to maximum
    //common_log_set_verbosity_thold(INT_MAX);
    params.n_ubatch = request->nbatch(); // fixes issue with reranking models being limited to 512 tokens (the default n_ubatch size); allows for setting the maximum input amount of tokens thereby avoiding this error "input is too large to process. increase the physical batch size"

    // Initialize ctx_shift to false by default (can be overridden by options)
    params.ctx_shift = false;
    // Initialize cache_ram_mib to -1 by default (no limit, can be overridden by options)
    params.cache_ram_mib = -1;
    // Initialize n_parallel to 1 by default (can be overridden by options)
    params.n_parallel = 1;
    // Initialize grpc_servers to empty (can be overridden by options)
    std::string grpc_servers_option = "";

    // Initialize fit_params options (can be overridden by options)
    // fit_params: whether to auto-adjust params to fit device memory (default: true as in llama.cpp)
    params.fit_params = true;
    // fit_params_target: target margin per device in bytes (default: 1GB per device)
    // Initialize as vector with default value for all devices
    params.fit_params_target = std::vector<size_t>(llama_max_devices(), 1024 * 1024 * 1024);
    // fit_params_min_ctx: minimum context size for fit (default: 4096)
    params.fit_params_min_ctx = 4096;

    // Initialize additional server options (can be overridden by options)
    // n_cache_reuse: min chunk size for KV cache reuse via shifting (default: 0 = disabled)
    params.n_cache_reuse = 0;
    // slot_prompt_similarity: threshold for slot prompt matching (default: 0.1)
    params.slot_prompt_similarity = 0.1f;
    // swa_full: use full-size SWA cache (default: false)
    params.swa_full = false;
    // cont_batching: continuous batching (default: true, auto-enabled when n_parallel > 1)
    params.cont_batching = true;
    // check_tensors: validate tensor data (default: false)
    params.check_tensors = false;
    // warmup: enable warmup run (default: true)
    params.warmup = true;
    // no_op_offload: disable host tensor op offload (default: false)
    params.no_op_offload = false;
    // kv_unified: enable unified KV cache. Upstream's server auto-enables this
    // when the slot count is auto (-np <0), bumping n_parallel to 4 alongside.
    // LocalAI keeps n_parallel=1 by default, which would skip that auto path
    // and leave kv_unified=false. We flip the default to true here so the
    // server-side prompt cache (cache_idle_slots) is actually usable on the
    // single-slot path that LocalAI ships with: without it, idle slots are
    // never persisted across requests and the prompt cache is dead weight.
    // Users can opt out with `options: [ "kv_unified:false" ]`.
    params.kv_unified = true;
    // n_ctx_checkpoints: max context checkpoints per slot. Match upstream's
    // default (32); the previous LocalAI-specific 8 was unnecessarily tight
    // and limits partial-prefix recovery without a clear memory rationale.
    params.n_ctx_checkpoints = 32;
    // cache_idle_slots: save and clear idle slot KV to the prompt cache on
    // task switch. Upstream default is true; the server auto-disables it if
    // kv_unified=false or cache_ram_mib=0, so flipping kv_unified above is
    // what actually unlocks it.
    params.cache_idle_slots = true;
    // checkpoint_min_step: minimum spacing between context checkpoints in
    // tokens (0 disables the minimum). Match upstream's default (256). This
    // field was renamed from `checkpoint_every_nt` in llama.cpp; the semantics
    // also shifted from a fixed cadence to a minimum spacing. The turboquant
    // fork still lacks common_params::checkpoint_min_step, so skip it there
    // (LOCALAI_TURBOQUANT_NO_CHECKPOINT_MIN_STEP is injected by
    // backend/cpp/turboquant/patch-grpc-server.sh).
#ifndef LOCALAI_TURBOQUANT_NO_CHECKPOINT_MIN_STEP
    params.checkpoint_min_step = 256;
#endif

    // Raw upstream llama-server flags collected from any option entry that
    // starts with '-'. Applied once after the loop via common_params_parse.
    std::vector<std::string> extra_argv;

    auto add_device_options = [&](const std::string & devices) {
        const std::regex regex{ R"([,]+)" };
        std::sregex_token_iterator it{ devices.begin(), devices.end(), regex, -1 };
        std::vector<std::string> split_arg{ it, {} };

        for (std::string device : split_arg) {
            const auto start = device.find_first_not_of(" \t\n\r");
            if (start == std::string::npos) {
                continue;
            }
            const auto end = device.find_last_not_of(" \t\n\r");
            device = device.substr(start, end - start + 1);

            extra_argv.push_back("--device");
            extra_argv.push_back(device);
        }
    };

     // decode options. Options are in form optname:optvale, or if booleans only optname.
    for (int i = 0; i < request->options_size(); i++) {
        std::string opt = request->options(i);
        std::vector<char> opt_buf(opt.begin(), opt.end());
        opt_buf.push_back('\0');
        char *optname = strtok(opt_buf.data(), ":");
        char *optval = strtok(NULL, ":");
        std::string optval_str = (optval == NULL) ? "true" : optval;

        if (!strcmp(optname, "context_shift")) {
            if (optval_str == "true" || optval_str == "1" || optval_str == "yes" || optval_str == "on" || optval_str == "enabled") {
                params.ctx_shift = true;
            } else if (optval_str == "false" || optval_str == "0" || optval_str == "no" || optval_str == "off" || optval_str == "disabled") {
                params.ctx_shift = false;
            }
        } else if (!strcmp(optname, "use_jinja") || !strcmp(optname, "jinja")) {
            if (optval_str == "true" || optval_str == "1" || optval_str == "yes" || optval_str == "on" || optval_str == "enabled") {
                params.use_jinja = true;
            } else if (optval_str == "false" || optval_str == "0" || optval_str == "no" || optval_str == "off" || optval_str == "disabled") {
                params.use_jinja = false;
            }
        } else if (!strcmp(optname, "cache_ram")) {
            if (optval != NULL) {
                try {
                    params.cache_ram_mib = std::stoi(optval_str);
                } catch (const std::exception& e) {
                    // If conversion fails, keep default value (-1)
                }
            }
        } else if (!strcmp(optname, "parallel") || !strcmp(optname, "n_parallel")) {
            if (optval != NULL) {
                try {
                    params.n_parallel = std::stoi(optval_str);
                    if (params.n_parallel > 1) {
                        params.cont_batching = true;
                    }
                } catch (const std::exception& e) {
                    // If conversion fails, keep default value (1)
                }
            }
        } else if (!strcmp(optname, "grpc_servers") || !strcmp(optname, "rpc_servers")) {
            if (optval != NULL) {
                grpc_servers_option = optval_str;
            }
        } else if (!strcmp(optname, "fit_params") || !strcmp(optname, "fit")) {
            if (optval_str == "true" || optval_str == "1" || optval_str == "yes" || optval_str == "on" || optval_str == "enabled") {
                params.fit_params = true;
            } else if (optval_str == "false" || optval_str == "0" || optval_str == "no" || optval_str == "off" || optval_str == "disabled") {
                params.fit_params = false;
            }
        } else if (!strcmp(optname, "fit_params_target") || !strcmp(optname, "fit_target")) {
            if (optval != NULL) {
                try {
                    // Value is in MiB, can be comma-separated list for multiple devices
                    // Single value is broadcast across all devices
                    std::string arg_next = optval_str;
                    const std::regex regex{ R"([,/]+)" };
                    std::sregex_token_iterator it{ arg_next.begin(), arg_next.end(), regex, -1 };
                    std::vector<std::string> split_arg{ it, {} };
                    if (split_arg.size() >= llama_max_devices()) {
                        // Too many values provided
                        continue;
                    }
                    if (split_arg.size() == 1) {
                        // Single value: broadcast to all devices
                        size_t value_mib = std::stoul(split_arg[0]);
                        std::fill(params.fit_params_target.begin(), params.fit_params_target.end(), value_mib * 1024 * 1024);
                    } else {
                        // Multiple values: set per device
                        for (size_t i = 0; i < split_arg.size() && i < params.fit_params_target.size(); i++) {
                            params.fit_params_target[i] = std::stoul(split_arg[i]) * 1024 * 1024;
                        }
                    }
                } catch (const std::exception& e) {
                    // If conversion fails, keep default value (1GB per device)
                }
            }
        } else if (!strcmp(optname, "fit_params_min_ctx") || !strcmp(optname, "fit_ctx")) {
            if (optval != NULL) {
                try {
                    params.fit_params_min_ctx = std::stoi(optval_str);
                } catch (const std::exception& e) {
                    // If conversion fails, keep default value (4096)
                }
            }
        } else if (!strcmp(optname, "n_cache_reuse") || !strcmp(optname, "cache_reuse")) {
            if (optval != NULL) {
                try {
                    params.n_cache_reuse = std::stoi(optval_str);
                } catch (const std::exception& e) {
                    // If conversion fails, keep default value (0)
                }
            }
        } else if (!strcmp(optname, "slot_prompt_similarity") || !strcmp(optname, "sps")) {
            if (optval != NULL) {
                try {
                    params.slot_prompt_similarity = std::stof(optval_str);
                } catch (const std::exception& e) {
                    // If conversion fails, keep default value (0.1)
                }
            }
        } else if (!strcmp(optname, "swa_full")) {
            if (optval_str == "true" || optval_str == "1" || optval_str == "yes" || optval_str == "on" || optval_str == "enabled") {
                params.swa_full = true;
            } else if (optval_str == "false" || optval_str == "0" || optval_str == "no" || optval_str == "off" || optval_str == "disabled") {
                params.swa_full = false;
            }
        } else if (!strcmp(optname, "cont_batching") || !strcmp(optname, "continuous_batching")) {
            if (optval_str == "true" || optval_str == "1" || optval_str == "yes" || optval_str == "on" || optval_str == "enabled") {
                params.cont_batching = true;
            } else if (optval_str == "false" || optval_str == "0" || optval_str == "no" || optval_str == "off" || optval_str == "disabled") {
                params.cont_batching = false;
            }
        } else if (!strcmp(optname, "check_tensors")) {
            if (optval_str == "true" || optval_str == "1" || optval_str == "yes" || optval_str == "on" || optval_str == "enabled") {
                params.check_tensors = true;
            } else if (optval_str == "false" || optval_str == "0" || optval_str == "no" || optval_str == "off" || optval_str == "disabled") {
                params.check_tensors = false;
            }
        } else if (!strcmp(optname, "warmup")) {
            if (optval_str == "true" || optval_str == "1" || optval_str == "yes" || optval_str == "on" || optval_str == "enabled") {
                params.warmup = true;
            } else if (optval_str == "false" || optval_str == "0" || optval_str == "no" || optval_str == "off" || optval_str == "disabled") {
                params.warmup = false;
            }
        } else if (!strcmp(optname, "no_op_offload")) {
            if (optval_str == "true" || optval_str == "1" || optval_str == "yes" || optval_str == "on" || optval_str == "enabled") {
                params.no_op_offload = true;
            } else if (optval_str == "false" || optval_str == "0" || optval_str == "no" || optval_str == "off" || optval_str == "disabled") {
                params.no_op_offload = false;
            }
        } else if (!strcmp(optname, "device") || !strcmp(optname, "devices")) {
            if (optval != NULL) {
                add_device_options(optval_str);
            }
        } else if (!strcmp(optname, "split_mode") || !strcmp(optname, "sm")) {
            // Accepts: none | layer | row | tensor (the latter requires a llama.cpp build
            // that includes ggml-org/llama.cpp#19378, FlashAttention enabled, and KV-cache
            // quantization disabled).
            if (optval != NULL) {
                if (optval_str == "none") {
                    params.split_mode = LLAMA_SPLIT_MODE_NONE;
                } else if (optval_str == "layer") {
                    params.split_mode = LLAMA_SPLIT_MODE_LAYER;
                } else if (optval_str == "row") {
                    params.split_mode = LLAMA_SPLIT_MODE_ROW;
                } else if (optval_str == "tensor") {
                    params.split_mode = LLAMA_SPLIT_MODE_TENSOR;
                }
            }
        } else if (!strcmp(optname, "kv_unified") || !strcmp(optname, "unified_kv")) {
            if (optval_str == "true" || optval_str == "1" || optval_str == "yes" || optval_str == "on" || optval_str == "enabled") {
                params.kv_unified = true;
            } else if (optval_str == "false" || optval_str == "0" || optval_str == "no" || optval_str == "off" || optval_str == "disabled") {
                params.kv_unified = false;
            }
        } else if (!strcmp(optname, "n_ctx_checkpoints") || !strcmp(optname, "ctx_checkpoints")) {
            if (optval != NULL) {
                try {
                    params.n_ctx_checkpoints = std::stoi(optval_str);
                } catch (const std::exception& e) {
                    // If conversion fails, keep default value (32)
                }
            }

        // --- server-side idle-slot prompt cache toggle (upstream --cache-idle-slots) ---
        // Saves the slot's KV state into the host-side prompt cache on task
        // switch so a later request with the same prefix can warm-load it.
        // Auto-disabled by the server if kv_unified=false or cache_ram=0.
        } else if (!strcmp(optname, "cache_idle_slots") || !strcmp(optname, "idle_slots_cache")) {
            if (optval_str == "true" || optval_str == "1" || optval_str == "yes" || optval_str == "on" || optval_str == "enabled") {
                params.cache_idle_slots = true;
            } else if (optval_str == "false" || optval_str == "0" || optval_str == "no" || optval_str == "off" || optval_str == "disabled") {
                params.cache_idle_slots = false;
            }

#ifndef LOCALAI_TURBOQUANT_NO_CHECKPOINT_MIN_STEP
        // --- minimum context-checkpoint spacing (upstream -cms / --checkpoint-min-step) ---
        // 0 disables the minimum-spacing gate. Old option names (`checkpoint_every_nt`,
        // `checkpoint_every_n_tokens`) are kept as aliases for backward compatibility
        // with existing user configs: upstream renamed the field and shifted its
        // semantics from a fixed cadence to a minimum spacing.
        //
        // Gated out for the turboquant fork, which lacks common_params::
        // checkpoint_min_step. The leading `}` closing the cache_idle_slots
        // branch is removed with this block; the next `} else if` (n_ubatch)
        // then closes cache_idle_slots, so braces stay balanced under both
        // preprocessor branches.
        } else if (!strcmp(optname, "checkpoint_min_step") || !strcmp(optname, "checkpoint_min_spacing") ||
                   !strcmp(optname, "checkpoint_every_nt") || !strcmp(optname, "checkpoint_every_n_tokens")) {
            if (optval != NULL) {
                try {
                    params.checkpoint_min_step = std::stoi(optval_str);
                } catch (const std::exception& e) {
                    // If conversion fails, keep default value (256)
                }
            }
#endif

        // --- physical batch size (upstream -ub / --ubatch-size) ---
        // Note: line ~482 already aliases n_ubatch to n_batch as a default; this
        // option lets users decouple the two (useful for embeddings/rerank).
        } else if (!strcmp(optname, "n_ubatch") || !strcmp(optname, "ubatch")) {
            if (optval != NULL) {
                try { params.n_ubatch = std::stoi(optval_str); } catch (...) {}
            }

        // --- main-model batch threads (upstream -tb / --threads-batch) ---
        } else if (!strcmp(optname, "threads_batch") || !strcmp(optname, "n_threads_batch")) {
            if (optval != NULL) {
                try {
                    int n = std::stoi(optval_str);
                    if (n <= 0) n = (int)std::thread::hardware_concurrency();
                    params.cpuparams_batch.n_threads = n;
                } catch (...) {}
            }

        // --- pooling type for embeddings (upstream --pooling) ---
        } else if (!strcmp(optname, "pooling_type") || !strcmp(optname, "pooling")) {
            if (optval != NULL) {
                if      (optval_str == "none") params.pooling_type = LLAMA_POOLING_TYPE_NONE;
                else if (optval_str == "mean") params.pooling_type = LLAMA_POOLING_TYPE_MEAN;
                else if (optval_str == "cls")  params.pooling_type = LLAMA_POOLING_TYPE_CLS;
                else if (optval_str == "last") params.pooling_type = LLAMA_POOLING_TYPE_LAST;
                else if (optval_str == "rank") params.pooling_type = LLAMA_POOLING_TYPE_RANK;
                // unknown values silently leave UNSPECIFIED (auto-detect)
            }

        // --- llama log verbosity threshold (upstream -lv / --verbosity) ---
        } else if (!strcmp(optname, "verbosity")) {
            if (optval != NULL) {
                try { params.verbosity = std::stoi(optval_str); } catch (...) {}
            }

        // --- O_DIRECT model loading (upstream --direct-io) ---
        } else if (!strcmp(optname, "direct_io") || !strcmp(optname, "use_direct_io")) {
            if (optval_str == "true" || optval_str == "1" || optval_str == "yes" || optval_str == "on" || optval_str == "enabled") {
                params.use_direct_io = true;
            } else if (optval_str == "false" || optval_str == "0" || optval_str == "no" || optval_str == "off" || optval_str == "disabled") {
                params.use_direct_io = false;
            }

        // --- embedding normalization (upstream --embd-normalize) ---
        // -1 none, 0 max-abs, 1 taxicab, 2 L2 (default), >2 p-norm
        } else if (!strcmp(optname, "embd_normalize") || !strcmp(optname, "embedding_normalize")) {
            if (optval != NULL) {
                try { params.embd_normalize = std::stoi(optval_str); } catch (...) {}
            }

        // --- reasoning parser (upstream --reasoning-format) ---
        // Picks the parser for <think> blocks emitted by reasoning models.
        // none / auto / deepseek / deepseek-legacy
        } else if (!strcmp(optname, "reasoning_format")) {
            if (optval != NULL) {
                if      (optval_str == "none")             params.reasoning_format = COMMON_REASONING_FORMAT_NONE;
                else if (optval_str == "auto")             params.reasoning_format = COMMON_REASONING_FORMAT_AUTO;
                else if (optval_str == "deepseek")         params.reasoning_format = COMMON_REASONING_FORMAT_DEEPSEEK;
                else if (optval_str == "deepseek-legacy" || optval_str == "deepseek_legacy")
                                                            params.reasoning_format = COMMON_REASONING_FORMAT_DEEPSEEK_LEGACY;
                // unknown values silently keep the upstream default (DEEPSEEK)
            }

        // --- reasoning budget (upstream --reasoning-budget) ---
        // -1 unlimited, 0 disabled, >0 token budget for thinking blocks.
        // Distinct from per-request `enable_thinking` (chat_template_kwargs).
        } else if (!strcmp(optname, "enable_reasoning") || !strcmp(optname, "reasoning_budget")) {
            if (optval != NULL) {
                try { params.enable_reasoning = std::stoi(optval_str); } catch (...) {}
            }

        // --- prefill assistant turn (upstream --no-prefill-assistant) ---
        } else if (!strcmp(optname, "prefill_assistant")) {
            if (optval_str == "true" || optval_str == "1" || optval_str == "yes" || optval_str == "on" || optval_str == "enabled") {
                params.prefill_assistant = true;
            } else if (optval_str == "false" || optval_str == "0" || optval_str == "no" || optval_str == "off" || optval_str == "disabled") {
                params.prefill_assistant = false;
            }

        // --- mmproj GPU offload (upstream --no-mmproj-offload, inverted) ---
        } else if (!strcmp(optname, "mmproj_use_gpu") || !strcmp(optname, "mmproj_offload")) {
            if (optval_str == "true" || optval_str == "1" || optval_str == "yes" || optval_str == "on" || optval_str == "enabled") {
                params.mmproj_use_gpu = true;
            } else if (optval_str == "false" || optval_str == "0" || optval_str == "no" || optval_str == "off" || optval_str == "disabled") {
                params.mmproj_use_gpu = false;
            }

        // --- per-image vision token budget (upstream --image-min/max-tokens) ---
        } else if (!strcmp(optname, "image_min_tokens")) {
            if (optval != NULL) {
                try { params.image_min_tokens = std::stoi(optval_str); } catch (...) {}
            }
        } else if (!strcmp(optname, "image_max_tokens")) {
            if (optval != NULL) {
                try { params.image_max_tokens = std::stoi(optval_str); } catch (...) {}
            }

        // --- main-model tensor buffer overrides (upstream --override-tensor) ---
        // Format: <tensor regex>=<buffer type>,<tensor regex>=<buffer type>,...
        // Mirrors the existing `draft_override_tensor` parser below.
        } else if (!strcmp(optname, "override_tensor") || !strcmp(optname, "tensor_buft_overrides")) {
            ggml_backend_load_all();
            std::map<std::string, ggml_backend_buffer_type_t> buft_list;
            for (size_t i = 0; i < ggml_backend_dev_count(); ++i) {
                auto * dev = ggml_backend_dev_get(i);
                auto * buft = ggml_backend_dev_buffer_type(dev);
                if (buft) {
                    buft_list[ggml_backend_buft_name(buft)] = buft;
                }
            }
            static std::list<std::string> override_names;
            std::string cur;
            auto flush = [&](const std::string & spec) {
                auto pos = spec.find('=');
                if (pos == std::string::npos) return;
                const std::string name = spec.substr(0, pos);
                const std::string type = spec.substr(pos + 1);
                auto it = buft_list.find(type);
                if (it == buft_list.end()) return; // unknown buffer type: ignore
                override_names.push_back(name);
                params.tensor_buft_overrides.push_back(
                    {override_names.back().c_str(), it->second});
            };
            for (char c : optval_str) {
                if (c == ',') { if (!cur.empty()) { flush(cur); cur.clear(); } }
                else { cur.push_back(c); }
            }
            if (!cur.empty()) flush(cur);

        // Speculative decoding options
        } else if (!strcmp(optname, "spec_type") || !strcmp(optname, "speculative_type")) {
            // Upstream switched to a vector of types (comma-separated for multi-type
            // chaining via common_speculative_types_from_names). We keep accepting a
            // single value here, but also tolerate comma-separated lists.
            //
            // ggml-org/llama.cpp#22964 also renamed the registered names from
            // underscore- to dash-separated form, and replaced the bare
            // `draft`/`eagle3` aliases with `draft-simple`/`draft-eagle3`. We
            // normalize each token here so existing model configs keep working.
            auto normalize_spec_name = [](std::string s) -> std::string {
                std::replace(s.begin(), s.end(), '_', '-');
                if (s == "draft")  return "draft-simple";
                if (s == "eagle3") return "draft-eagle3";
                return s;
            };
            std::vector<std::string> names;
            std::string item;
            for (char c : optval_str) {
                if (c == ',') {
                    if (!item.empty()) { names.push_back(normalize_spec_name(item)); item.clear(); }
                } else {
                    item.push_back(c);
                }
            }
            if (!item.empty()) names.push_back(normalize_spec_name(item));
            auto parsed = common_speculative_types_from_names(names);
            if (!parsed.empty()) {
                params.speculative.types = parsed;
            }
        } else if (!strcmp(optname, "spec_n_max") || !strcmp(optname, "draft_max")) {
            if (optval != NULL) {
                try { params.speculative.draft.n_max = std::stoi(optval_str); } catch (...) {}
            }
        } else if (!strcmp(optname, "spec_n_min") || !strcmp(optname, "draft_min")) {
            if (optval != NULL) {
                try { params.speculative.draft.n_min = std::stoi(optval_str); } catch (...) {}
            }
        } else if (!strcmp(optname, "spec_p_min") || !strcmp(optname, "draft_p_min")) {
            if (optval != NULL) {
                try { params.speculative.draft.p_min = std::stof(optval_str); } catch (...) {}
            }
        } else if (!strcmp(optname, "spec_p_split")) {
            if (optval != NULL) {
                try { params.speculative.draft.p_split = std::stof(optval_str); } catch (...) {}
            }
        } else if (!strcmp(optname, "spec_ngram_size_n") || !strcmp(optname, "ngram_size_n")) {
            if (optval != NULL) {
                try { params.speculative.ngram_simple.size_n = (uint16_t)std::stoi(optval_str); } catch (...) {}
            }
        } else if (!strcmp(optname, "spec_ngram_size_m") || !strcmp(optname, "ngram_size_m")) {
            if (optval != NULL) {
                try { params.speculative.ngram_simple.size_m = (uint16_t)std::stoi(optval_str); } catch (...) {}
            }
        } else if (!strcmp(optname, "spec_ngram_min_hits") || !strcmp(optname, "ngram_min_hits")) {
            if (optval != NULL) {
                try { params.speculative.ngram_simple.min_hits = (uint16_t)std::stoi(optval_str); } catch (...) {}
            }
        } else if (!strcmp(optname, "draft_gpu_layers")) {
            if (optval != NULL) {
                try { params.speculative.draft.n_gpu_layers = std::stoi(optval_str); } catch (...) {}
            }
        } else if (!strcmp(optname, "draft_ctx_size")) {
            // The draft context size is no longer a separate field upstream: the draft
            // shares the target context size. Accept the option for backward
            // compatibility but silently ignore it.

        // --- ngram_mod family (upstream --spec-ngram-mod-*) ---
        } else if (!strcmp(optname, "spec_ngram_mod_n_min")) {
            if (optval != NULL) {
                try { params.speculative.ngram_mod.n_min = std::stoi(optval_str); } catch (...) {}
            }
        } else if (!strcmp(optname, "spec_ngram_mod_n_max")) {
            if (optval != NULL) {
                try { params.speculative.ngram_mod.n_max = std::stoi(optval_str); } catch (...) {}
            }
        } else if (!strcmp(optname, "spec_ngram_mod_n_match")) {
            if (optval != NULL) {
                try { params.speculative.ngram_mod.n_match = std::stoi(optval_str); } catch (...) {}
            }

        // --- ngram_map_k family (upstream --spec-ngram-map-k-*) ---
        } else if (!strcmp(optname, "spec_ngram_map_k_size_n")) {
            if (optval != NULL) {
                try { params.speculative.ngram_map_k.size_n = (uint16_t)std::stoi(optval_str); } catch (...) {}
            }
        } else if (!strcmp(optname, "spec_ngram_map_k_size_m")) {
            if (optval != NULL) {
                try { params.speculative.ngram_map_k.size_m = (uint16_t)std::stoi(optval_str); } catch (...) {}
            }
        } else if (!strcmp(optname, "spec_ngram_map_k_min_hits")) {
            if (optval != NULL) {
                try { params.speculative.ngram_map_k.min_hits = (uint16_t)std::stoi(optval_str); } catch (...) {}
            }

        // --- ngram_map_k4v family (upstream --spec-ngram-map-k4v-*) ---
        } else if (!strcmp(optname, "spec_ngram_map_k4v_size_n")) {
            if (optval != NULL) {
                try { params.speculative.ngram_map_k4v.size_n = (uint16_t)std::stoi(optval_str); } catch (...) {}
            }
        } else if (!strcmp(optname, "spec_ngram_map_k4v_size_m")) {
            if (optval != NULL) {
                try { params.speculative.ngram_map_k4v.size_m = (uint16_t)std::stoi(optval_str); } catch (...) {}
            }
        } else if (!strcmp(optname, "spec_ngram_map_k4v_min_hits")) {
            if (optval != NULL) {
                try { params.speculative.ngram_map_k4v.min_hits = (uint16_t)std::stoi(optval_str); } catch (...) {}
            }

        // --- ngram lookup caches (upstream --lookup-cache-static / -dynamic) ---
        } else if (!strcmp(optname, "spec_lookup_cache_static") || !strcmp(optname, "lookup_cache_static")) {
            params.speculative.ngram_cache.lookup_cache_static = optval_str;
        } else if (!strcmp(optname, "spec_lookup_cache_dynamic") || !strcmp(optname, "lookup_cache_dynamic")) {
            params.speculative.ngram_cache.lookup_cache_dynamic = optval_str;

        // --- draft model KV cache types (upstream --spec-draft-type-k / -v) ---
        } else if (!strcmp(optname, "draft_cache_type_k") || !strcmp(optname, "spec_draft_cache_type_k")) {
            params.speculative.draft.cache_type_k = kv_cache_type_from_str(optval_str);
        } else if (!strcmp(optname, "draft_cache_type_v") || !strcmp(optname, "spec_draft_cache_type_v")) {
            params.speculative.draft.cache_type_v = kv_cache_type_from_str(optval_str);

        // --- draft model thread counts (upstream --spec-draft-threads / -batch) ---
        } else if (!strcmp(optname, "draft_threads") || !strcmp(optname, "spec_draft_threads")) {
            if (optval != NULL) {
                try {
                    int n = std::stoi(optval_str);
                    if (n <= 0) n = (int)std::thread::hardware_concurrency();
                    params.speculative.draft.cpuparams.n_threads = n;
                } catch (...) {}
            }
        } else if (!strcmp(optname, "draft_threads_batch") || !strcmp(optname, "spec_draft_threads_batch")) {
            if (optval != NULL) {
                try {
                    int n = std::stoi(optval_str);
                    if (n <= 0) n = (int)std::thread::hardware_concurrency();
                    params.speculative.draft.cpuparams_batch.n_threads = n;
                } catch (...) {}
            }

        // --- draft model MoE on CPU (upstream --spec-draft-cpu-moe / --spec-draft-n-cpu-moe) ---
        } else if (!strcmp(optname, "draft_cpu_moe") || !strcmp(optname, "spec_draft_cpu_moe")) {
            // Bool-style flag: optval may be missing, "true"/"1"/"yes" enables.
            const bool enable = (optval == NULL) ||
                optval_str == "true" || optval_str == "1" || optval_str == "yes" ||
                optval_str == "on" || optval_str == "enabled";
            if (enable) {
                params.speculative.draft.tensor_buft_overrides.push_back(llm_ffn_exps_cpu_override());
            }
        } else if (!strcmp(optname, "draft_n_cpu_moe") || !strcmp(optname, "spec_draft_n_cpu_moe")) {
            if (optval != NULL) {
                try {
                    int n = std::stoi(optval_str);
                    if (n < 0) n = 0;
                    // Keep override-name storage alive for the lifetime of the params struct
                    // (mirrors upstream arg.cpp behavior with a function-local static).
                    static std::list<std::string> buft_overrides_draft;
                    for (int i = 0; i < n; ++i) {
                        buft_overrides_draft.push_back(llm_ffn_exps_block_regex(i));
                        params.speculative.draft.tensor_buft_overrides.push_back(
                            {buft_overrides_draft.back().c_str(), ggml_backend_cpu_buffer_type()});
                    }
                } catch (...) {}
            }

        // --- main model MoE on CPU (upstream --cpu-moe / --n-cpu-moe) ---
        } else if (!strcmp(optname, "cpu_moe")) {
            // Bool-style flag: keep all MoE expert weights on CPU.
            const bool enable = (optval == NULL) ||
                optval_str == "true" || optval_str == "1" || optval_str == "yes" ||
                optval_str == "on" || optval_str == "enabled";
            if (enable) {
                params.tensor_buft_overrides.push_back(llm_ffn_exps_cpu_override());
            }
        } else if (!strcmp(optname, "n_cpu_moe")) {
            if (optval != NULL) {
                try {
                    int n = std::stoi(optval_str);
                    if (n < 0) n = 0;
                    // Keep override-name storage alive for the lifetime of the
                    // params struct (mirrors upstream arg.cpp's function-local static).
                    static std::list<std::string> buft_overrides_main;
                    for (int i = 0; i < n; ++i) {
                        buft_overrides_main.push_back(llm_ffn_exps_block_regex(i));
                        params.tensor_buft_overrides.push_back(
                            {buft_overrides_main.back().c_str(), ggml_backend_cpu_buffer_type()});
                    }
                } catch (...) {}
            }

        // --- draft model tensor buffer overrides (upstream --spec-draft-override-tensor) ---
        } else if (!strcmp(optname, "draft_override_tensor") || !strcmp(optname, "spec_draft_override_tensor")) {
            // Format: <tensor regex>=<buffer type>,<tensor regex>=<buffer type>,...
            // We replicate upstream's parse_tensor_buffer_overrides (static in arg.cpp).
            ggml_backend_load_all();
            std::map<std::string, ggml_backend_buffer_type_t> buft_list;
            for (size_t i = 0; i < ggml_backend_dev_count(); ++i) {
                auto * dev = ggml_backend_dev_get(i);
                auto * buft = ggml_backend_dev_buffer_type(dev);
                if (buft) {
                    buft_list[ggml_backend_buft_name(buft)] = buft;
                }
            }
            static std::list<std::string> draft_override_names;
            std::string cur;
            auto flush = [&](const std::string & spec) {
                auto pos = spec.find('=');
                if (pos == std::string::npos) return;
                const std::string name = spec.substr(0, pos);
                const std::string type = spec.substr(pos + 1);
                auto it = buft_list.find(type);
                if (it == buft_list.end()) return; // unknown buffer type: ignore
                draft_override_names.push_back(name);
                params.speculative.draft.tensor_buft_overrides.push_back(
                    {draft_override_names.back().c_str(), it->second});
            };
            for (char c : optval_str) {
                if (c == ',') { if (!cur.empty()) { flush(cur); cur.clear(); } }
                else { cur.push_back(c); }
            }
            if (!cur.empty()) flush(cur);

        // --- generic passthrough: any entry starting with '-' is a raw
        //     upstream llama-server flag, forwarded verbatim to the parser. ---
        } else if (optname[0] == '-') {
            std::string flag = optname;
            // These flags make upstream's parser exit() (printing usage /
            // completion), which would kill the backend process. Skip them.
            if (flag == "-h" || flag == "--help" || flag == "--usage" ||
                flag == "--version" || flag == "--license" ||
                flag == "--list-devices" || flag == "-cl" ||
                flag == "--cache-list" ||
                flag.rfind("--completion", 0) == 0) {
                fprintf(stderr,
                    "[llama-cpp] ignoring passthrough flag that would exit: %s\n",
                    flag.c_str());
            } else {
                extra_argv.push_back(flag);
                // Preserve the whole value after the first ':' so embedded
                // colons (e.g. host:port) survive strtok's truncation of optval.
                auto colon = opt.find(':');
                if (colon != std::string::npos) {
                    extra_argv.push_back(opt.substr(colon + 1));
                }
            }
        }
    }

    // Set params.n_parallel from environment variable if not set via options (fallback)
    if (params.n_parallel == 1) {
        const char *env_parallel = std::getenv("LLAMACPP_PARALLEL");
        if (env_parallel != NULL) {
            try {
                params.n_parallel = std::stoi(env_parallel);
                if (params.n_parallel > 1) {
                    params.cont_batching = true;
                }
            } catch (const std::exception& e) {
                // If conversion fails, keep default value (1)
            }
        }
    }

    // Add RPC devices from option or environment variable (fallback)
    if (!grpc_servers_option.empty()) {
        add_rpc_devices(grpc_servers_option);
    } else {
        const char *llama_grpc_servers = std::getenv("LLAMACPP_GRPC_SERVERS");
        if (llama_grpc_servers != NULL) {
            add_rpc_devices(std::string(llama_grpc_servers));
        }
    }

    // Add kv_overrides
    if (request->overrides_size() > 0) {
        for (int i = 0; i < request->overrides_size(); i++) {
            string_parse_kv_override(request->overrides(i).c_str(), params.kv_overrides);
        }
    }

    // TODO: Add yarn

    if (!request->tensorsplit().empty()) {
        std::string arg_next = request->tensorsplit();

        // split string by , and /
        const std::regex regex{ R"([,/]+)" };
        std::sregex_token_iterator it{ arg_next.begin(), arg_next.end(), regex, -1 };
        std::vector<std::string> split_arg{ it, {} };

        GGML_ASSERT(split_arg.size() <= llama_max_devices());

        for (size_t i_device = 0; i_device < llama_max_devices(); ++i_device) {
            if (i_device < split_arg.size()) {
                params.tensor_split[i_device] = std::stof(split_arg[i_device]);
            }
            else {
                params.tensor_split[i_device] = 0.0f;
            }
        }
    }

    if (!request->maingpu().empty()) {
        params.main_gpu = std::stoi(request->maingpu());
    }
    if (!request->loraadapter().empty() && !request->lorabase().empty()) {
     float scale_factor = 1.0f;
     if (request->lorascale() != 0.0f) {
        scale_factor = request->lorascale();
     }
     // get the directory of modelfile
     std::string model_dir = params.model.path.substr(0, params.model.path.find_last_of("/\\"));
     common_adapter_lora_info lora_info;
     lora_info.path = model_dir + "/" + request->loraadapter();
     lora_info.scale = scale_factor;
     lora_info.task_name = "";
     lora_info.prompt_prefix = "";
     lora_info.ptr = nullptr;
     params.lora_adapters.push_back(std::move(lora_info));
    }
    params.use_mlock = request->mlock();
    params.use_mmap = request->mmap();

    if (request->flashattention() == "on" || request->flashattention() == "enabled") {
        params.flash_attn_type = LLAMA_FLASH_ATTN_TYPE_ENABLED;
    } else if (request->flashattention() == "off" || request->flashattention() == "disabled") {
        params.flash_attn_type = LLAMA_FLASH_ATTN_TYPE_DISABLED;
    } else if (request->flashattention() == "auto") {
        params.flash_attn_type = LLAMA_FLASH_ATTN_TYPE_AUTO;
    }

    params.no_kv_offload = request->nokvoffload();
    params.embedding = request->embeddings() || request->reranking();
    if (request->reranking()) {
        params.pooling_type = LLAMA_POOLING_TYPE_RANK;
    }


    if (request->ropescaling() == "none")   { params.rope_scaling_type = LLAMA_ROPE_SCALING_TYPE_NONE; }
    else if (request->ropescaling() == "yarn")   { params.rope_scaling_type = LLAMA_ROPE_SCALING_TYPE_YARN; }
    else if (request->ropescaling() == "linear")   {  params.rope_scaling_type = LLAMA_ROPE_SCALING_TYPE_LINEAR; }

    if ( request->yarnextfactor() != 0.0f ) {
        params.yarn_ext_factor = request->yarnextfactor();
    }
    if ( request->yarnattnfactor() != 0.0f ) {
        params.yarn_attn_factor = request->yarnattnfactor();
    }
    if ( request->yarnbetafast() != 0.0f ) {
        params.yarn_beta_fast = request->yarnbetafast();
    }
    if ( request->yarnbetaslow() != 0.0f ) {
        params.yarn_beta_slow = request->yarnbetaslow();
    }
    if ( request->ropefreqbase() != 0.0f ) {
        params.rope_freq_base = request->ropefreqbase();
    }
    if ( request->ropefreqscale() != 0.0f ) {
        params.rope_freq_scale = request->ropefreqscale();
    }

    if (request->grammartriggers_size() > 0) {
        //params.sampling.grammar_lazy = true;
        // Store grammar trigger words for processing after model is loaded
        for (int i = 0; i < request->grammartriggers_size(); i++) {
            const auto & word = request->grammartriggers(i).word();
            common_grammar_trigger trigger;
            trigger.type = COMMON_GRAMMAR_TRIGGER_TYPE_WORD;
            trigger.value = word;
            params.sampling.grammar_triggers.push_back(std::move(trigger));
        }
    }

    // Apply any raw upstream flags last so an explicit passthrough flag wins
    // over the LocalAI-resolved field it maps to (e.g. --ctx-size beats
    // context_size). This is the same parser llama-server itself uses.
    if (!extra_argv.empty()) {
        // common_params_parser_init resets a few fields for the SERVER example
        // (n_parallel -> -1, use_color). Snapshot n_parallel so an unrelated
        // passthrough flag can't silently clobber LocalAI's resolved value.
        const int saved_n_parallel = params.n_parallel;

        std::vector<char *> argv;
        std::string prog = "llama-server";
        argv.push_back(prog.data());
        for (auto & a : extra_argv) {
            argv.push_back(a.data());
        }

        // ctx_arg.params is a reference, so this overlays the given flags onto
        // `params` in place. Returns false on a recoverable parse error (and
        // self-restores params); may exit() on a hard error, exactly as
        // passing the same bad flag to llama-server would.
        if (!common_params_parse((int)argv.size(), argv.data(), params,
                                 LLAMA_EXAMPLE_SERVER)) {
            fprintf(stderr,
                "[llama-cpp] failed to parse passthrough options; ignoring them\n");
        }

        // Restore n_parallel unless a passthrough flag explicitly set it
        // (parser_init's reset sentinel for SERVER is -1).
        if (params.n_parallel == -1) {
            params.n_parallel = saved_n_parallel;
        }
    }

    // Terminate/pad the override vectors only after BOTH the named-option loop
    // and the generic passthrough (common_params_parse above) have pushed their
    // real entries, so back() is the null sentinel the model loader asserts on.
    // Running these before the passthrough let a passthrough flag (--cpu-moe,
    // --override-tensor, --override-kv, ...) append a real entry after the
    // sentinel: a GGML_ASSERT crash for tensor_buft_overrides, a silent drop for
    // kv_overrides. Double-termination is harmless (the while is a no-op if the
    // passthrough parse already padded; an extra trailing null is ignored).

    if (!params.kv_overrides.empty()) {
        params.kv_overrides.emplace_back();
        params.kv_overrides.back().key[0] = 0;
    }

    // tensor_buft_overrides sentinel termination (mirrors upstream common/arg.cpp).
    // Real entries are pushed during option parsing; here we pad/terminate so the
    // model loader sees back().pattern == nullptr (GGML_ASSERT at common.cpp:1543)
    // and so llama_params_fit has the placeholder slots it requires.
    {
        const size_t ntbo = llama_max_tensor_buft_overrides();
        while (params.tensor_buft_overrides.size() < ntbo) {
            params.tensor_buft_overrides.push_back({nullptr, nullptr});
        }
    }
    // Terminate the draft tensor_buft_overrides list with a sentinel, mirroring
    // the main-model handling above.
    if (!params.speculative.draft.tensor_buft_overrides.empty()) {
        params.speculative.draft.tensor_buft_overrides.push_back({nullptr, nullptr});
    }
}


// GRPC Server start
class BackendServiceImpl final : public backend::Backend::Service {
private:
    server_context& ctx_server;
    common_params params_base; // Store copy of params_base, set after model load

public:
    BackendServiceImpl(server_context& ctx) : ctx_server(ctx) {}

    grpc::Status Health(ServerContext* context, const backend::HealthMessage* /*request*/, backend::Reply* reply) override {
        auto auth = checkAuth(context);
        if (!auth.ok()) return auth;
        // Implement Health RPC
        reply->set_message("OK");
        return Status::OK;
    }

    grpc::Status LoadModel(ServerContext* context, const backend::ModelOptions* request, backend::Result* result) override {
        auto auth = checkAuth(context);
        if (!auth.ok()) return auth;
        // Implement LoadModel RPC
        common_params params;
        params_parse(ctx_server, request, params);

        common_init();
        // Ensure debug logs are enabled after common_init() sets up logging
        common_log_set_verbosity_thold(params.verbosity);

        llama_backend_init();
        llama_numa_init(params.numa);


        LOG_INF("system info: n_threads = %d, n_threads_batch = %d, total_threads = %d\n", params.cpuparams.n_threads, params.cpuparams_batch.n_threads, std::thread::hardware_concurrency());
        LOG_INF("\n");
        LOG_INF("%s\n", common_params_get_system_info(params).c_str());
        LOG_INF("\n");
        
        // Capture error messages during model loading
        struct error_capture {
            std::string captured_error;
            std::mutex error_mutex;
            ggml_log_callback original_callback;
            void* original_user_data;
        } error_capture_data;
        
        // Get original log callback
        llama_log_get(&error_capture_data.original_callback, &error_capture_data.original_user_data);
        
        // Set custom callback to capture errors
        llama_log_set([](ggml_log_level level, const char * text, void * user_data) {
            auto* capture = static_cast<error_capture*>(user_data);
            
            // Capture error messages
            if (level == GGML_LOG_LEVEL_ERROR) {
                std::lock_guard<std::mutex> lock(capture->error_mutex);
                // Append error message, removing trailing newlines
                std::string msg(text);
                while (!msg.empty() && (msg.back() == '\n' || msg.back() == '\r')) {
                    msg.pop_back();
                }
                if (!msg.empty()) {
                    if (!capture->captured_error.empty()) {
                        capture->captured_error.append("; ");
                    }
                    capture->captured_error.append(msg);
                }
            }
            
            // Also call original callback to preserve logging
            if (capture->original_callback) {
                capture->original_callback(level, text, capture->original_user_data);
            }
        }, &error_capture_data);
        
        // load the model
        bool load_success = ctx_server.load_model(params);
        
        // Restore original log callback
        llama_log_set(error_capture_data.original_callback, error_capture_data.original_user_data);
        
        if (!load_success) {
            std::string error_msg = "Failed to load model: " + params.model.path;
            if (!params.mmproj.path.empty()) {
                error_msg += " (with mmproj: " + params.mmproj.path + ")";
            }
            if (params.speculative.has_dft() && !params.speculative.draft.mparams.path.empty()) {
                error_msg += " (with draft model: " + params.speculative.draft.mparams.path + ")";
            }
            
            // Add captured error details if available
            {
                std::lock_guard<std::mutex> lock(error_capture_data.error_mutex);
                if (!error_capture_data.captured_error.empty()) {
                    error_msg += ". Error: " + error_capture_data.captured_error;
                } else {
                    error_msg += ". Model file may not exist or be invalid.";
                }
            }
            
            result->set_message(error_msg);
            result->set_success(false);
            return grpc::Status(grpc::StatusCode::INTERNAL, error_msg);
        }

        // Process grammar triggers now that vocab is available
        if (!params.sampling.grammar_triggers.empty()) {
            std::vector<common_grammar_trigger> processed_triggers;
            for (const auto& trigger : params.sampling.grammar_triggers) {
                if (trigger.type == COMMON_GRAMMAR_TRIGGER_TYPE_WORD) {
                    auto ids = common_tokenize(ctx_server.impl->vocab, trigger.value, /* add_special= */ false, /* parse_special= */ true);
                    if (ids.size() == 1) {
                        auto token = ids[0];
                        // Add the token to preserved_tokens if not already present
                        if (params.sampling.preserved_tokens.find(token) == params.sampling.preserved_tokens.end()) {
                            params.sampling.preserved_tokens.insert(token);
                            LOG_INF("Added grammar trigger token to preserved tokens: %d (`%s`)\n", token, trigger.value.c_str());
                        }
                        LOG_INF("Grammar trigger token: %d (`%s`)\n", token, trigger.value.c_str());
                        common_grammar_trigger processed_trigger;
                        processed_trigger.type = COMMON_GRAMMAR_TRIGGER_TYPE_TOKEN;
                        processed_trigger.value = trigger.value;
                        processed_trigger.token = token;
                        processed_triggers.push_back(std::move(processed_trigger));
                    } else {
                        LOG_INF("Grammar trigger word: `%s`\n", trigger.value.c_str());
                        processed_triggers.push_back(trigger);
                    }
                } else {
                    processed_triggers.push_back(trigger);
                }
            }
            // Update the grammar triggers in params
            params.sampling.grammar_triggers = std::move(processed_triggers);
        }

        //ctx_server.init();
        result->set_message("Loading succeeded");
        result->set_success(true);
        loaded_model = true;
        // Store copy of params_base for use in parse_options and other methods
        params_base = params;

        return Status::OK;
    }

    // Helper function to extract logprobs from JSON response
    static json extract_logprobs_from_json(const json& res_json) {
        json logprobs_json = json::object();

        // Check for OAI-compatible format: choices[0].logprobs
        if (res_json.contains("choices") && res_json["choices"].is_array() &&
            res_json["choices"].size() > 0 && res_json["choices"][0].contains("logprobs")) {
            logprobs_json = res_json["choices"][0]["logprobs"];
        }
        // Check for non-OAI format: completion_probabilities
        else if (res_json.contains("completion_probabilities")) {
            // Convert completion_probabilities to OAI format
            logprobs_json["content"] = res_json["completion_probabilities"];
        }
        // Check for direct logprobs field
        else if (res_json.contains("logprobs")) {
            logprobs_json = res_json["logprobs"];
        }

        return logprobs_json;
    }

    // Helper: populate chat_deltas on a Reply from oaicompat_msg_diffs (streaming chunks)
    static void populate_chat_deltas_from_diffs(backend::Reply & reply,
                                                const std::vector<common_chat_msg_diff> & diffs) {
        for (const auto & diff : diffs) {
            auto* delta = reply.add_chat_deltas();
            if (!diff.content_delta.empty()) {
                delta->set_content(diff.content_delta);
            }
            if (!diff.reasoning_content_delta.empty()) {
                delta->set_reasoning_content(diff.reasoning_content_delta);
            }
            if (diff.tool_call_index != std::string::npos) {
                auto* tc = delta->add_tool_calls();
                tc->set_index(static_cast<int32_t>(diff.tool_call_index));
                if (!diff.tool_call_delta.id.empty()) {
                    tc->set_id(diff.tool_call_delta.id);
                }
                if (!diff.tool_call_delta.name.empty()) {
                    tc->set_name(diff.tool_call_delta.name);
                }
                if (!diff.tool_call_delta.arguments.empty()) {
                    tc->set_arguments(diff.tool_call_delta.arguments);
                }
            }
        }
    }

    // Helper: populate chat_deltas on a Reply from final oaicompat_msg (non-streaming)
    static void populate_chat_deltas_from_final(backend::Reply & reply,
                                                const common_chat_msg & msg) {
        // Content delta
        if (!msg.content.empty() || !msg.reasoning_content.empty() || !msg.tool_calls.empty()) {
            auto* delta = reply.add_chat_deltas();
            if (!msg.content.empty()) {
                delta->set_content(msg.content);
            }
            if (!msg.reasoning_content.empty()) {
                delta->set_reasoning_content(msg.reasoning_content);
            }
            // Tool calls as individual deltas within the same ChatDelta
            for (size_t i = 0; i < msg.tool_calls.size(); i++) {
                auto* tc = delta->add_tool_calls();
                tc->set_index(static_cast<int32_t>(i));
                tc->set_id(msg.tool_calls[i].id);
                tc->set_name(msg.tool_calls[i].name);
                tc->set_arguments(msg.tool_calls[i].arguments);
            }
        }
    }

    grpc::Status PredictStream(grpc::ServerContext* context, const backend::PredictOptions* request, grpc::ServerWriter<backend::Reply>* writer) override {
        auto auth = checkAuth(context);
        if (!auth.ok()) return auth;
        if (params_base.model.path.empty()) {
            return grpc::Status(grpc::StatusCode::FAILED_PRECONDITION, "Model not loaded");
        }
        conflict_guard guard("PredictStream", slot_loop_inflight, score_inflight, "score_inflight");
        json data = parse_options(true, request, params_base, ctx_server.get_llama_context());


        //Raise error if embeddings is set to true
        if (params_base.embedding) {
            return grpc::Status(grpc::StatusCode::INVALID_ARGUMENT, "Embedding is not supported in streaming mode");
        }


        auto completion_id = gen_chatcmplid();
        // get response reader - it contains references to the queues and will stay valid
        auto rd = ctx_server.get_response_reader();
        try {
            std::vector<server_task> tasks;

            std::string prompt_str;
            std::vector<raw_buffer> files; // Declare files early so it's accessible in both branches
            // Handle chat templates when UseTokenizerTemplate is enabled and Messages are provided
            if (request->usetokenizertemplate() && request->messages_size() > 0 && ctx_server.impl->chat_params.tmpls != nullptr) {
                // Convert proto Messages to JSON format compatible with oaicompat_chat_params_parse
                json body_json;
                json messages_json = json::array();

                // Find the last user message index to attach images/audio to
                int last_user_msg_idx = -1;
                for (int i = request->messages_size() - 1; i >= 0; i--) {
                    if (request->messages(i).role() == "user") {
                        last_user_msg_idx = i;
                        break;
                    }
                }

                for (int i = 0; i < request->messages_size(); i++) {
                    const auto& msg = request->messages(i);
                    llama_grpc::ReconstructedMessageInput rin;
                    rin.role = msg.role();
                    rin.content = msg.content();
                    rin.name = msg.name();
                    rin.tool_call_id = msg.tool_call_id();
                    rin.reasoning_content = msg.reasoning_content();
                    rin.tool_calls = msg.tool_calls();
                    rin.is_last_user_msg = (i == last_user_msg_idx);
                    if (rin.is_last_user_msg) {
                        for (int j = 0; j < request->images_size(); j++) rin.images.push_back(request->images(j));
                        for (int j = 0; j < request->audios_size(); j++) rin.audios.push_back(request->audios(j));
                        for (int j = 0; j < request->videos_size(); j++) rin.videos.push_back(request->videos(j));
                    }
                    messages_json.push_back(llama_grpc::build_reconstructed_message(rin));
                }

                // Final safety check: Ensure no message has null content (Jinja templates require strings)
                SRV_INF("[CONTENT DEBUG] PredictStream: Running final safety check on %zu messages\n", messages_json.size());
                for (size_t idx = 0; idx < messages_json.size(); idx++) {
                    auto& msg = messages_json[idx];
                    if (msg.contains("content") && msg["content"].is_null()) {
                        SRV_INF("[CONTENT DEBUG] PredictStream: Safety check found message %zu with NULL content, converting to empty string\n", idx);
                        msg["content"] = "";
                    } else if (!msg.contains("content")) {
                        SRV_INF("[CONTENT DEBUG] PredictStream: Safety check found message %zu without content field, adding empty string\n", idx);
                        msg["content"] = "";
                    } else {
                        SRV_INF("[CONTENT DEBUG] PredictStream: Safety check message %zu: content OK, type=%s\n",
                                idx, msg["content"].is_string() ? "string" :
                                    msg["content"].is_array() ? "array" :
                                    msg["content"].is_object() ? "object" : "other");
                    }
                }

                // Debug: Count tool messages
                int tool_msg_count = 0;
                for (const auto& msg : messages_json) {
                    if (msg.contains("role") && msg["role"] == "tool") {
                        tool_msg_count++;
                    }
                }
                SRV_DBG("[TOOLS DEBUG] PredictStream: Built %d tool messages out of %zu total messages\n", tool_msg_count, messages_json.size());

                // Debug: Print full conversation (messages)
                SRV_DBG("[CONVERSATION DEBUG] PredictStream: Full messages array:\n%s\n", messages_json.dump(2).c_str());

                body_json["messages"] = messages_json;
                body_json["stream"] = true; // PredictStream is always streaming
                body_json["stream_options"] = {{"include_usage", true}}; // Ensure token counts in final chunk

                // Check if grammar is provided from Go layer (NoGrammar=false)
                // If grammar is provided, we must use it and NOT let template generate grammar from tools
                // oaicompat_chat_params_parse throws an error if both grammar and tools are provided
                bool has_grammar_from_go = data.contains("grammar") &&
                    data["grammar"].is_string() &&
                    !data["grammar"].get<std::string>().empty();

                SRV_INF("[TOOLS DEBUG] PredictStream: has_grammar_from_go=%d, data.contains(\"tools\")=%d, data.contains(\"grammar\")=%d\n",
                        has_grammar_from_go ? 1 : 0,
                        data.contains("tools") ? 1 : 0,
                        data.contains("grammar") ? 1 : 0);
                if (data.contains("grammar")) {
                    SRV_INF("[TOOLS DEBUG] PredictStream: grammar type=%s, empty=%d\n",
                            data["grammar"].is_string() ? "string" : "other",
                            data["grammar"].is_string() && data["grammar"].get<std::string>().empty() ? 1 : 0);
                }

                // Copy other relevant fields from data that oaicompat_chat_params_parse expects
                // Tools and tool_choice are only passed when NoGrammar is true (grammar not provided)
                // When grammar is provided from Go layer, we use it instead of template-generated grammar
                if (!has_grammar_from_go) {
                    // NoGrammar=true: pass tools and let template generate grammar
                    if (data.contains("tools")) {
                        body_json["tools"] = data["tools"];
                        std::string tools_str = data["tools"].dump();
                        SRV_INF("Using tools from data (NoGrammar=true): %s\n", tools_str.c_str());
                        // Debug: Log tools count and details before template processing
                        if (data["tools"].is_array()) {
                            SRV_INF("[TOOLS DEBUG] PredictStream: Passing %zu tools to oaicompat_chat_params_parse\n", data["tools"].size());
                            for (size_t t_idx = 0; t_idx < data["tools"].size(); t_idx++) {
                                const auto& tool = data["tools"][t_idx];
                                std::string tool_name = "unknown";
                                std::string tool_desc = "";
                                if (tool.contains("function")) {
                                    const auto& func = tool["function"];
                                    if (func.contains("name")) {
                                        tool_name = func["name"].get<std::string>();
                                    }
                                    if (func.contains("description")) {
                                        tool_desc = func["description"].is_string() ?
                                            func["description"].get<std::string>() : "";
                                    }
                                } else if (tool.contains("name")) {
                                    tool_name = tool["name"].get<std::string>();
                                    if (tool.contains("description")) {
                                        tool_desc = tool["description"].is_string() ?
                                            tool["description"].get<std::string>() : "";
                                    }
                                }
                                SRV_INF("[TOOLS DEBUG] PredictStream: Tool %zu: name=%s, description=%s\n",
                                        t_idx, tool_name.c_str(), tool_desc.substr(0, 100).c_str());
                            }
                        }
                    } else {
                        SRV_WRN("%s", "No tools found in data - tool calls will not work without tools field\n");
                        SRV_DBG("[TOOLS DEBUG] PredictStream: No tools in data, tool_choice=%s\n", data.contains("tool_choice") ? data["tool_choice"].dump().c_str() : "not set");
                    }
                    if (data.contains("tool_choice")) {
                        // tool_choice can be a string or object, but oaicompat_chat_params_parse expects a string
                        // Convert object tool_choice to "required" (since a specific function is requested)
                        if (data["tool_choice"].is_string()) {
                            body_json["tool_choice"] = data["tool_choice"].get<std::string>();
                        } else if (data["tool_choice"].is_object()) {
                            // Object tool_choice means a specific function is requested, use "required"
                            body_json["tool_choice"] = "required";
                            std::string tool_choice_obj_str = data["tool_choice"].dump();
                            SRV_INF("Converted object tool_choice to 'required': %s\n", tool_choice_obj_str.c_str());
                        } else {
                            // Fallback: convert to string
                            body_json["tool_choice"] = data["tool_choice"].dump();
                        }
                        std::string tool_choice_str = body_json["tool_choice"].get<std::string>();
                        SRV_INF("Using tool_choice: %s\n", tool_choice_str.c_str());
                    } else {
                        // Default to "auto" if not specified
                        body_json["tool_choice"] = "auto";
                    }
                } else {
                    // Grammar is provided from Go layer (NoGrammar=false) - use it, don't pass tools
                    SRV_INF("%s", "Grammar provided from Go layer - using it instead of template-generated grammar\n");
                    // Grammar will be copied from data after parsing (it's already in data)
                }

                if (data.contains("json_schema")) {
                    body_json["json_schema"] = data["json_schema"];
                }
                // If grammar is provided from Go layer, copy it to body_json so it's preserved
                // (though oaicompat_chat_params_parse may not use it if tools are present)
                if (has_grammar_from_go) {
                    body_json["grammar"] = data["grammar"];
                }
                if (data.contains("response_format")) {
                    body_json["response_format"] = data["response_format"];
                }
                if (data.contains("chat_template_kwargs")) {
                    body_json["chat_template_kwargs"] = data["chat_template_kwargs"];
                }
                // Pass parallel_tool_calls if present (used by oaicompat_chat_params_parse)
                if (data.contains("parallel_tool_calls")) {
                    body_json["parallel_tool_calls"] = data["parallel_tool_calls"];
                }
                // Pass add_generation_prompt if present (used by oaicompat_chat_params_parse)
                if (data.contains("add_generation_prompt")) {
                    body_json["add_generation_prompt"] = data["add_generation_prompt"];
                }

                // Pass sampling parameters to body_json so oaicompat_chat_params_parse respects them
                // and doesn't overwrite them with defaults in the returned parsed_data
                if (data.contains("n_predict")) {
                    body_json["max_tokens"] = data["n_predict"];
                }
                if (data.contains("ignore_eos")) {
                    body_json["ignore_eos"] = data["ignore_eos"];
                }
                if (data.contains("stop")) {
                    body_json["stop"] = data["stop"];
                }
                if (data.contains("temperature")) {
                    body_json["temperature"] = data["temperature"];
                }
                if (data.contains("top_p")) {
                    body_json["top_p"] = data["top_p"];
                }
                if (data.contains("frequency_penalty")) {
                    body_json["frequency_penalty"] = data["frequency_penalty"];
                }
                if (data.contains("presence_penalty")) {
                    body_json["presence_penalty"] = data["presence_penalty"];
                }
                if (data.contains("seed")) {
                    body_json["seed"] = data["seed"];
                }
                if (data.contains("logit_bias")) {
                    body_json["logit_bias"] = data["logit_bias"];
                }
                if (data.contains("top_k")) {
                    body_json["top_k"] = data["top_k"];
                }
                if (data.contains("min_p")) {
                    body_json["min_p"] = data["min_p"];
                }

                // Forward the chat_template_kwargs the Go layer resolved (model config
                // chat_template_kwargs + per-request metadata: enable_thinking,
                // reasoning_effort, preserve_thinking, ...). One generic merge replaces
                // the previous per-key handling - new template levers need no C++ change.
                // oaicompat_chat_params_parse reads these from body_json.
                const auto& metadata = request->metadata();
                auto ctk_it = metadata.find("chat_template_kwargs");
                if (ctk_it != metadata.end() && !ctk_it->second.empty()) {
                    try {
                        json ctk = json::parse(ctk_it->second);
                        if (ctk.is_object()) {
                            if (!body_json.contains("chat_template_kwargs")) {
                                body_json["chat_template_kwargs"] = json::object();
                            }
                            for (auto& el : ctk.items()) {
                                body_json["chat_template_kwargs"][el.key()] = el.value();
                            }
                        }
                    } catch (const std::exception & e) {
                        SRV_WRN("failed to parse chat_template_kwargs metadata: %s\n", e.what());
                    }
                }

                // Debug: Print full body_json before template processing (includes messages, tools, tool_choice, etc.)
                SRV_DBG("[CONVERSATION DEBUG] PredictStream: Full body_json before oaicompat_chat_params_parse:\n%s\n", body_json.dump(2).c_str());

                // Use the same approach as server.cpp: call oaicompat_chat_params_parse
                // This handles all template application, grammar merging, etc. automatically
                // Files extracted from multimodal content in messages will be added to the files vector
                // chat_params already contains tmpls, allow_image, and allow_audio set during model loading

                // Debug: Log tools before template processing
                if (body_json.contains("tools")) {
                    SRV_DBG("[TOOLS DEBUG] PredictStream: Before oaicompat_chat_params_parse - tools count: %zu\n",
                            body_json["tools"].is_array() ? body_json["tools"].size() : 0);
                }

                // Debug: Verify messages content before template processing
                // Also ensure ALL messages have content set to string (not null) - templates expect strings
                if (body_json.contains("messages") && body_json["messages"].is_array()) {
                    SRV_INF("[CONTENT DEBUG] PredictStream: Before oaicompat_chat_params_parse - checking %zu messages\n", body_json["messages"].size());
                    for (size_t idx = 0; idx < body_json["messages"].size(); idx++) {
                        llama_grpc::normalize_template_message(body_json["messages"][idx]);
                    }
                }

                json parsed_data = oaicompat_chat_params_parse(body_json, ctx_server.impl->chat_params, files);

                // Debug: Log tools after template processing
                if (parsed_data.contains("tools")) {
                    SRV_DBG("[TOOLS DEBUG] PredictStream: After oaicompat_chat_params_parse - tools count: %zu\n",
                            parsed_data["tools"].is_array() ? parsed_data["tools"].size() : 0);
                } else {
                    SRV_DBG("%s", "[TOOLS DEBUG] PredictStream: After oaicompat_chat_params_parse - no tools in parsed_data\n");
                }

                // Extract the prompt from parsed data
                prompt_str = parsed_data.at("prompt").get<std::string>();

                // Preserve grammar from Go layer if it was provided (NoGrammar=false)
                // Otherwise, use grammar from parsed_data (template-generated when NoGrammar=true)
                json preserved_grammar;
                if (has_grammar_from_go && data.contains("grammar")) {
                    preserved_grammar = data["grammar"];
                }

                // Merge all fields from parsed_data into data (grammar, grammar_triggers, preserved_tokens, parse_tool_calls, etc.)
                // This ensures all template-generated fields are included
                // parse_tool_calls is set by oaicompat_chat_params_parse when tools are present
                for (const auto& item : parsed_data.items()) {
                    if (item.key() != "prompt") { // Don't overwrite prompt_str, we already extracted it
                        // If grammar was provided from Go layer, preserve it instead of template-generated grammar
                        if (item.key() == "grammar" && has_grammar_from_go && !preserved_grammar.is_null()) {
                            data["grammar"] = preserved_grammar;
                        } else {
                            data[item.key()] = item.value();
                        }
                    }
                }

                // Debug: Log parse_tool_calls if present (set by oaicompat_chat_params_parse when tools are present)
                if (data.contains("parse_tool_calls")) {
                    SRV_DBG("[TOOLS DEBUG] PredictStream: parse_tool_calls=%s\n", data["parse_tool_calls"].get<bool>() ? "true" : "false");
                }
            } else {
                // Use prompt directly from data
                if (data.contains("prompt") && data["prompt"].is_string()) {
                    prompt_str = data["prompt"].get<std::string>();
                } else {
                    prompt_str = request->prompt();
                }
            }

            const auto type = SERVER_TASK_TYPE_COMPLETION;
            // TODO: this log can become very long, put it behind a flag or think about a more compact format
            //SRV_DBG("Prompt: %s\n", prompt.is_string() ? prompt.get<std::string>().c_str() : prompt.dump(2).c_str());

            // If not using chat templates, extract files from image_data/audio_data fields
            // (If using chat templates, files were already extracted by oaicompat_chat_params_parse)
            if (!request->usetokenizertemplate() || request->messages_size() == 0 || ctx_server.impl->chat_params.tmpls == nullptr) {
                const auto &images_data = data.find("image_data");
                if (images_data != data.end() && images_data->is_array())
                {
                    for (const auto &img : *images_data)
                    {
                        auto decoded_data = base64_decode(img["data"].get<std::string>());
                        files.push_back(decoded_data);
                    }
                }

                const auto &audio_data = data.find("audio_data");
                if (audio_data != data.end() && audio_data->is_array())
                {
                    for (const auto &audio : *audio_data)
                    {
                        auto decoded_data = base64_decode(audio["data"].get<std::string>());
                        files.push_back(decoded_data);
                    }
                }

                const auto &video_data = data.find("video_data");
                if (video_data != data.end() && video_data->is_array())
                {
                    for (const auto &video : *video_data)
                    {
                        auto decoded_data = base64_decode(video["data"].get<std::string>());
                        files.push_back(decoded_data);
                    }
                }
            }

            const bool has_mtmd = ctx_server.impl->mctx != nullptr;

            // process prompt
            std::vector<server_tokens> inputs;
            if (has_mtmd) {
                // multimodal
                inputs.push_back(process_mtmd_prompt(ctx_server.impl->mctx, prompt_str, files));
            } else {
                 // Everything else, including multimodal completions.
                inputs = tokenize_input_prompts(ctx_server.impl->vocab, ctx_server.impl->mctx, prompt_str, true, true);
            }

            tasks.reserve(inputs.size());
            for (size_t i = 0; i < inputs.size(); i++) {
                server_task task = server_task(type);

                task.id    = rd.queue_tasks.get_new_id();
                task.index = i;

                task.tokens    = std::move(inputs[i]);
#ifdef LOCALAI_HAS_SERVER_SCHEMA
                task.params           = server_schema::eval_llama_cmpl_schema(
#else
                task.params           = server_task::params_from_json_cmpl(
#endif
                        ctx_server.impl->vocab,
                        params_base,
                        ctx_server.get_meta().slot_n_ctx,
                        ctx_server.get_meta().logit_bias_eog,
                        data);
                task.id_slot = json_value(data, "id_slot", -1);

                // OAI-compat: enable autoparser (PEG-based chat parsing) so that
                // reasoning, tool calls, and content are classified into ChatDeltas.
                // Without this, the PEG parser never produces diffs and the Go side
                // cannot detect tool calls or separate reasoning from content.
                task.params.res_type                 = TASK_RESPONSE_TYPE_OAI_CHAT;
                task.params.oaicompat_cmpl_id         = completion_id;
                // oaicompat_model is already populated by eval_llama_cmpl_schema

                tasks.push_back(std::move(task));
            }

            rd.post_tasks(std::move(tasks));
        } catch (const std::exception & e) {
            return grpc::Status(grpc::StatusCode::INVALID_ARGUMENT, e.what());
        }

        // Get first result for error checking (following server.cpp pattern)
        server_task_result_ptr first_result = rd.next([&context]() { return context->IsCancelled(); });
        if (first_result == nullptr) {
            // connection is closed
            return grpc::Status(grpc::StatusCode::CANCELLED, "Request cancelled by client");
        } else if (first_result->is_error()) {
            json error_json = first_result->to_json();
            backend::Reply reply;
            reply.set_message(error_json.value("message", ""));
            writer->Write(reply);
            return grpc::Status(grpc::StatusCode::INTERNAL, error_json.value("message", "Error occurred"));
        }

        // Lambda to build a Reply from JSON + attach chat deltas from a result.
        // Handles both native format ({"content": "..."}) and OAI chat format
        // ({"choices": [{"delta": {"content": "...", "reasoning": "..."}}]}).
        auto build_reply_from_json = [](const json & res_json, server_task_result * raw_result) -> backend::Reply {
            backend::Reply reply;
            std::string completion_text;

            if (res_json.contains("choices")) {
                // OAI chat format — extract content from choices[0].delta
                const auto & choices = res_json.at("choices");
                if (!choices.empty()) {
                    const auto & delta = choices[0].value("delta", json::object());
                    if (delta.contains("content") && !delta.at("content").is_null()) {
                        completion_text = delta.at("content").get<std::string>();
                    }
                }
            } else {
                // Native llama.cpp format
                completion_text = res_json.value("content", "");
            }

            reply.set_message(completion_text);

            // Token counts: native format has top-level fields,
            // OAI format has them in "usage" (final chunk only)
            if (res_json.contains("usage")) {
                const auto & usage = res_json.at("usage");
                reply.set_tokens(usage.value("completion_tokens", 0));
                reply.set_prompt_tokens(usage.value("prompt_tokens", 0));
            } else {
                reply.set_tokens(res_json.value("tokens_predicted", 0));
                reply.set_prompt_tokens(res_json.value("tokens_evaluated", 0));
            }

            // Timings: present as top-level "timings" in both formats
            if (res_json.contains("timings")) {
                reply.set_timing_prompt_processing(res_json.at("timings").value("prompt_ms", 0.0));
                reply.set_timing_token_generation(res_json.at("timings").value("predicted_ms", 0.0));
            }

            // Logprobs: extract_logprobs_from_json handles both formats
            json logprobs_json = extract_logprobs_from_json(res_json);
            if (!logprobs_json.empty() && !logprobs_json.is_null()) {
                reply.set_logprobs(logprobs_json.dump());
            }

            return reply;
        };

        // Attach chat deltas from the autoparser to a Reply.
        // When diffs are available, populate ChatDeltas on the reply.
        // The raw message is always preserved so the Go side can use it
        // for reasoning extraction and tool call parsing as a fallback
        // (important in distributed mode where ChatDeltas may not be
        // the primary parsing path).
        auto attach_chat_deltas = [](backend::Reply & reply, server_task_result * raw_result) {
            // Try streaming partial result first
            auto* partial = dynamic_cast<server_task_result_cmpl_partial*>(raw_result);
            if (partial && !partial->oaicompat_msg_diffs.empty()) {
                populate_chat_deltas_from_diffs(reply, partial->oaicompat_msg_diffs);
                return;
            }
            // Try final result
            auto* final_res = dynamic_cast<server_task_result_cmpl_final*>(raw_result);
            if (final_res && final_res->is_updated) {
                populate_chat_deltas_from_diffs(reply, final_res->oaicompat_msg_diffs);
            }
        };

        // Process first result.
        // When TASK_RESPONSE_TYPE_OAI_CHAT is used, the first token may
        // produce a JSON array with a role-init element followed by the
        // actual content element. We must only attach chat deltas to the
        // content element — attaching to both would duplicate the first
        // token since oaicompat_msg_diffs is the same for both.
        json first_res_json = first_result->to_json();
        // Upstream llama.cpp (ggml-org/llama.cpp#23884) now emits an initial
        // "begin" partial whose to_json() returns null, used only to signal the
        // HTTP layer to flush 200 status headers before any token. gRPC has no
        // such concept, so there is nothing to emit — the real tokens arrive in
        // the loop below. Feeding this null into build_reply_from_json would
        // throw (uncaught) and surface as a generic RPC error.
        if (first_res_json.is_null()) {
            // skip the begin-of-stream marker
        } else if (first_res_json.is_array()) {
            for (const auto & res : first_res_json) {
                auto reply = build_reply_from_json(res, first_result.get());
                // Skip chat deltas for role-init elements (have "role" in
                // delta but no content/reasoning diffs of their own).
                bool is_role_init = res.contains("choices") && !res["choices"].empty() &&
                                    res["choices"][0].value("delta", json::object()).contains("role");
                if (!is_role_init) {
                    attach_chat_deltas(reply, first_result.get());
                }
                writer->Write(reply);
            }
        } else {
            auto reply = build_reply_from_json(first_res_json, first_result.get());
            attach_chat_deltas(reply, first_result.get());
            writer->Write(reply);
        }

        // Process subsequent results
        while (rd.has_next()) {
            if (context->IsCancelled()) {
                break;
            }

            auto result = rd.next([&context]() { return context->IsCancelled(); });
            if (result == nullptr) {
                break;
            }

            json res_json = result->to_json();
            if (res_json.is_null()) {
                // begin-of-stream marker (see note above) — nothing to emit
                continue;
            } else if (res_json.is_array()) {
                for (const auto & res : res_json) {
                    auto reply = build_reply_from_json(res, result.get());
                    bool is_role_init = res.contains("choices") && !res["choices"].empty() &&
                                        res["choices"][0].value("delta", json::object()).contains("role");
                    if (!is_role_init) {
                        attach_chat_deltas(reply, result.get());
                    }
                    writer->Write(reply);
                }
            } else {
                auto reply = build_reply_from_json(res_json, result.get());
                attach_chat_deltas(reply, result.get());
                writer->Write(reply);
            }
        }

        // Check if context was cancelled during processing
        if (context->IsCancelled()) {
            return grpc::Status(grpc::StatusCode::CANCELLED, "Request cancelled by client");
        }

        return grpc::Status::OK;
    }

    grpc::Status Predict(ServerContext* context, const backend::PredictOptions* request, backend::Reply* reply) override {
         auto auth = checkAuth(context);
         if (!auth.ok()) return auth;
         if (params_base.model.path.empty()) {
             return grpc::Status(grpc::StatusCode::FAILED_PRECONDITION, "Model not loaded");
         }
         conflict_guard guard("Predict", slot_loop_inflight, score_inflight, "score_inflight");
         json data = parse_options(true, request, params_base, ctx_server.get_llama_context());

        data["stream"] = false;
        //Raise error if embeddings is set to true
        if (params_base.embedding) {
            return grpc::Status(grpc::StatusCode::INVALID_ARGUMENT, "Embedding is not supported in Predict mode");
        }
        std::cout << "[PREDICT] Received result: " << data.dump(2) << std::endl;
        auto completion_id = gen_chatcmplid();
        auto rd = ctx_server.get_response_reader();
        try {
            std::vector<server_task> tasks;

            std::string prompt_str;
            std::vector<raw_buffer> files; // Declare files early so it's accessible in both branches
            // Handle chat templates when UseTokenizerTemplate is enabled and Messages are provided
            if (request->usetokenizertemplate() && request->messages_size() > 0 && ctx_server.impl->chat_params.tmpls != nullptr) {
                // Convert proto Messages to JSON format compatible with oaicompat_chat_params_parse
                json body_json;
                json messages_json = json::array();

                // Find the last user message index to attach images/audio to
                int last_user_msg_idx = -1;
                for (int i = request->messages_size() - 1; i >= 0; i--) {
                    if (request->messages(i).role() == "user") {
                        last_user_msg_idx = i;
                        break;
                    }
                }

                SRV_INF("[CONTENT DEBUG] Predict: Processing %d messages\n", request->messages_size());
                for (int i = 0; i < request->messages_size(); i++) {
                    const auto& msg = request->messages(i);
                    llama_grpc::ReconstructedMessageInput rin;
                    rin.role = msg.role();
                    rin.content = msg.content();
                    rin.name = msg.name();
                    rin.tool_call_id = msg.tool_call_id();
                    rin.reasoning_content = msg.reasoning_content();
                    rin.tool_calls = msg.tool_calls();
                    rin.is_last_user_msg = (i == last_user_msg_idx);
                    if (rin.is_last_user_msg) {
                        for (int j = 0; j < request->images_size(); j++) rin.images.push_back(request->images(j));
                        for (int j = 0; j < request->audios_size(); j++) rin.audios.push_back(request->audios(j));
                        for (int j = 0; j < request->videos_size(); j++) rin.videos.push_back(request->videos(j));
                    }
                    messages_json.push_back(llama_grpc::build_reconstructed_message(rin));
                }

                // Final safety check: Ensure no message has null content (Jinja templates require strings)
                SRV_INF("[CONTENT DEBUG] Predict: Running final safety check on %zu messages\n", messages_json.size());
                for (size_t idx = 0; idx < messages_json.size(); idx++) {
                    auto& msg = messages_json[idx];
                    std::string role_str = msg.contains("role") ? msg["role"].get<std::string>() : "unknown";
                    if (msg.contains("content") && msg["content"].is_null()) {
                        SRV_INF("[CONTENT DEBUG] Predict: Safety check found message %zu (role=%s) with NULL content, converting to empty string\n", idx, role_str.c_str());
                        msg["content"] = "";
                    } else if (!msg.contains("content")) {
                        SRV_INF("[CONTENT DEBUG] Predict: Safety check found message %zu (role=%s) without content field, adding empty string\n", idx, role_str.c_str());
                        msg["content"] = "";
                    } else {
                        SRV_INF("[CONTENT DEBUG] Predict: Safety check message %zu (role=%s): content OK, type=%s\n",
                                idx, role_str.c_str(),
                                msg["content"].is_string() ? "string" :
                                msg["content"].is_array() ? "array" :
                                msg["content"].is_object() ? "object" : "other");
                    }
                }

                // Debug: Count tool messages
                int tool_msg_count = 0;
                for (const auto& msg : messages_json) {
                    if (msg.contains("role") && msg["role"] == "tool") {
                        tool_msg_count++;
                    }
                }
                SRV_DBG("[TOOLS DEBUG] Predict: Built %d tool messages out of %zu total messages\n", tool_msg_count, messages_json.size());

                // Debug: Print full conversation (messages)
                SRV_DBG("[CONVERSATION DEBUG] Predict: Full messages array:\n%s\n", messages_json.dump(2).c_str());

                body_json["messages"] = messages_json;
                body_json["stream"] = false;

                // Check if grammar is provided from Go layer (NoGrammar=false)
                // If grammar is provided, we must use it and NOT let template generate grammar from tools
                // oaicompat_chat_params_parse throws an error if both grammar and tools are provided
                bool has_grammar_from_go = data.contains("grammar") &&
                    data["grammar"].is_string() &&
                    !data["grammar"].get<std::string>().empty();

                SRV_INF("[TOOLS DEBUG] Predict: has_grammar_from_go=%d, data.contains(\"tools\")=%d, data.contains(\"grammar\")=%d\n",
                        has_grammar_from_go ? 1 : 0,
                        data.contains("tools") ? 1 : 0,
                        data.contains("grammar") ? 1 : 0);
                if (data.contains("grammar")) {
                    SRV_INF("[TOOLS DEBUG] Predict: grammar type=%s, empty=%d\n",
                            data["grammar"].is_string() ? "string" : "other",
                            data["grammar"].is_string() && data["grammar"].get<std::string>().empty() ? 1 : 0);
                }

                // Copy other relevant fields from data that oaicompat_chat_params_parse expects
                // Tools and tool_choice are only passed when NoGrammar is true (grammar not provided)
                // When grammar is provided from Go layer, we use it instead of template-generated grammar
                if (!has_grammar_from_go) {
                    // NoGrammar=true: pass tools and let template generate grammar
                    if (data.contains("tools")) {
                        body_json["tools"] = data["tools"];
                        std::string tools_str = data["tools"].dump();
                        SRV_INF("Using tools from data (NoGrammar=true): %s\n", tools_str.c_str());
                        // Debug: Log tools count and details before template processing
                        if (data["tools"].is_array()) {
                            SRV_INF("[TOOLS DEBUG] Predict: Passing %zu tools to oaicompat_chat_params_parse\n", data["tools"].size());
                            for (size_t t_idx = 0; t_idx < data["tools"].size(); t_idx++) {
                                const auto& tool = data["tools"][t_idx];
                                std::string tool_name = "unknown";
                                std::string tool_desc = "";
                                if (tool.contains("function")) {
                                    const auto& func = tool["function"];
                                    if (func.contains("name")) {
                                        tool_name = func["name"].get<std::string>();
                                    }
                                    if (func.contains("description")) {
                                        tool_desc = func["description"].is_string() ?
                                            func["description"].get<std::string>() : "";
                                    }
                                } else if (tool.contains("name")) {
                                    tool_name = tool["name"].get<std::string>();
                                    if (tool.contains("description")) {
                                        tool_desc = tool["description"].is_string() ?
                                            tool["description"].get<std::string>() : "";
                                    }
                                }
                                SRV_INF("[TOOLS DEBUG] Predict: Tool %zu: name=%s, description=%s\n",
                                        t_idx, tool_name.c_str(), tool_desc.substr(0, 100).c_str());
                            }
                        }
                    } else {
                        SRV_WRN("%s", "No tools found in data - tool calls will not work without tools field\n");
                        SRV_DBG("[TOOLS DEBUG] Predict: No tools in data, tool_choice=%s\n", data.contains("tool_choice") ? data["tool_choice"].dump().c_str() : "not set");
                    }
                    if (data.contains("tool_choice")) {
                        // tool_choice can be a string or object, but oaicompat_chat_params_parse expects a string
                        // Convert object tool_choice to "required" (since a specific function is requested)
                        if (data["tool_choice"].is_string()) {
                            body_json["tool_choice"] = data["tool_choice"].get<std::string>();
                        } else if (data["tool_choice"].is_object()) {
                            // Object tool_choice means a specific function is requested, use "required"
                            body_json["tool_choice"] = "required";
                            std::string tool_choice_obj_str = data["tool_choice"].dump();
                            SRV_INF("Converted object tool_choice to 'required': %s\n", tool_choice_obj_str.c_str());
                        } else {
                            // Fallback: convert to string
                            body_json["tool_choice"] = data["tool_choice"].dump();
                        }
                        std::string tool_choice_str = body_json["tool_choice"].get<std::string>();
                        SRV_INF("Using tool_choice: %s\n", tool_choice_str.c_str());
                    } else {
                        // Default to "auto" if not specified
                        body_json["tool_choice"] = "auto";
                    }
                } else {
                    // Grammar is provided from Go layer (NoGrammar=false) - use it, don't pass tools
                    SRV_INF("%s", "Grammar provided from Go layer - using it instead of template-generated grammar\n");
                    // Grammar will be copied from data after parsing (it's already in data)
                }

                if (data.contains("json_schema")) {
                    body_json["json_schema"] = data["json_schema"];
                }
                // If grammar is provided from Go layer, copy it to body_json so it's preserved
                // (though oaicompat_chat_params_parse may not use it if tools are present)
                if (has_grammar_from_go) {
                    body_json["grammar"] = data["grammar"];
                }
                if (data.contains("response_format")) {
                    body_json["response_format"] = data["response_format"];
                }
                if (data.contains("chat_template_kwargs")) {
                    body_json["chat_template_kwargs"] = data["chat_template_kwargs"];
                }
                // Pass parallel_tool_calls if present (used by oaicompat_chat_params_parse)
                if (data.contains("parallel_tool_calls")) {
                    body_json["parallel_tool_calls"] = data["parallel_tool_calls"];
                }
                // Pass add_generation_prompt if present (used by oaicompat_chat_params_parse)
                if (data.contains("add_generation_prompt")) {
                    body_json["add_generation_prompt"] = data["add_generation_prompt"];
                }

                // Pass sampling parameters to body_json so oaicompat_chat_params_parse respects them
                // and doesn't overwrite them with defaults in the returned parsed_data
                if (data.contains("n_predict")) {
                    body_json["max_tokens"] = data["n_predict"];
                }
                if (data.contains("ignore_eos")) {
                    body_json["ignore_eos"] = data["ignore_eos"];
                }
                if (data.contains("stop")) {
                    body_json["stop"] = data["stop"];
                }
                if (data.contains("temperature")) {
                    body_json["temperature"] = data["temperature"];
                }
                if (data.contains("top_p")) {
                    body_json["top_p"] = data["top_p"];
                }
                if (data.contains("frequency_penalty")) {
                    body_json["frequency_penalty"] = data["frequency_penalty"];
                }
                if (data.contains("presence_penalty")) {
                    body_json["presence_penalty"] = data["presence_penalty"];
                }
                if (data.contains("seed")) {
                    body_json["seed"] = data["seed"];
                }
                if (data.contains("logit_bias")) {
                    body_json["logit_bias"] = data["logit_bias"];
                }
                if (data.contains("top_k")) {
                    body_json["top_k"] = data["top_k"];
                }
                if (data.contains("min_p")) {
                    body_json["min_p"] = data["min_p"];
                }

                // Forward the chat_template_kwargs the Go layer resolved (model config
                // chat_template_kwargs + per-request metadata: enable_thinking,
                // reasoning_effort, preserve_thinking, ...). One generic merge replaces
                // the previous per-key handling - new template levers need no C++ change.
                const auto& predict_metadata = request->metadata();
                auto predict_ctk_it = predict_metadata.find("chat_template_kwargs");
                if (predict_ctk_it != predict_metadata.end() && !predict_ctk_it->second.empty()) {
                    try {
                        json ctk = json::parse(predict_ctk_it->second);
                        if (ctk.is_object()) {
                            if (!body_json.contains("chat_template_kwargs")) {
                                body_json["chat_template_kwargs"] = json::object();
                            }
                            for (auto& el : ctk.items()) {
                                body_json["chat_template_kwargs"][el.key()] = el.value();
                            }
                        }
                    } catch (const std::exception & e) {
                        SRV_WRN("failed to parse chat_template_kwargs metadata: %s\n", e.what());
                    }
                }

                // Debug: Print full body_json before template processing (includes messages, tools, tool_choice, etc.)
                SRV_DBG("[CONVERSATION DEBUG] Predict: Full body_json before oaicompat_chat_params_parse:\n%s\n", body_json.dump(2).c_str());

                // Use the same approach as server.cpp: call oaicompat_chat_params_parse
                // This handles all template application, grammar merging, etc. automatically
                // Files extracted from multimodal content in messages will be added to the files vector
                // chat_params already contains tmpls, allow_image, and allow_audio set during model loading

                // Debug: Log tools before template processing
                if (body_json.contains("tools")) {
                    SRV_DBG("[TOOLS DEBUG] Predict: Before oaicompat_chat_params_parse - tools count: %zu\n",
                            body_json["tools"].is_array() ? body_json["tools"].size() : 0);
                }

                // Debug: Verify messages content before template processing
                // Also ensure ALL messages have content set to string (not null) - templates expect strings
                if (body_json.contains("messages") && body_json["messages"].is_array()) {
                    SRV_INF("[CONTENT DEBUG] Predict: Before oaicompat_chat_params_parse - checking %zu messages\n", body_json["messages"].size());
                    for (size_t idx = 0; idx < body_json["messages"].size(); idx++) {
                        llama_grpc::normalize_template_message(body_json["messages"][idx]);
                    }
                }

                json parsed_data = oaicompat_chat_params_parse(body_json, ctx_server.impl->chat_params, files);

                // Debug: Log tools after template processing
                if (parsed_data.contains("tools")) {
                    SRV_DBG("[TOOLS DEBUG] Predict: After oaicompat_chat_params_parse - tools count: %zu\n",
                            parsed_data["tools"].is_array() ? parsed_data["tools"].size() : 0);
                } else {
                    SRV_DBG("%s", "[TOOLS DEBUG] Predict: After oaicompat_chat_params_parse - no tools in parsed_data\n");
                }

                // Extract the prompt from parsed data
                prompt_str = parsed_data.at("prompt").get<std::string>();

                // Preserve grammar from Go layer if it was provided (NoGrammar=false)
                // Otherwise, use grammar from parsed_data (template-generated when NoGrammar=true)
                json preserved_grammar;
                if (has_grammar_from_go && data.contains("grammar")) {
                    preserved_grammar = data["grammar"];
                }

                // Merge all fields from parsed_data into data (grammar, grammar_triggers, preserved_tokens, parse_tool_calls, etc.)
                // This ensures all template-generated fields are included
                // parse_tool_calls is set by oaicompat_chat_params_parse when tools are present
                for (const auto& item : parsed_data.items()) {
                    if (item.key() != "prompt") { // Don't overwrite prompt_str, we already extracted it
                        // If grammar was provided from Go layer, preserve it instead of template-generated grammar
                        if (item.key() == "grammar" && has_grammar_from_go && !preserved_grammar.is_null()) {
                            data["grammar"] = preserved_grammar;
                        } else {
                            data[item.key()] = item.value();
                        }
                    }
                }

                // Debug: Log parse_tool_calls if present (set by oaicompat_chat_params_parse when tools are present)
                if (data.contains("parse_tool_calls")) {
                    SRV_DBG("[TOOLS DEBUG] Predict: parse_tool_calls=%s\n", data["parse_tool_calls"].get<bool>() ? "true" : "false");
                }
            } else {
                // Use prompt directly from data
                if (data.contains("prompt") && data["prompt"].is_string()) {
                    prompt_str = data["prompt"].get<std::string>();
                } else {
                    prompt_str = request->prompt();
                }
            }

            const auto type = SERVER_TASK_TYPE_COMPLETION;
            // TODO: this log can become very long, put it behind a flag or think about a more compact format
            //SRV_DBG("Prompt: %s\n", prompt.is_string() ? prompt.get<std::string>().c_str() : prompt.dump(2).c_str());

            // If not using chat templates, extract files from image_data/audio_data fields
            // (If using chat templates, files were already extracted by oaicompat_chat_params_parse)
            if (!request->usetokenizertemplate() || request->messages_size() == 0 || ctx_server.impl->chat_params.tmpls == nullptr) {
                const auto &images_data = data.find("image_data");
                if (images_data != data.end() && images_data->is_array())
                {
                    std::cout << "[PREDICT] Processing " << images_data->size() << " images" << std::endl;
                    for (const auto &img : *images_data)
                    {
                        std::cout << "[PREDICT] Processing image" << std::endl;
                        auto decoded_data = base64_decode(img["data"].get<std::string>());
                        files.push_back(decoded_data);
                    }
                }

                const auto &audio_data = data.find("audio_data");
                if (audio_data != data.end() && audio_data->is_array())
                {
                    for (const auto &audio : *audio_data)
                    {
                        auto decoded_data = base64_decode(audio["data"].get<std::string>());
                        files.push_back(decoded_data);
                    }
                }

                const auto &video_data = data.find("video_data");
                if (video_data != data.end() && video_data->is_array())
                {
                    for (const auto &video : *video_data)
                    {
                        auto decoded_data = base64_decode(video["data"].get<std::string>());
                        files.push_back(decoded_data);
                    }
                }
            }

            // process files
            const bool has_mtmd = ctx_server.impl->mctx != nullptr;

            // process prompt
            std::vector<server_tokens> inputs;
            if (has_mtmd) {
                // multimodal
                inputs.push_back(process_mtmd_prompt(ctx_server.impl->mctx, prompt_str, files));
            } else {
                 // Everything else, including multimodal completions.
                inputs = tokenize_input_prompts(ctx_server.impl->vocab, ctx_server.impl->mctx, prompt_str, true, true);
            }

            tasks.reserve(inputs.size());
            for (size_t i = 0; i < inputs.size(); i++) {
                server_task task = server_task(type);

                task.id    = rd.queue_tasks.get_new_id();
                task.index = i;

                task.tokens    = std::move(inputs[i]);
#ifdef LOCALAI_HAS_SERVER_SCHEMA
                task.params           = server_schema::eval_llama_cmpl_schema(
#else
                task.params           = server_task::params_from_json_cmpl(
#endif
                        ctx_server.impl->vocab,
                        params_base,
                        ctx_server.get_meta().slot_n_ctx,
                        ctx_server.get_meta().logit_bias_eog,
                        data);
                task.id_slot = json_value(data, "id_slot", -1);

                // OAI-compat: enable autoparser (PEG-based chat parsing) so that
                // reasoning, tool calls, and content are classified into ChatDeltas.
                task.params.res_type                 = TASK_RESPONSE_TYPE_OAI_CHAT;
                task.params.oaicompat_cmpl_id         = completion_id;
                // oaicompat_model is already populated by eval_llama_cmpl_schema

                tasks.push_back(std::move(task));
            }

            rd.post_tasks(std::move(tasks));
        } catch (const std::exception & e) {
            return grpc::Status(grpc::StatusCode::INVALID_ARGUMENT, e.what());
        }


        std::cout << "[DEBUG] Waiting for results..." << std::endl;

        // Wait for all results
        auto all_results = rd.wait_for_all([&context]() { return context->IsCancelled(); });

        if (all_results.is_terminated) {
            return grpc::Status(grpc::StatusCode::CANCELLED, "Request cancelled by client");
        } else if (all_results.error) {
            std::cout << "[DEBUG] Error in results: " << all_results.error->to_json().value("message", "") << std::endl;
            reply->set_message(all_results.error->to_json().value("message", ""));
            return grpc::Status(grpc::StatusCode::INTERNAL, all_results.error->to_json().value("message", "Error occurred"));
        } else {
            std::cout << "[DEBUG] Received " << all_results.results.size() << " results" << std::endl;
            if (all_results.results.size() == 1) {
                // single result
                auto* final_res = dynamic_cast<server_task_result_cmpl_final*>(all_results.results[0].get());
                GGML_ASSERT(final_res != nullptr);
                json result_json = all_results.results[0]->to_json();

                // Handle both native format ({"content": "...", "tokens_predicted": N})
                // and OAI chat format ({"choices": [{"message": {"content": "..."}}],
                // "usage": {"completion_tokens": N, "prompt_tokens": N}}).
                std::string completion_text;
                int32_t tokens_predicted = 0;
                int32_t tokens_evaluated = 0;

                if (result_json.contains("choices")) {
                    // OAI chat format
                    const auto & choices = result_json.at("choices");
                    if (!choices.empty()) {
                        const auto & msg = choices[0].value("message", json::object());
                        if (msg.contains("content") && !msg.at("content").is_null()) {
                            completion_text = msg.at("content").get<std::string>();
                        }
                    }
                    if (result_json.contains("usage")) {
                        const auto & usage = result_json.at("usage");
                        tokens_predicted = usage.value("completion_tokens", 0);
                        tokens_evaluated = usage.value("prompt_tokens", 0);
                    }
                } else {
                    // Native llama.cpp format
                    completion_text = result_json.value("content", "");
                    tokens_predicted = result_json.value("tokens_predicted", 0);
                    tokens_evaluated = result_json.value("tokens_evaluated", 0);
                }
                reply->set_message(completion_text);
                reply->set_tokens(tokens_predicted);
                reply->set_prompt_tokens(tokens_evaluated);

                // Timings: present in both formats as a top-level "timings" object
                if (result_json.contains("timings")) {
                    reply->set_timing_prompt_processing(result_json.at("timings").value("prompt_ms", 0.0));
                    reply->set_timing_token_generation(result_json.at("timings").value("predicted_ms", 0.0));
                }

                // Logprobs: extract_logprobs_from_json handles both formats
                json logprobs_json = extract_logprobs_from_json(result_json);
                if (!logprobs_json.empty() && !logprobs_json.is_null()) {
                    reply->set_logprobs(logprobs_json.dump());
                }

                // Populate chat deltas from the autoparser's final parsed message
                if (final_res->is_updated) {
                    populate_chat_deltas_from_final(*reply, final_res->oaicompat_msg);
                }

            } else {
                // multiple results (multitask)
                json arr = json::array();
                json logprobs_arr = json::array();
                bool has_logprobs = false;
                for (auto & res : all_results.results) {
                    GGML_ASSERT(dynamic_cast<server_task_result_cmpl_final*>(res.get()) != nullptr);
                    json res_json = res->to_json();
                    // Handle both native and OAI chat formats
                    std::string result_content;
                    if (res_json.contains("choices")) {
                        const auto & choices = res_json.at("choices");
                        if (!choices.empty()) {
                            const auto & msg = choices[0].value("message", json::object());
                            if (msg.contains("content") && !msg.at("content").is_null()) {
                                result_content = msg.at("content").get<std::string>();
                            }
                        }
                    } else {
                        result_content = res_json.value("content", "");
                    }
                    arr.push_back(result_content);

                    // Extract logprobs for each result
                    json logprobs_json = extract_logprobs_from_json(res_json);
                    if (!logprobs_json.empty() && !logprobs_json.is_null()) {
                        has_logprobs = true;
                        logprobs_arr.push_back(logprobs_json);
                    } else {
                        logprobs_arr.push_back(json::object());
                    }
                }
                reply->set_message(arr);

                // Set logprobs if any result has them
                if (has_logprobs) {
                    std::string logprobs_str = logprobs_arr.dump();
                    reply->set_logprobs(logprobs_str);
                }
            }
        }

        std::cout << "[DEBUG] Predict request completed successfully" << std::endl;

        // Check if context was cancelled during processing
        if (context->IsCancelled()) {
            return grpc::Status(grpc::StatusCode::CANCELLED, "Request cancelled by client");
        }

        return grpc::Status::OK;
    }

    grpc::Status Embedding(ServerContext* context, const backend::PredictOptions* request, backend::EmbeddingResult* embeddingResult) override {
        auto auth = checkAuth(context);
        if (!auth.ok()) return auth;
        if (params_base.model.path.empty()) {
            return grpc::Status(grpc::StatusCode::FAILED_PRECONDITION, "Model not loaded");
        }
        conflict_guard guard("Embedding", slot_loop_inflight, score_inflight, "score_inflight");
        json body = parse_options(false, request, params_base, ctx_server.get_llama_context());

        body["stream"] = false;

        /*
        if (llama_pooling_type(ctx_server.ctx) == LLAMA_POOLING_TYPE_NONE) {
            return grpc::Status(grpc::StatusCode::INVALID_ARGUMENT, "Pooling type 'none' is not OAI compatible. Please use a different pooling type");
        }
        */

        // for the shape of input/content, see tokenize_input_prompts()
        json prompt = body.at("embeddings");


        auto tokenized_prompts = tokenize_input_prompts(ctx_server.impl->vocab, ctx_server.impl->mctx, prompt, true, true);
        for (const auto & tokens : tokenized_prompts) {
            // this check is necessary for models that do not add BOS token to the input
            if (tokens.empty()) {
                return grpc::Status(grpc::StatusCode::INVALID_ARGUMENT, "Input content cannot be empty");
            }
        }

        // Honor the load-time embd_normalize set via options:embd_normalize.
        // -1 none, 0 max-abs, 1 taxicab, 2 L2 (default), >2 p-norm.
        int embd_normalize = params_base.embd_normalize;
        // create and queue the task
        auto rd = ctx_server.get_response_reader();
        {
            std::vector<server_task> tasks;
            for (size_t i = 0; i < tokenized_prompts.size(); i++) {
                server_task task = server_task(SERVER_TASK_TYPE_EMBEDDING);

                task.id            = rd.queue_tasks.get_new_id();
                task.index         = i;
                task.tokens = std::move(tokenized_prompts[i]);

                task.params.res_type = TASK_RESPONSE_TYPE_NONE;
                task.params.embd_normalize = embd_normalize;
                tasks.push_back(std::move(task));
            }

            rd.post_tasks(std::move(tasks));
        }

        // Wait for all results
        auto all_results = rd.wait_for_all([&context]() { return context->IsCancelled(); });

        if (all_results.is_terminated) {
            return grpc::Status(grpc::StatusCode::CANCELLED, "Request cancelled by client");
        } else if (all_results.error) {
            return grpc::Status(grpc::StatusCode::INTERNAL, all_results.error->to_json().value("message", "Error in receiving results"));
        }

        // Collect responses
        json responses = json::array();
        for (auto & res : all_results.results) {
            GGML_ASSERT(dynamic_cast<server_task_result_embd*>(res.get()) != nullptr);
            responses.push_back(res->to_json());
        }

        std::cout << "[DEBUG] Responses size: " << responses.size() << std::endl;

        // Process the responses and extract embeddings
        for (const auto & response_elem : responses) {
            // Check if the response has an "embedding" field
            if (response_elem.contains("embedding")) {
                json embedding_data = json_value(response_elem, "embedding", json::array());

                if (embedding_data.is_array() && !embedding_data.empty()) {
                    for (const auto & embedding_vector : embedding_data) {
                        if (embedding_vector.is_array()) {
                            for (const auto & embedding_value : embedding_vector) {
                                embeddingResult->add_embeddings(embedding_value.get<float>());
                            }
                        }
                    }
                }
            } else {
                // Check if the response itself contains the embedding data directly
                if (response_elem.is_array()) {
                    for (const auto & embedding_value : response_elem) {
                        embeddingResult->add_embeddings(embedding_value.get<float>());
                    }
                }
            }
        }




        return grpc::Status::OK;
    }

    grpc::Status Rerank(ServerContext* context, const backend::RerankRequest* request, backend::RerankResult* rerankResult) override {
        if (!params_base.embedding || params_base.pooling_type != LLAMA_POOLING_TYPE_RANK) {
            return grpc::Status(grpc::StatusCode::UNIMPLEMENTED, "This server does not support reranking. Start it with `--reranking` and without `--embedding`");
        }

        // Validate request
        if (request->query().empty()) {
            return grpc::Status(grpc::StatusCode::INVALID_ARGUMENT, "\"query\" must be provided");
        }

        if (request->documents_size() == 0) {
            return grpc::Status(grpc::StatusCode::INVALID_ARGUMENT, "\"documents\" must be a non-empty string array");
        }

        conflict_guard guard("Rerank", slot_loop_inflight, score_inflight, "score_inflight");

        // Create and queue the task
        auto rd = ctx_server.get_response_reader();
        {
            std::vector<server_task> tasks;
            std::vector<std::string> documents;
            for (int i = 0; i < request->documents_size(); i++) {
                documents.push_back(request->documents(i));
            }

            tasks.reserve(documents.size());
            for (size_t i = 0; i < documents.size(); i++) {
                auto tmp = format_prompt_rerank(ctx_server.impl->model_tgt, ctx_server.impl->vocab, ctx_server.impl->mctx, request->query(), documents[i]);
                server_task task = server_task(SERVER_TASK_TYPE_RERANK);
                task.id = rd.queue_tasks.get_new_id();
                task.index = i;
                task.tokens = std::move(tmp);
                tasks.push_back(std::move(task));
            }

            rd.post_tasks(std::move(tasks));
        }

        // Wait for all results
        auto all_results = rd.wait_for_all([&context]() { return context->IsCancelled(); });

        if (all_results.is_terminated) {
            return grpc::Status(grpc::StatusCode::CANCELLED, "Request cancelled by client");
        } else if (all_results.error) {
            return grpc::Status(grpc::StatusCode::INTERNAL, all_results.error->to_json().value("message", "Error in receiving results"));
        }

        // Collect responses
        json responses = json::array();
        for (auto & res : all_results.results) {
            GGML_ASSERT(dynamic_cast<server_task_result_rerank*>(res.get()) != nullptr);
            responses.push_back(res->to_json());
        }
        // Sort responses by score in descending order
        std::sort(responses.begin(), responses.end(), [](const json& a, const json& b) {
            return a.value("score", 0.0f) > b.value("score", 0.0f);
        });

        // Crop results by request.top_n if specified
        int top_n = request->top_n();
        if (top_n > 0 && top_n < static_cast<int>(responses.size())) {
            responses = json(responses.begin(), responses.begin() + top_n);
        }
        // Set usage information
        backend::Usage* usage = rerankResult->mutable_usage();
        int total_tokens = 0;
        int prompt_tokens = 0;

        // Create document results
        for (const auto& response : responses) {
            backend::DocumentResult* doc_result = rerankResult->add_results();
            doc_result->set_index(response.value("index", 0));
            doc_result->set_text(request->documents(response.value("index", 0)));
            doc_result->set_relevance_score(response.value("score", 0.0f));

            // Add tokens evaluated for this document
            int tokens_evaluated = response.value("tokens_evaluated", 0);
            total_tokens += tokens_evaluated;
            prompt_tokens += tokens_evaluated;
        }

        // Set the total tokens in usage
        usage->set_total_tokens(total_tokens);
        usage->set_prompt_tokens(prompt_tokens);

        return grpc::Status::OK;
    }

    // Score returns the model's joint log-probability of each candidate
    // continuation given a shared prompt.
    //
    // WHY bypass the slot/task queue: upstream server_context exposes
    // get_llama_context as "main thread only" and the slot loop's
    // update_slots() owns the context whenever a task is in flight.
    // No public synchronization primitive is available — so Score is
    // unsafe to call concurrently with active generation through this
    // backend. In practice routing-classifier calls happen before the
    // request is routed to a generation backend, so the model used
    // for Score is typically idle. Concurrent Score calls are
    // serialised by a local mutex; KV-cache state is isolated behind
    // a dedicated sequence ID cleared between candidates.
    //
    // A patch to server-context.cpp that adds SERVER_TASK_TYPE_SCORE
    // and routes scoring through the slot loop would be the correct
    // long-term fix; tracked as a follow-up.
    //
    // Perf TODO (measured: ~450 ms warm for 3 candidates on Arch-
    // Router-1.5B Q4_K_M + Intel SYCL): the current loop re-decodes
    // `prompt + candidate` from scratch for every candidate, throwing
    // away the prompt's KV cache between iterations. A smarter
    // version would:
    //   1. Decode just the prompt once into score_seq_id.
    //   2. Snapshot/cp that sequence (llama_memory_seq_cp) into a
    //      per-candidate sequence id.
    //   3. For each candidate, decode only its tokens onto the copy
    //      (continuing from the saved prompt state), read logits.
    //   4. llama_memory_seq_rm the copy.
    // Estimated speedup: 3-candidate calls 450 ms -> ~150-200 ms,
    // 6-candidate calls 630 ms -> ~220 ms. Single source-file change,
    // no proto / Go-side changes needed. Worth doing once routing is
    // wired into the middleware and Score is on the hot path of every
    // chat request.
    grpc::Status Score(ServerContext* context, const backend::ScoreRequest* request, backend::ScoreResponse* response) override {
        auto auth = checkAuth(context);
        if (!auth.ok()) return auth;
        if (params_base.model.path.empty()) {
            return grpc::Status(grpc::StatusCode::FAILED_PRECONDITION, "Model not loaded");
        }
        if (request->candidates_size() == 0) {
            return grpc::Status(grpc::StatusCode::INVALID_ARGUMENT, "candidates must be non-empty");
        }

        // Tripwire against the slot loop. Acquired before score_mutex
        // so it fires even when this Score is queued behind another.
        conflict_guard guard("Score", score_inflight, slot_loop_inflight, "slot_loop_inflight");

        // Serialise concurrent Score calls. The slot loop is still
        // free to race with us — see the class comment above.
        static std::mutex score_mutex;
        std::lock_guard<std::mutex> score_lock(score_mutex);

        llama_context * lctx = ctx_server.get_llama_context();
        if (lctx == nullptr) {
            return grpc::Status(grpc::StatusCode::FAILED_PRECONDITION, "llama context unavailable (sleeping?)");
        }
        const llama_vocab * vocab = ctx_server.impl->vocab;
        const int32_t n_vocab = llama_vocab_n_tokens(vocab);
        const int32_t n_ctx = llama_n_ctx(lctx);
        llama_memory_t mem = llama_get_memory(lctx);

        // The KV-cache is sized to seq_to_stream.size() at load
        // (typically equal to n_slots, often 1). Sequence IDs must
        // be in [0, n_seq_max), so we can't pick a high-value
        // "private" ID — we have to share with the slot. We clear
        // the cache before AND after each candidate to keep
        // scoring isolated from whatever state the slot held, and
        // the static mutex above guarantees no other Score call is
        // racing in the meantime. The slot loop is still free to
        // race (see comment on this method) — Score must not run
        // concurrently with generation through this backend.
        const llama_seq_id score_seq_id = 0;
        llama_memory_seq_rm(mem, score_seq_id, -1, -1);

        // Tokenize the shared prompt once with add_special=true so
        // BOS is prepended when the model requires it. parse_special
        // keeps chat-template markers in the prompt intact.
        const std::string prompt = request->prompt();
        std::vector<llama_token> prompt_tokens = common_tokenize(vocab, prompt, /*add_special=*/true, /*parse_special=*/true);
        const int32_t prompt_len = (int32_t) prompt_tokens.size();

        for (int ci = 0; ci < request->candidates_size(); ci++) {
            const std::string & candidate_text = request->candidates(ci);

            // Re-tokenize prompt + candidate as a single string. BPE
            // merges across the boundary can shift the tokenization
            // versus tokenize(prompt) ++ tokenize(candidate), so we
            // find the divergence point against prompt_tokens.
            std::vector<llama_token> full_tokens = common_tokenize(vocab, prompt + candidate_text, /*add_special=*/true, /*parse_special=*/true);
            int32_t divergence = prompt_len;
            const int32_t min_len = std::min<int32_t>(prompt_len, (int32_t) full_tokens.size());
            for (int32_t i = 0; i < min_len; i++) {
                if (prompt_tokens[i] != full_tokens[i]) {
                    divergence = i;
                    break;
                }
            }
            const int32_t cand_len = (int32_t) full_tokens.size() - divergence;
            backend::CandidateScore * cs = response->add_candidates();
            cs->set_num_tokens(cand_len);
            if (cand_len <= 0) {
                cs->set_log_prob(0.0);
                if (request->length_normalize()) {
                    cs->set_length_normalized_log_prob(0.0);
                }
                continue;
            }
            if (divergence < 1) {
                // Need at least one prior token (typically BOS) to
                // predict the first candidate token's logit. Tokeniser
                // models without BOS + an empty prompt fall in here.
                return grpc::Status(grpc::StatusCode::INVALID_ARGUMENT,
                    "Score: prompt produced no leading tokens; need at least one (e.g. BOS) to predict candidate");
            }
            if ((int32_t) full_tokens.size() > n_ctx) {
                return grpc::Status(grpc::StatusCode::OUT_OF_RANGE,
                    "Score: prompt+candidate exceeds context size (got " +
                    std::to_string(full_tokens.size()) + ", n_ctx=" + std::to_string(n_ctx) + ")");
            }

            // Build a batch covering the entire prompt+candidate. We
            // need logits at (divergence-1) onward — those are the
            // predictions for each candidate token.
            llama_batch batch = llama_batch_init((int32_t) full_tokens.size(), 0, 1);
            for (int32_t i = 0; i < (int32_t) full_tokens.size(); i++) {
                batch.token[i]    = full_tokens[i];
                batch.pos[i]      = i;
                batch.n_seq_id[i] = 1;
                batch.seq_id[i][0] = score_seq_id;
                // logits[i] is "do we want the prediction *for the
                // next token*, computed from this position?"
                // We want predictions for candidate tokens at
                // positions divergence .. full_tokens.size()-1, which
                // come from logits at positions (divergence-1) ..
                // (full_tokens.size()-2).
                bool need_logit = (i >= divergence - 1) && (i < (int32_t) full_tokens.size() - 1);
                batch.logits[i] = need_logit ? 1 : 0;
            }
            batch.n_tokens = (int32_t) full_tokens.size();

            // Decode the batch. If decode fails (e.g. KV slot
            // exhaustion), surface as INTERNAL — the caller will
            // typically fall back to a sampling-based classifier.
            int decode_err = llama_decode(lctx, batch);
            if (decode_err != 0) {
                llama_batch_free(batch);
                llama_memory_seq_rm(mem, score_seq_id, -1, -1);
                return grpc::Status(grpc::StatusCode::INTERNAL,
                    "llama_decode failed during Score: " + std::to_string(decode_err));
            }

            // Sum log-probabilities of the actual candidate tokens.
            double total_log_prob = 0.0;
            for (int32_t k = 0; k < cand_len; k++) {
                // The k-th candidate token sits at full_tokens index
                // (divergence + k). Its predicting logit is at batch
                // position (divergence + k - 1).
                int32_t logit_pos = divergence + k - 1;
                const float * logits = llama_get_logits_ith(lctx, logit_pos);
                if (logits == nullptr) {
                    llama_batch_free(batch);
                    llama_memory_seq_rm(mem, score_seq_id, -1, -1);
                    return grpc::Status(grpc::StatusCode::INTERNAL,
                        "llama_get_logits_ith returned null at position " + std::to_string(logit_pos));
                }
                llama_token target_token = full_tokens[divergence + k];

                // Compute log_softmax(logits)[target_token] with the
                // max-subtraction stability trick.
                float max_logit = logits[0];
                for (int32_t v = 1; v < n_vocab; v++) {
                    if (logits[v] > max_logit) max_logit = logits[v];
                }
                double sum_exp = 0.0;
                for (int32_t v = 0; v < n_vocab; v++) {
                    sum_exp += std::exp((double)(logits[v] - max_logit));
                }
                double token_log_prob = (double)(logits[target_token] - max_logit) - std::log(sum_exp);
                total_log_prob += token_log_prob;

                if (request->include_token_logprobs()) {
                    backend::TokenLogProb * tlp = cs->add_tokens();
                    std::string piece = common_token_to_piece(lctx, target_token);
                    tlp->set_token(piece);
                    tlp->set_log_prob(token_log_prob);
                }
            }

            cs->set_log_prob(total_log_prob);
            if (request->length_normalize() && cand_len > 0) {
                cs->set_length_normalized_log_prob(total_log_prob / (double) cand_len);
            }

            llama_batch_free(batch);
            // Drop this candidate's KV-cache contribution so the next
            // candidate starts from a clean state. Without this, the
            // next decode would conflict at positions 0..N-1 for our
            // sequence ID.
            llama_memory_seq_rm(mem, score_seq_id, -1, -1);
        }

        return grpc::Status::OK;
    }

    grpc::Status TokenizeString(ServerContext* context, const backend::PredictOptions* request, backend::TokenizationResponse* response) override {
        auto auth = checkAuth(context);
        if (!auth.ok()) return auth;
        if (params_base.model.path.empty()) {
            return grpc::Status(grpc::StatusCode::FAILED_PRECONDITION, "Model not loaded");
        }
        conflict_guard guard("TokenizeString", slot_loop_inflight, score_inflight, "score_inflight");
        json body = parse_options(false, request, params_base, ctx_server.get_llama_context());
        body["stream"] = false;

        json tokens_response = json::array();
        if (body.count("prompt") != 0) {
            const bool add_special = json_value(body, "add_special", false);

            llama_tokens tokens = tokenize_mixed(ctx_server.impl->vocab, body.at("prompt"), add_special, true);


            for (const auto& token : tokens) {
                std::string piece = common_token_to_piece(ctx_server.get_llama_context(), token);
                response->add_tokens(token);
            }
        }

        return grpc::Status::OK;
    }

    grpc::Status GetMetrics(ServerContext* /*context*/, const backend::MetricsRequest* /*request*/, backend::MetricsResponse* response) override {

        conflict_guard guard("GetMetrics", slot_loop_inflight, score_inflight, "score_inflight");

// request slots data using task queue
        auto rd = ctx_server.get_response_reader();
        int task_id = rd.queue_tasks.get_new_id();
        {
            server_task task(SERVER_TASK_TYPE_METRICS);
            task.id = task_id;
            rd.queue_results.add_waiting_task_id(task_id);
            rd.queue_tasks.post(std::move(task), true); // high-priority task
        }

        // get the result
        server_task_result_ptr result = rd.queue_results.recv(task_id);
        rd.queue_results.remove_waiting_task_id(task_id);

        if (result->is_error()) {
            // Handle case when no active slot exists
            response->set_slot_id(0);
            response->set_prompt_json_for_slot("");
            response->set_tokens_per_second(0);
            response->set_tokens_generated(0);
            response->set_prompt_tokens_processed(0);
            return grpc::Status(grpc::StatusCode::INTERNAL, "Error in receiving results");
        }

        // TODO: get rid of this dynamic_cast
        auto res_metrics = dynamic_cast<server_task_result_metrics*>(result.get());
        GGML_ASSERT(res_metrics != nullptr);

        // Populate the response with metrics
        response->set_slot_id(0);
        response->set_prompt_json_for_slot("");
        response->set_tokens_per_second(res_metrics->n_prompt_tokens_processed ? 1.e3 / res_metrics->t_prompt_processing * res_metrics->n_prompt_tokens_processed : 0.);
        response->set_tokens_generated(res_metrics->n_tokens_predicted_total);
        response->set_prompt_tokens_processed(res_metrics->n_prompt_tokens_processed_total);


        return grpc::Status::OK;
    }

    grpc::Status ModelMetadata(ServerContext* /*context*/, const backend::ModelOptions* /*request*/, backend::ModelMetadataResponse* response) override {
        // Check if model is loaded
        if (params_base.model.path.empty()) {
            return grpc::Status(grpc::StatusCode::FAILED_PRECONDITION, "Model not loaded");
        }

        // Report the active multimodal media marker so the Go layer can emit the
        // same string when rendering prompts outside the tokenizer-template path.
        // Only meaningful when an mtmd context was initialized (vision/audio models).
        if (ctx_server.impl->mctx != nullptr) {
            response->set_media_marker(get_media_marker());
        }

        // Check if chat templates are initialized
        if (ctx_server.impl->chat_params.tmpls == nullptr) {
            // If templates are not initialized, we can't detect thinking support
            // Return false as default
            response->set_supports_thinking(false);
            response->set_rendered_template("");
            return grpc::Status::OK;
        }

        // Detect thinking support using llama.cpp's function
        bool supports_thinking = common_chat_templates_support_enable_thinking(ctx_server.impl->chat_params.tmpls.get());
        response->set_supports_thinking(supports_thinking);

        // Render the template with enable_thinking=true so Go code can detect thinking tokens
        // This allows reusing existing detection functions in Go
        std::string rendered_template = "";
        if (params_base.use_jinja) {
            // Render the template with enable_thinking=true to see what the actual prompt looks like
            common_chat_templates_inputs dummy_inputs;
            common_chat_msg msg;
            msg.role = "user";
            msg.content = "test";
            dummy_inputs.messages = {msg};
            dummy_inputs.enable_thinking = true;
            dummy_inputs.use_jinja = params_base.use_jinja;
            
            const auto rendered = common_chat_templates_apply(ctx_server.impl->chat_params.tmpls.get(), dummy_inputs);
            rendered_template = rendered.prompt;
        }
        
        response->set_rendered_template(rendered_template);

        // Run differential template analysis to detect tool format markers
        if (params_base.use_jinja) {
            try {
                // Get template source and reconstruct a common_chat_template for analysis
                std::string tmpl_src = common_chat_templates_source(ctx_server.impl->chat_params.tmpls.get());
                if (!tmpl_src.empty()) {
                    const auto * vocab = llama_model_get_vocab(ctx_server.impl->model_tgt);
                    std::string token_bos, token_eos;
                    if (vocab) {
                        auto bos_id = llama_vocab_bos(vocab);
                        auto eos_id = llama_vocab_eos(vocab);
                        if (bos_id != LLAMA_TOKEN_NULL) {
                            token_bos = common_token_to_piece(vocab, bos_id, true);
                        }
                        if (eos_id != LLAMA_TOKEN_NULL) {
                            token_eos = common_token_to_piece(vocab, eos_id, true);
                        }
                    }
                    common_chat_template tmpl(tmpl_src, token_bos, token_eos);
                    struct autoparser::autoparser ap;
                    ap.analyze_template(tmpl);

                    if (ap.analysis_complete && ap.tools.format.mode != autoparser::tool_format::NONE) {
                        auto * tf = response->mutable_tool_format();

                        // Format type
                        switch (ap.tools.format.mode) {
                            case autoparser::tool_format::JSON_NATIVE:
                                tf->set_format_type("json_native");
                                break;
                            case autoparser::tool_format::TAG_WITH_JSON:
                                tf->set_format_type("tag_with_json");
                                break;
                            case autoparser::tool_format::TAG_WITH_TAGGED:
                                tf->set_format_type("tag_with_tagged");
                                break;
                            default:
                                break;
                        }

                        // Tool section markers
                        tf->set_section_start(ap.tools.format.section_start);
                        tf->set_section_end(ap.tools.format.section_end);
                        tf->set_per_call_start(ap.tools.format.per_call_start);
                        tf->set_per_call_end(ap.tools.format.per_call_end);

                        // Function markers
                        tf->set_func_name_prefix(ap.tools.function.name_prefix);
                        tf->set_func_name_suffix(ap.tools.function.name_suffix);
                        tf->set_func_close(ap.tools.function.close);

                        // Argument markers
                        tf->set_arg_name_prefix(ap.tools.arguments.name_prefix);
                        tf->set_arg_name_suffix(ap.tools.arguments.name_suffix);
                        tf->set_arg_value_prefix(ap.tools.arguments.value_prefix);
                        tf->set_arg_value_suffix(ap.tools.arguments.value_suffix);
                        tf->set_arg_separator(ap.tools.arguments.separator);
                        tf->set_args_start(ap.tools.arguments.start);
                        tf->set_args_end(ap.tools.arguments.end);

                        // JSON format fields
                        tf->set_name_field(ap.tools.format.name_field);
                        tf->set_args_field(ap.tools.format.args_field);
                        tf->set_id_field(ap.tools.format.id_field);
                        tf->set_fun_name_is_key(ap.tools.format.fun_name_is_key);
                        tf->set_tools_array_wrapped(ap.tools.format.tools_array_wrapped);
                        tf->set_function_field(ap.tools.format.function_field);

                        tf->set_gen_id_field(ap.tools.format.gen_id_field);

                        for (const auto & p : ap.tools.format.parameter_order) {
                            tf->add_parameter_order(p);
                        }

                        // Call ID markers
                        switch (ap.tools.call_id.pos) {
                            case autoparser::call_id_position::NONE:
                                tf->set_call_id_position("none");
                                break;
                            case autoparser::call_id_position::PRE_FUNC_NAME:
                                tf->set_call_id_position("pre_func_name");
                                break;
                            case autoparser::call_id_position::BETWEEN_FUNC_AND_ARGS:
                                tf->set_call_id_position("between_func_and_args");
                                break;
                            case autoparser::call_id_position::POST_ARGS:
                                tf->set_call_id_position("post_args");
                                break;
                        }
                        tf->set_call_id_prefix(ap.tools.call_id.prefix);
                        tf->set_call_id_suffix(ap.tools.call_id.suffix);

                        // Reasoning markers
                        tf->set_reasoning_start(ap.reasoning.start);
                        tf->set_reasoning_end(ap.reasoning.end);

                        // Content markers
                        tf->set_content_start(ap.content.start);
                        tf->set_content_end(ap.content.end);
                    }
                }
            } catch (const std::exception & e) {
                SRV_WRN("ModelMetadata: failed to run autoparser analysis: %s\n", e.what());
            }
        }

        return grpc::Status::OK;
    }

    // runTranscriptionAsCompletion implements OAI /v1/audio/transcriptions on
    // top of the existing chat-completion + multimodal-audio pipeline, exactly
    // the way upstream llama.cpp's server does it (see
    // tools/server/server-context.cpp post_transcriptions_oai → forwards into
    // handle_completions_impl with a single user message attaching the audio
    // file via the mtmd marker).
    //
    // We synthesize a backend::PredictOptions with one user message
    // ("Transcribe audio to text" + optional language hint) and the audio
    // bytes attached via the existing PredictOptions.audios field, then
    // delegate to our own Predict() handler. This keeps every multimodal
    // codepath identical to the chat path and avoids duplicating ~700 lines
    // of task-construction logic.
    grpc::Status runTranscriptionAsCompletion(grpc::ServerContext* context,
                                              const backend::TranscriptRequest* request,
                                              backend::Reply* out_reply) {
        if (params_base.model.path.empty()) {
            return grpc::Status(grpc::StatusCode::FAILED_PRECONDITION, "Model not loaded");
        }
        if (request->dst().empty()) {
            return grpc::Status(grpc::StatusCode::INVALID_ARGUMENT, "dst (audio file path) is required");
        }

        // Read audio bytes from the path LocalAI's HTTP layer wrote.
        std::ifstream f(request->dst(), std::ios::binary);
        if (!f.is_open()) {
            return grpc::Status(grpc::StatusCode::INVALID_ARGUMENT, "failed to open audio file: " + request->dst());
        }
        std::vector<unsigned char> bytes((std::istreambuf_iterator<char>(f)),
                                          std::istreambuf_iterator<char>());
        f.close();
        if (bytes.empty()) {
            return grpc::Status(grpc::StatusCode::INVALID_ARGUMENT, "audio file is empty: " + request->dst());
        }

        std::string b64 = base64_encode_bytes(bytes.data(), bytes.size());

        // Build the same prompt upstream uses in convert_transcriptions_to_chatcmpl.
        std::string user_prompt = "Transcribe audio to text";
        if (!request->language().empty()) {
            user_prompt += " (language: " + request->language() + ")";
        }
        if (!request->prompt().empty()) {
            // Optional context hint from the caller.
            user_prompt += "\n" + request->prompt();
        }

        backend::PredictOptions synthetic;
        synthetic.set_usetokenizertemplate(true);
        synthetic.set_temperature(request->temperature());
        // Generation length: leave at 0 so parse_options uses -1 (model default).
        // The model's stop tokens / EOS handle termination naturally for ASR.
        backend::Message* msg = synthetic.add_messages();
        msg->set_role("user");
        msg->set_content(user_prompt);
        synthetic.add_audios(b64);

        return Predict(context, &synthetic, out_reply);
    }

    grpc::Status AudioTranscription(ServerContext* context,
                                    const backend::TranscriptRequest* request,
                                    backend::TranscriptResult* response) override {
        auto auth = checkAuth(context);
        if (!auth.ok()) return auth;

        backend::Reply reply;
        grpc::Status st = runTranscriptionAsCompletion(context, request, &reply);
        if (!st.ok()) {
            return st;
        }
        response->set_text(reply.message());
        if (!request->language().empty()) {
            response->set_language(request->language());
        }
        return grpc::Status::OK;
    }

    grpc::Status AudioTranscriptionStream(ServerContext* context,
                                          const backend::TranscriptRequest* request,
                                          grpc::ServerWriter<backend::TranscriptStreamResponse>* writer) override {
        auto auth = checkAuth(context);
        if (!auth.ok()) return auth;

        // Buffered streaming: run the transcription as a normal chat
        // completion, then emit one delta + one final event. Real
        // token-by-token streaming would require refactoring PredictStream's
        // 700-line writer-coupled body; the HTTP/SSE contract is identical
        // either way, and clients that only consume the assembled text don't
        // notice the difference.
        backend::Reply reply;
        grpc::Status st = runTranscriptionAsCompletion(context, request, &reply);
        if (!st.ok()) {
            return st;
        }

        const std::string& text = reply.message();
        if (!text.empty()) {
            backend::TranscriptStreamResponse delta_chunk;
            delta_chunk.set_delta(text);
            writer->Write(delta_chunk);
        }

        backend::TranscriptStreamResponse final_chunk;
        backend::TranscriptResult* final_result = final_chunk.mutable_final_result();
        final_result->set_text(text);
        if (!request->language().empty()) {
            final_result->set_language(request->language());
        }
        writer->Write(final_chunk);
        return grpc::Status::OK;
    }
};


int main(int argc, char** argv) {
  std::string server_address("localhost:50051");

  // Define long and short options
  struct option long_options[] = {
      {"addr", required_argument, nullptr, 'a'},
      {nullptr, 0, nullptr, 0}
  };

  // Parse command-line arguments
  int option;
  int option_index = 0;
  while ((option = getopt_long(argc, argv, "a:", long_options, &option_index)) != -1) {
    switch (option) {
      case 'a':
        server_address = optarg;
        break;
      default:
        std::cerr << "Usage: " << argv[0] << " [--addr=<address>] or [-a <address>]" << std::endl;
        return 1;
    }
  }

    // Best-effort backstop: self-terminate if the LocalAI process that spawned
    // us dies without cleaning us up (see parent_watch.h).
    llama_grpc::start_parent_death_watcher();

    server_context ctx_server;
    BackendServiceImpl service(ctx_server);

    ServerBuilder builder;
    builder.AddListeningPort(server_address, grpc::InsecureServerCredentials());

    // Initialize bearer token auth if LOCALAI_GRPC_AUTH_TOKEN is set
    const char* auth_token = std::getenv("LOCALAI_GRPC_AUTH_TOKEN");
    if (auth_token != nullptr && auth_token[0] != '\0') {
        g_grpc_auth_token = auth_token;
        std::cout << "gRPC auth enabled via LOCALAI_GRPC_AUTH_TOKEN" << std::endl;
    }
    builder.RegisterService(&service);
    builder.SetMaxMessageSize(50 * 1024 * 1024); // 50MB
    builder.SetMaxSendMessageSize(50 * 1024 * 1024); // 50MB
    builder.SetMaxReceiveMessageSize(50 * 1024 * 1024); // 50MB

    std::unique_ptr<Server> server(builder.BuildAndStart());
   // run the HTTP server in a thread - see comment below
    std::thread t([&]()
    {
        std::cout << "Server listening on " << server_address << std::endl;
        server->Wait();
        return 0;
    });

    // clean up function, to be called before exit
    auto clean_up = [&server, &ctx_server]() {
        SRV_INF("%s: cleaning up before exit...\n", __func__);
        server->Shutdown();
        ctx_server.terminate();
        llama_backend_free();
    };


    //);
    start_llama_server(ctx_server);
    std::cout << "stopping" << std::endl;


    clean_up();
    t.join();

    return 0;
}
