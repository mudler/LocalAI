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
#include "server-context.cpp"

// LocalAI

#include "backend.pb.h"
#include "backend.grpc.pb.h"
#include "common.h"
#include <getopt.h>
#include <grpcpp/ext/proto_server_reflection_plugin.h>
#include <grpcpp/grpcpp.h>
#include <grpcpp/health_check_service_interface.h>
#include <regex>
#include <atomic>
#include <signal.h>
#include <thread>

#if defined(_WIN32)
#include <windows.h>
#endif


using grpc::Server;
using grpc::ServerBuilder;
using grpc::ServerContext;
using grpc::Status;
// END LocalAI


/////////////////////////////////
////////////////////////////////
//////// LOCALAI code starts below here
/////////////////////////////////
////////////////////////////////

bool loaded_model; // TODO: add a mutex for this, but happens only once loading the model

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

    ctx_server.init();
    //state.store(SERVER_STATE_READY);

    LOG_INF("%s: model loaded\n", __func__);

    // print sample chat example to make it clear which template is used
    // LOG_INF("%s: chat template, chat_template: %s, example_format: '%s'\n", __func__,
    //     common_chat_templates_source(ctx_server.impl->chat_templates.get()),
    //     common_chat_format_example(ctx_server.impl->chat_templates.get(), ctx_server.impl->params_base.use_jinja).c_str(), ctx_server.impl->params_base.default_template_kwargs);

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
// https://github.com/ggerganov/llama.cpp/compare/4dbc8b9cb71876e005724f4e8f73a3544646bcf5..3edfa7d3753c29e44b964c0ff424d2ea8d5fdee6
static void add_rpc_devices(std::string servers) {
    auto rpc_servers = string_split<std::string>(servers, ',');
    if (rpc_servers.empty()) {
        throw std::invalid_argument("no RPC servers specified");
    }
    ggml_backend_reg_t rpc_reg = ggml_backend_reg_by_name("RPC");
    if (!rpc_reg) {
        throw std::invalid_argument("failed to find RPC backend");
    }
    typedef ggml_backend_dev_t (*ggml_backend_rpc_add_device_t)(const char * endpoint);
    ggml_backend_rpc_add_device_t ggml_backend_rpc_add_device_fn = (ggml_backend_rpc_add_device_t) ggml_backend_reg_get_proc_address(rpc_reg, "ggml_backend_rpc_add_device");
    if (!ggml_backend_rpc_add_device_fn) {
        throw std::invalid_argument("failed to find RPC device add function");
    }
    for (const auto & server : rpc_servers) {
        ggml_backend_dev_t dev = ggml_backend_rpc_add_device_fn(server.c_str());
        if (dev) {
            ggml_backend_device_register(dev);
        } else {
            throw std::invalid_argument("failed to register RPC device");
        }
    }
}

static void params_parse(server_context& ctx_server, const backend::ModelOptions* request,
                                common_params & params) {
   
    // this is comparable to: https://github.com/ggerganov/llama.cpp/blob/d9b33fe95bd257b36c84ee5769cc048230067d6f/examples/server/server.cpp#L1809

    params.model.path = request->modelfile();
    if (!request->mmproj().empty()) {
    // get the directory of modelfile
      std::string model_dir = params.model.path.substr(0, params.model.path.find_last_of("/\\"));
      params.mmproj.path = model_dir + "/"+ request->mmproj();
    }
    //  params.model_alias ??
    params.model_alias =  request->modelfile();
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

    if (!params.kv_overrides.empty()) {
        params.kv_overrides.emplace_back();
        params.kv_overrides.back().key[0] = 0;
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
}


// GRPC Server start
class BackendServiceImpl final : public backend::Backend::Service {
private:
    server_context& ctx_server;
    const common_params* params_base_ptr; // Store pointer to params_base, set after model load

public:
    BackendServiceImpl(server_context& ctx) : ctx_server(ctx), params_base_ptr(nullptr) {}

    grpc::Status Health(ServerContext* context, const backend::HealthMessage* request, backend::Reply* reply) {
        // Implement Health RPC
        reply->set_message("OK");
        return Status::OK;
    }

    grpc::Status LoadModel(ServerContext* context, const backend::ModelOptions* request, backend::Result* result) {
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
        // load the model
        if (!ctx_server.load_model(params)) {
            result->set_message("Failed loading model");
            result->set_success(false);
            return Status::CANCELLED;
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
            // Update the grammar triggers in params_base
            ctx_server.impl->params_base.sampling.grammar_triggers = std::move(processed_triggers);
            // Also update preserved_tokens in params_base
            ctx_server.impl->params_base.sampling.preserved_tokens = params.sampling.preserved_tokens;
        }

        //ctx_server.init();
        result->set_message("Loading succeeded");
        result->set_success(true);
        loaded_model = true;
        ctx_server.impl->slot_prompt_similarity = params.slot_prompt_similarity;
        // Store pointer to params_base for use in parse_options
        params_base_ptr = &ctx_server.impl->params_base;

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

    grpc::Status PredictStream(grpc::ServerContext* context, const backend::PredictOptions* request, grpc::ServerWriter<backend::Reply>* writer) override {
        if (!params_base_ptr) {
            return grpc::Status(grpc::StatusCode::FAILED_PRECONDITION, "Model not loaded");
        }
        json data = parse_options(true, request, *params_base_ptr, ctx_server.get_llama_context());


        //Raise error if embeddings is set to true
        if (ctx_server.impl->params_base.embedding) {
            return grpc::Status(grpc::StatusCode::INVALID_ARGUMENT, "Embedding is not supported in streaming mode");
        }


        auto completion_id = gen_chatcmplid();
        // need to store the reader as a pointer, so that it won't be destroyed when the handle returns
        auto queues = ctx_server.get_queues();
        const auto rd = std::make_shared<server_response_reader>(queues, 1); // HTTP_POLLING_SECONDS = 1
        try {
            std::vector<server_task> tasks;

            std::string prompt_str;
            std::vector<raw_buffer> files; // Declare files early so it's accessible in both branches
            // Handle chat templates when UseTokenizerTemplate is enabled and Messages are provided
            if (request->usetokenizertemplate() && request->messages_size() > 0 && ctx_server.impl->chat_templates != nullptr) {
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
                    json msg_json;
                    msg_json["role"] = msg.role();
                    
                    bool is_last_user_msg = (i == last_user_msg_idx);
                    bool has_images_or_audio = (request->images_size() > 0 || request->audios_size() > 0);
                    
                    // Handle content - can be string, null, or array
                    // For multimodal content, we'll embed images/audio from separate fields
                    if (!msg.content().empty()) {
                        // Try to parse content as JSON to see if it's already an array
                        json content_val;
                        try {
                            content_val = json::parse(msg.content());
                            // Handle null values - convert to empty string to avoid template errors
                            if (content_val.is_null()) {
                                content_val = "";
                            }
                        } catch (const json::parse_error&) {
                            // Not JSON, treat as plain string
                            content_val = msg.content();
                        }
                        
                        // If content is an object (e.g., from tool call failures), convert to string
                        if (content_val.is_object()) {
                            content_val = content_val.dump();
                        }
                        
                        // If content is a string and this is the last user message with images/audio, combine them
                        if (content_val.is_string() && is_last_user_msg && has_images_or_audio) {
                            json content_array = json::array();
                            // Add text first
                            content_array.push_back({{"type", "text"}, {"text", content_val.get<std::string>()}});
                            // Add images
                            if (request->images_size() > 0) {
                                for (int j = 0; j < request->images_size(); j++) {
                                    json image_chunk;
                                    image_chunk["type"] = "image_url";
                                    json image_url;
                                    image_url["url"] = "data:image/jpeg;base64," + request->images(j);
                                    image_chunk["image_url"] = image_url;
                                    content_array.push_back(image_chunk);
                                }
                            }
                            // Add audios
                            if (request->audios_size() > 0) {
                                for (int j = 0; j < request->audios_size(); j++) {
                                    json audio_chunk;
                                    audio_chunk["type"] = "input_audio";
                                    json input_audio;
                                    input_audio["data"] = request->audios(j);
                                    input_audio["format"] = "wav"; // default, could be made configurable
                                    audio_chunk["input_audio"] = input_audio;
                                    content_array.push_back(audio_chunk);
                                }
                            }
                            msg_json["content"] = content_array;
                        } else {
                            // Use content as-is (already array or not last user message)
                            // Ensure null values are converted to empty string
                            if (content_val.is_null()) {
                                msg_json["content"] = "";
                            } else {
                                msg_json["content"] = content_val;
                            }
                        }
                    } else if (is_last_user_msg && has_images_or_audio) {
                        // If no content but this is the last user message with images/audio, create content array
                        json content_array = json::array();
                        if (request->images_size() > 0) {
                            for (int j = 0; j < request->images_size(); j++) {
                                json image_chunk;
                                image_chunk["type"] = "image_url";
                                json image_url;
                                image_url["url"] = "data:image/jpeg;base64," + request->images(j);
                                image_chunk["image_url"] = image_url;
                                content_array.push_back(image_chunk);
                            }
                        }
                        if (request->audios_size() > 0) {
                            for (int j = 0; j < request->audios_size(); j++) {
                                json audio_chunk;
                                audio_chunk["type"] = "input_audio";
                                json input_audio;
                                input_audio["data"] = request->audios(j);
                                input_audio["format"] = "wav"; // default, could be made configurable
                                audio_chunk["input_audio"] = input_audio;
                                content_array.push_back(audio_chunk);
                            }
                        }
                        msg_json["content"] = content_array;
                    } else if (msg.role() == "tool") {
                        // Tool role messages must have content field set, even if empty
                        // Jinja templates expect content to be a string, not null or object
                        SRV_INF("[CONTENT DEBUG] PredictStream: Message %d is tool role, content_empty=%d\n", i, msg.content().empty() ? 1 : 0);
                        if (msg.content().empty()) {
                            msg_json["content"] = "";
                            SRV_INF("[CONTENT DEBUG] PredictStream: Message %d (tool): empty content, set to empty string\n", i);
                        } else {
                            SRV_INF("[CONTENT DEBUG] PredictStream: Message %d (tool): content exists: %s\n", 
                                    i, msg.content().substr(0, std::min<size_t>(200, msg.content().size())).c_str());
                            // Content exists, parse and ensure it's a string
                            json content_val;
                            try {
                                content_val = json::parse(msg.content());
                                SRV_INF("[CONTENT DEBUG] PredictStream: Message %d (tool): parsed JSON, type=%s\n", 
                                        i, content_val.is_null() ? "null" : 
                                           content_val.is_object() ? "object" :
                                           content_val.is_string() ? "string" :
                                           content_val.is_array() ? "array" : "other");
                                // Handle null values - Jinja templates expect content to be a string, not null
                                if (content_val.is_null()) {
                                    msg_json["content"] = "";
                                    SRV_INF("[CONTENT DEBUG] PredictStream: Message %d (tool): null content, converted to empty string\n", i);
                                } else if (content_val.is_object()) {
                                    // If content is an object (e.g., from tool call failures/errors), convert to string
                                    msg_json["content"] = content_val.dump();
                                    SRV_INF("[CONTENT DEBUG] PredictStream: Message %d (tool): object content, converted to string: %s\n", 
                                            i, content_val.dump().substr(0, std::min<size_t>(200, content_val.dump().size())).c_str());
                                } else if (content_val.is_string()) {
                                    msg_json["content"] = content_val.get<std::string>();
                                    SRV_INF("[CONTENT DEBUG] PredictStream: Message %d (tool): string content, using as-is\n", i);
                                } else {
                                    // For arrays or other types, convert to string
                                    msg_json["content"] = content_val.dump();
                                    SRV_INF("[CONTENT DEBUG] PredictStream: Message %d (tool): %s content, converted to string\n", 
                                            i, content_val.is_array() ? "array" : "other type");
                                }
                            } catch (const json::parse_error&) {
                                // Not JSON, treat as plain string
                                msg_json["content"] = msg.content();
                                SRV_INF("[CONTENT DEBUG] PredictStream: Message %d (tool): not JSON, using as string\n", i);
                            }
                        }
                    } else {
                        // Ensure all messages have content set (fallback for any unhandled cases)
                        // Jinja templates expect content to be present, default to empty string if not set
                        if (!msg_json.contains("content")) {
                            SRV_INF("[CONTENT DEBUG] PredictStream: Message %d (role=%s): no content field, adding empty string\n", 
                                    i, msg.role().c_str());
                            msg_json["content"] = "";
                        }
                    }
                    
                    // Add optional fields for OpenAI-compatible message format
                    if (!msg.name().empty()) {
                        msg_json["name"] = msg.name();
                    }
                    if (!msg.tool_call_id().empty()) {
                        msg_json["tool_call_id"] = msg.tool_call_id();
                    }
                    if (!msg.reasoning_content().empty()) {
                        msg_json["reasoning_content"] = msg.reasoning_content();
                    }
                    if (!msg.tool_calls().empty()) {
                        // Parse tool_calls JSON string and add to message
                        try {
                            json tool_calls = json::parse(msg.tool_calls());
                            msg_json["tool_calls"] = tool_calls;
                            SRV_INF("[TOOL CALLS DEBUG] PredictStream: Message %d has tool_calls: %s\n", i, tool_calls.dump().c_str());
                            // IMPORTANT: If message has tool_calls but content is empty or not set,
                            // set content to space " " instead of empty string "", because llama.cpp's
                            // common_chat_msgs_to_json_oaicompat converts empty strings to null (line 312),
                            // which causes template errors when accessing message.content[:tool_start_length]
                            if (!msg_json.contains("content") || (msg_json.contains("content") && msg_json["content"].is_string() && msg_json["content"].get<std::string>().empty())) {
                                SRV_INF("[CONTENT DEBUG] PredictStream: Message %d has tool_calls but empty content, setting to space\n", i);
                                msg_json["content"] = " ";
                            }
                            // Log each tool call with name and arguments
                            if (tool_calls.is_array()) {
                                for (size_t tc_idx = 0; tc_idx < tool_calls.size(); tc_idx++) {
                                    const auto& tc = tool_calls[tc_idx];
                                    std::string tool_name = "unknown";
                                    std::string tool_args = "{}";
                                    if (tc.contains("function")) {
                                        const auto& func = tc["function"];
                                        if (func.contains("name")) {
                                            tool_name = func["name"].get<std::string>();
                                        }
                                        if (func.contains("arguments")) {
                                            tool_args = func["arguments"].is_string() ? 
                                                func["arguments"].get<std::string>() : 
                                                func["arguments"].dump();
                                        }
                                    } else if (tc.contains("name")) {
                                        tool_name = tc["name"].get<std::string>();
                                        if (tc.contains("arguments")) {
                                            tool_args = tc["arguments"].is_string() ? 
                                                tc["arguments"].get<std::string>() : 
                                                tc["arguments"].dump();
                                        }
                                    }
                                    SRV_INF("[TOOL CALLS DEBUG] PredictStream: Message %d, tool_call %zu: name=%s, arguments=%s\n", 
                                            i, tc_idx, tool_name.c_str(), tool_args.c_str());
                                }
                            }
                        } catch (const json::parse_error& e) {
                            SRV_WRN("Failed to parse tool_calls JSON: %s\n", e.what());
                        }
                    }
                    
                    // Debug: Log final content state before adding to array
                    if (msg_json.contains("content")) {
                        if (msg_json["content"].is_null()) {
                            SRV_INF("[CONTENT DEBUG] PredictStream: Message %d FINAL STATE: content is NULL - THIS WILL CAUSE ERROR!\n", i);
                        } else {
                            SRV_INF("[CONTENT DEBUG] PredictStream: Message %d FINAL STATE: content type=%s, has_value=%d\n", 
                                    i, msg_json["content"].is_string() ? "string" :
                                       msg_json["content"].is_array() ? "array" :
                                       msg_json["content"].is_object() ? "object" : "other",
                                    msg_json["content"].is_null() ? 0 : 1);
                        }
                    } else {
                        SRV_INF("[CONTENT DEBUG] PredictStream: Message %d FINAL STATE: NO CONTENT FIELD - THIS WILL CAUSE ERROR!\n", i);
                    }
                    
                    messages_json.push_back(msg_json);
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

                // Debug: Print full body_json before template processing (includes messages, tools, tool_choice, etc.)
                SRV_DBG("[CONVERSATION DEBUG] PredictStream: Full body_json before oaicompat_chat_params_parse:\n%s\n", body_json.dump(2).c_str());

                // Use the same approach as server.cpp: call oaicompat_chat_params_parse
                // This handles all template application, grammar merging, etc. automatically
                // Files extracted from multimodal content in messages will be added to the files vector
                // Create parser options with current chat_templates to ensure tmpls is not null
                oaicompat_parser_options parser_opt = ctx_server.impl->oai_parser_opt;
                parser_opt.tmpls = ctx_server.impl->chat_templates.get(); // Ensure tmpls is set to current chat_templates
                // Update allow_image and allow_audio based on current mctx state
                parser_opt.allow_image = ctx_server.impl->mctx ? mtmd_support_vision(ctx_server.impl->mctx) : false;
                parser_opt.allow_audio = ctx_server.impl->mctx ? mtmd_support_audio(ctx_server.impl->mctx) : false;
                
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
                        auto& msg = body_json["messages"][idx];
                        std::string role_str = msg.contains("role") ? msg["role"].get<std::string>() : "unknown";
                        if (msg.contains("content")) {
                            if (msg["content"].is_null()) {
                                SRV_INF("[CONTENT DEBUG] PredictStream: BEFORE TEMPLATE - Message %zu (role=%s) has NULL content - FIXING!\n", idx, role_str.c_str());
                                msg["content"] = ""; // Fix null content
                            } else if (!msg["content"].is_string() && !msg["content"].is_array()) {
                                // If content is object or other non-string type, convert to string for templates
                                SRV_INF("[CONTENT DEBUG] PredictStream: BEFORE TEMPLATE - Message %zu (role=%s) content is not string/array, converting\n", idx, role_str.c_str());
                                if (msg["content"].is_object()) {
                                    msg["content"] = msg["content"].dump();
                                } else {
                                    msg["content"] = "";
                                }
                            } else {
                                SRV_INF("[CONTENT DEBUG] PredictStream: BEFORE TEMPLATE - Message %zu (role=%s): content type=%s\n", 
                                        idx, role_str.c_str(),
                                        msg["content"].is_string() ? "string" :
                                        msg["content"].is_array() ? "array" :
                                        msg["content"].is_object() ? "object" : "other");
                            }
                        } else {
                            SRV_INF("[CONTENT DEBUG] PredictStream: BEFORE TEMPLATE - Message %zu (role=%s) MISSING content field - ADDING!\n", idx, role_str.c_str());
                            msg["content"] = ""; // Add missing content
                        }
                    }
                }
                
                json parsed_data = oaicompat_chat_params_parse(body_json, parser_opt, files);
                
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

            const auto & prompt = prompt_str;
            const auto type = SERVER_TASK_TYPE_COMPLETION;
            // TODO: this log can become very long, put it behind a flag or think about a more compact format
            //SRV_DBG("Prompt: %s\n", prompt.is_string() ? prompt.get<std::string>().c_str() : prompt.dump(2).c_str());

            // If not using chat templates, extract files from image_data/audio_data fields
            // (If using chat templates, files were already extracted by oaicompat_chat_params_parse)
            if (!request->usetokenizertemplate() || request->messages_size() == 0 || ctx_server.impl->chat_templates == nullptr) {
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

                task.id    = queues.first.get_new_id();
                task.index = i;

                task.tokens    = std::move(inputs[i]);
                task.params           = server_task::params_from_json_cmpl(
                        ctx_server.get_llama_context(),
                        ctx_server.impl->params_base,
                        data);
                task.id_slot = json_value(data, "id_slot", -1);

                // OAI-compat
                task.params.res_type                 = TASK_RESPONSE_TYPE_NONE;
                task.params.oaicompat_cmpl_id         = completion_id;
                // oaicompat_model is already populated by params_from_json_cmpl

                tasks.push_back(std::move(task));
            }

            rd->post_tasks(std::move(tasks));
        } catch (const std::exception & e) {
            return grpc::Status(grpc::StatusCode::INVALID_ARGUMENT, e.what());
        }

        // Get first result for error checking (following server.cpp pattern)
        server_task_result_ptr first_result = rd->next([&context]() { return context->IsCancelled(); });
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

        // Process first result
        json first_res_json = first_result->to_json();
        if (first_res_json.is_array()) {
            for (const auto & res : first_res_json) {
                std::string completion_text = res.value("content", "");

                backend::Reply reply;
                reply.set_message(completion_text);
                int32_t tokens_predicted = res.value("tokens_predicted", 0);
                reply.set_tokens(tokens_predicted);
                int32_t tokens_evaluated = res.value("tokens_evaluated", 0);
                reply.set_prompt_tokens(tokens_evaluated);

                if (res.contains("timings")) {
                    double timing_prompt_processing = res.at("timings").value("prompt_ms", 0.0);
                    reply.set_timing_prompt_processing(timing_prompt_processing);
                    double timing_token_generation = res.at("timings").value("predicted_ms", 0.0);
                    reply.set_timing_token_generation(timing_token_generation);
                }

                // Extract and set logprobs if present
                json logprobs_json = extract_logprobs_from_json(res);
                if (!logprobs_json.empty() && !logprobs_json.is_null()) {
                    std::string logprobs_str = logprobs_json.dump();
                    reply.set_logprobs(logprobs_str);
                }

                writer->Write(reply);
            }
        } else {
            std::string completion_text = first_res_json.value("content", "");

            backend::Reply reply;
            reply.set_message(completion_text);
            int32_t tokens_predicted = first_res_json.value("tokens_predicted", 0);
            reply.set_tokens(tokens_predicted);
            int32_t tokens_evaluated = first_res_json.value("tokens_evaluated", 0);
            reply.set_prompt_tokens(tokens_evaluated);

            if (first_res_json.contains("timings")) {
                double timing_prompt_processing = first_res_json.at("timings").value("prompt_ms", 0.0);
                reply.set_timing_prompt_processing(timing_prompt_processing);
                double timing_token_generation = first_res_json.at("timings").value("predicted_ms", 0.0);
                reply.set_timing_token_generation(timing_token_generation);
            }

            // Extract and set logprobs if present
            json logprobs_json = extract_logprobs_from_json(first_res_json);
            if (!logprobs_json.empty() && !logprobs_json.is_null()) {
                std::string logprobs_str = logprobs_json.dump();
                reply.set_logprobs(logprobs_str);
            }

            writer->Write(reply);
        }

        // Process subsequent results
        while (rd->has_next()) {
            // Check if context is cancelled before processing result
            if (context->IsCancelled()) {
                break;
            }

            auto result = rd->next([&context]() { return context->IsCancelled(); });
            if (result == nullptr) {
                // connection is closed
                break;
            }

            json res_json = result->to_json();
            if (res_json.is_array()) {
                for (const auto & res : res_json) {
                    std::string completion_text = res.value("content", "");

                    backend::Reply reply;
                    reply.set_message(completion_text);
                    int32_t tokens_predicted = res.value("tokens_predicted", 0);
                    reply.set_tokens(tokens_predicted);
                    int32_t tokens_evaluated = res.value("tokens_evaluated", 0);
                    reply.set_prompt_tokens(tokens_evaluated);

                    if (res.contains("timings")) {
                        double timing_prompt_processing = res.at("timings").value("prompt_ms", 0.0);
                        reply.set_timing_prompt_processing(timing_prompt_processing);
                        double timing_token_generation = res.at("timings").value("predicted_ms", 0.0);
                        reply.set_timing_token_generation(timing_token_generation);
                    }

                    // Extract and set logprobs if present
                    json logprobs_json = extract_logprobs_from_json(res);
                    if (!logprobs_json.empty() && !logprobs_json.is_null()) {
                        std::string logprobs_str = logprobs_json.dump();
                        reply.set_logprobs(logprobs_str);
                    }

                    writer->Write(reply);
                }
            } else {
                std::string completion_text = res_json.value("content", "");

                backend::Reply reply;
                reply.set_message(completion_text);
                int32_t tokens_predicted = res_json.value("tokens_predicted", 0);
                reply.set_tokens(tokens_predicted);
                int32_t tokens_evaluated = res_json.value("tokens_evaluated", 0);
                reply.set_prompt_tokens(tokens_evaluated);

                if (res_json.contains("timings")) {
                    double timing_prompt_processing = res_json.at("timings").value("prompt_ms", 0.0);
                    reply.set_timing_prompt_processing(timing_prompt_processing);
                    double timing_token_generation = res_json.at("timings").value("predicted_ms", 0.0);
                    reply.set_timing_token_generation(timing_token_generation);
                }

                // Extract and set logprobs if present
                json logprobs_json = extract_logprobs_from_json(res_json);
                if (!logprobs_json.empty() && !logprobs_json.is_null()) {
                    std::string logprobs_str = logprobs_json.dump();
                    reply.set_logprobs(logprobs_str);
                }

                writer->Write(reply);
            }
        }

        // Check if context was cancelled during processing
        if (context->IsCancelled()) {
            return grpc::Status(grpc::StatusCode::CANCELLED, "Request cancelled by client");
        }

        return grpc::Status::OK;
    }

    grpc::Status Predict(ServerContext* context, const backend::PredictOptions* request, backend::Reply* reply) {
         if (!params_base_ptr) {
             return grpc::Status(grpc::StatusCode::FAILED_PRECONDITION, "Model not loaded");
         }
         json data = parse_options(true, request, *params_base_ptr, ctx_server.get_llama_context());

        data["stream"] = false;
        //Raise error if embeddings is set to true
        if (ctx_server.impl->params_base.embedding) {
            return grpc::Status(grpc::StatusCode::INVALID_ARGUMENT, "Embedding is not supported in Predict mode");
        }
        std::cout << "[PREDICT] Received result: " << data.dump(2) << std::endl;
        auto completion_id = gen_chatcmplid();
        auto queues = ctx_server.get_queues();
        const auto rd = std::make_shared<server_response_reader>(queues, 1); // HTTP_POLLING_SECONDS = 1
        try {
            std::vector<server_task> tasks;

            std::string prompt_str;
            std::vector<raw_buffer> files; // Declare files early so it's accessible in both branches
            // Handle chat templates when UseTokenizerTemplate is enabled and Messages are provided
            if (request->usetokenizertemplate() && request->messages_size() > 0 && ctx_server.impl->chat_templates != nullptr) {
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
                    json msg_json;
                    msg_json["role"] = msg.role();
                    
                    SRV_INF("[CONTENT DEBUG] Predict: Message %d: role=%s, content_empty=%d, content_length=%zu\n", 
                            i, msg.role().c_str(), msg.content().empty() ? 1 : 0, msg.content().size());
                    if (!msg.content().empty()) {
                        SRV_INF("[CONTENT DEBUG] Predict: Message %d content (first 200 chars): %s\n", 
                                i, msg.content().substr(0, std::min<size_t>(200, msg.content().size())).c_str());
                    }
                    
                    bool is_last_user_msg = (i == last_user_msg_idx);
                    bool has_images_or_audio = (request->images_size() > 0 || request->audios_size() > 0);
                    
                    // Handle content - can be string, null, or array
                    // For multimodal content, we'll embed images/audio from separate fields
                    if (!msg.content().empty()) {
                        // Try to parse content as JSON to see if it's already an array
                        json content_val;
                        try {
                            content_val = json::parse(msg.content());
                            // Handle null values - convert to empty string to avoid template errors
                            if (content_val.is_null()) {
                                SRV_INF("[CONTENT DEBUG] Predict: Message %d parsed JSON is null, converting to empty string\n", i);
                                content_val = "";
                            }
                        } catch (const json::parse_error&) {
                            // Not JSON, treat as plain string
                            content_val = msg.content();
                        }
                        
                        // If content is an object (e.g., from tool call failures), convert to string
                        if (content_val.is_object()) {
                            SRV_INF("[CONTENT DEBUG] Predict: Message %d content is object, converting to string\n", i);
                            content_val = content_val.dump();
                        }
                        
                        // If content is a string and this is the last user message with images/audio, combine them
                        if (content_val.is_string() && is_last_user_msg && has_images_or_audio) {
                            json content_array = json::array();
                            // Add text first
                            content_array.push_back({{"type", "text"}, {"text", content_val.get<std::string>()}});
                            // Add images
                            if (request->images_size() > 0) {
                                for (int j = 0; j < request->images_size(); j++) {
                                    json image_chunk;
                                    image_chunk["type"] = "image_url";
                                    json image_url;
                                    image_url["url"] = "data:image/jpeg;base64," + request->images(j);
                                    image_chunk["image_url"] = image_url;
                                    content_array.push_back(image_chunk);
                                }
                            }
                            // Add audios
                            if (request->audios_size() > 0) {
                                for (int j = 0; j < request->audios_size(); j++) {
                                    json audio_chunk;
                                    audio_chunk["type"] = "input_audio";
                                    json input_audio;
                                    input_audio["data"] = request->audios(j);
                                    input_audio["format"] = "wav"; // default, could be made configurable
                                    audio_chunk["input_audio"] = input_audio;
                                    content_array.push_back(audio_chunk);
                                }
                            }
                            msg_json["content"] = content_array;
                        } else {
                            // Use content as-is (already array or not last user message)
                            // Ensure null values are converted to empty string
                            if (content_val.is_null()) {
                                SRV_INF("[CONTENT DEBUG] Predict: Message %d content_val was null, setting to empty string\n", i);
                                msg_json["content"] = "";
                            } else {
                                msg_json["content"] = content_val;
                                SRV_INF("[CONTENT DEBUG] Predict: Message %d content set, type=%s\n", 
                                        i, content_val.is_string() ? "string" : 
                                           content_val.is_array() ? "array" : 
                                           content_val.is_object() ? "object" : "other");
                            }
                        }
                    } else if (is_last_user_msg && has_images_or_audio) {
                        // If no content but this is the last user message with images/audio, create content array
                        json content_array = json::array();
                        if (request->images_size() > 0) {
                            for (int j = 0; j < request->images_size(); j++) {
                                json image_chunk;
                                image_chunk["type"] = "image_url";
                                json image_url;
                                image_url["url"] = "data:image/jpeg;base64," + request->images(j);
                                image_chunk["image_url"] = image_url;
                                content_array.push_back(image_chunk);
                            }
                        }
                        if (request->audios_size() > 0) {
                            for (int j = 0; j < request->audios_size(); j++) {
                                json audio_chunk;
                                audio_chunk["type"] = "input_audio";
                                json input_audio;
                                input_audio["data"] = request->audios(j);
                                input_audio["format"] = "wav"; // default, could be made configurable
                                audio_chunk["input_audio"] = input_audio;
                                content_array.push_back(audio_chunk);
                            }
                        }
                        msg_json["content"] = content_array;
                        SRV_INF("[CONTENT DEBUG] Predict: Message %d created content array with media\n", i);
                    } else if (!msg.tool_calls().empty()) {
                        // Tool call messages may have null content, but templates expect string
                        // IMPORTANT: Set to space " " instead of empty string "", because llama.cpp's
                        // common_chat_msgs_to_json_oaicompat converts empty strings to null (line 312),
                        // which causes template errors when accessing message.content[:tool_start_length]
                        SRV_INF("[CONTENT DEBUG] Predict: Message %d has tool_calls, setting content to space (not empty string)\n", i);
                        msg_json["content"] = " ";
                    } else if (msg.role() == "tool") {
                        // Tool role messages must have content field set, even if empty
                        // Jinja templates expect content to be a string, not null or object
                        SRV_INF("[CONTENT DEBUG] Predict: Message %d is tool role, content_empty=%d\n", i, msg.content().empty() ? 1 : 0);
                        if (msg.content().empty()) {
                            msg_json["content"] = "";
                            SRV_INF("[CONTENT DEBUG] Predict: Message %d (tool): empty content, set to empty string\n", i);
                        } else {
                            SRV_INF("[CONTENT DEBUG] Predict: Message %d (tool): content exists: %s\n", 
                                    i, msg.content().substr(0, std::min<size_t>(200, msg.content().size())).c_str());
                            // Content exists, parse and ensure it's a string
                            json content_val;
                            try {
                                content_val = json::parse(msg.content());
                                SRV_INF("[CONTENT DEBUG] Predict: Message %d (tool): parsed JSON, type=%s\n", 
                                        i, content_val.is_null() ? "null" : 
                                           content_val.is_object() ? "object" :
                                           content_val.is_string() ? "string" :
                                           content_val.is_array() ? "array" : "other");
                                // Handle null values - Jinja templates expect content to be a string, not null
                                if (content_val.is_null()) {
                                    msg_json["content"] = "";
                                    SRV_INF("[CONTENT DEBUG] Predict: Message %d (tool): null content, converted to empty string\n", i);
                                } else if (content_val.is_object()) {
                                    // If content is an object (e.g., from tool call failures/errors), convert to string
                                    msg_json["content"] = content_val.dump();
                                    SRV_INF("[CONTENT DEBUG] Predict: Message %d (tool): object content, converted to string: %s\n", 
                                            i, content_val.dump().substr(0, std::min<size_t>(200, content_val.dump().size())).c_str());
                                } else if (content_val.is_string()) {
                                    msg_json["content"] = content_val.get<std::string>();
                                    SRV_INF("[CONTENT DEBUG] Predict: Message %d (tool): string content, using as-is\n", i);
                                } else {
                                    // For arrays or other types, convert to string
                                    msg_json["content"] = content_val.dump();
                                    SRV_INF("[CONTENT DEBUG] Predict: Message %d (tool): %s content, converted to string\n", 
                                            i, content_val.is_array() ? "array" : "other type");
                                }
                            } catch (const json::parse_error&) {
                                // Not JSON, treat as plain string
                                msg_json["content"] = msg.content();
                                SRV_INF("[CONTENT DEBUG] Predict: Message %d (tool): not JSON, using as string\n", i);
                            }
                        }
                    } else {
                        // Ensure all messages have content set (fallback for any unhandled cases)
                        // Jinja templates expect content to be present, default to empty string if not set
                        if (!msg_json.contains("content")) {
                            SRV_INF("[CONTENT DEBUG] Predict: Message %d (role=%s): no content field, adding empty string\n", 
                                    i, msg.role().c_str());
                            msg_json["content"] = "";
                        }
                    }
                    
                    // Add optional fields for OpenAI-compatible message format
                    if (!msg.name().empty()) {
                        msg_json["name"] = msg.name();
                    }
                    if (!msg.tool_call_id().empty()) {
                        msg_json["tool_call_id"] = msg.tool_call_id();
                    }
                    if (!msg.reasoning_content().empty()) {
                        msg_json["reasoning_content"] = msg.reasoning_content();
                    }
                    if (!msg.tool_calls().empty()) {
                        // Parse tool_calls JSON string and add to message
                        try {
                            json tool_calls = json::parse(msg.tool_calls());
                            msg_json["tool_calls"] = tool_calls;
                            SRV_INF("[TOOL CALLS DEBUG] Predict: Message %d has tool_calls: %s\n", i, tool_calls.dump().c_str());
                            // IMPORTANT: If message has tool_calls but content is empty or not set,
                            // set content to space " " instead of empty string "", because llama.cpp's
                            // common_chat_msgs_to_json_oaicompat converts empty strings to null (line 312),
                            // which causes template errors when accessing message.content[:tool_start_length]
                            if (!msg_json.contains("content") || (msg_json.contains("content") && msg_json["content"].is_string() && msg_json["content"].get<std::string>().empty())) {
                                SRV_INF("[CONTENT DEBUG] Predict: Message %d has tool_calls but empty content, setting to space\n", i);
                                msg_json["content"] = " ";
                            }
                            // Log each tool call with name and arguments
                            if (tool_calls.is_array()) {
                                for (size_t tc_idx = 0; tc_idx < tool_calls.size(); tc_idx++) {
                                    const auto& tc = tool_calls[tc_idx];
                                    std::string tool_name = "unknown";
                                    std::string tool_args = "{}";
                                    if (tc.contains("function")) {
                                        const auto& func = tc["function"];
                                        if (func.contains("name")) {
                                            tool_name = func["name"].get<std::string>();
                                        }
                                        if (func.contains("arguments")) {
                                            tool_args = func["arguments"].is_string() ? 
                                                func["arguments"].get<std::string>() : 
                                                func["arguments"].dump();
                                        }
                                    } else if (tc.contains("name")) {
                                        tool_name = tc["name"].get<std::string>();
                                        if (tc.contains("arguments")) {
                                            tool_args = tc["arguments"].is_string() ? 
                                                tc["arguments"].get<std::string>() : 
                                                tc["arguments"].dump();
                                        }
                                    }
                                    SRV_INF("[TOOL CALLS DEBUG] Predict: Message %d, tool_call %zu: name=%s, arguments=%s\n", 
                                            i, tc_idx, tool_name.c_str(), tool_args.c_str());
                                }
                            }
                        } catch (const json::parse_error& e) {
                            SRV_WRN("Failed to parse tool_calls JSON: %s\n", e.what());
                        }
                    }
                    
                    // Debug: Log final content state before adding to array
                    if (msg_json.contains("content")) {
                        if (msg_json["content"].is_null()) {
                            SRV_INF("[CONTENT DEBUG] Predict: Message %d FINAL STATE: content is NULL - THIS WILL CAUSE ERROR!\n", i);
                        } else {
                            SRV_INF("[CONTENT DEBUG] Predict: Message %d FINAL STATE: content type=%s, has_value=%d\n", 
                                    i, msg_json["content"].is_string() ? "string" :
                                       msg_json["content"].is_array() ? "array" :
                                       msg_json["content"].is_object() ? "object" : "other",
                                    msg_json["content"].is_null() ? 0 : 1);
                        }
                    } else {
                        SRV_INF("[CONTENT DEBUG] Predict: Message %d FINAL STATE: NO CONTENT FIELD - THIS WILL CAUSE ERROR!\n", i);
                    }
                    
                    messages_json.push_back(msg_json);
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

                // Debug: Print full body_json before template processing (includes messages, tools, tool_choice, etc.)
                SRV_DBG("[CONVERSATION DEBUG] Predict: Full body_json before oaicompat_chat_params_parse:\n%s\n", body_json.dump(2).c_str());

                // Use the same approach as server.cpp: call oaicompat_chat_params_parse
                // This handles all template application, grammar merging, etc. automatically
                // Files extracted from multimodal content in messages will be added to the files vector
                // Create parser options with current chat_templates to ensure tmpls is not null
                oaicompat_parser_options parser_opt = ctx_server.impl->oai_parser_opt;
                parser_opt.tmpls = ctx_server.impl->chat_templates.get(); // Ensure tmpls is set to current chat_templates
                // Update allow_image and allow_audio based on current mctx state
                parser_opt.allow_image = ctx_server.impl->mctx ? mtmd_support_vision(ctx_server.impl->mctx) : false;
                parser_opt.allow_audio = ctx_server.impl->mctx ? mtmd_support_audio(ctx_server.impl->mctx) : false;
                
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
                        auto& msg = body_json["messages"][idx];
                        std::string role_str = msg.contains("role") ? msg["role"].get<std::string>() : "unknown";
                        if (msg.contains("content")) {
                            if (msg["content"].is_null()) {
                                SRV_INF("[CONTENT DEBUG] Predict: BEFORE TEMPLATE - Message %zu (role=%s) has NULL content - FIXING!\n", idx, role_str.c_str());
                                msg["content"] = ""; // Fix null content
                            } else if (!msg["content"].is_string() && !msg["content"].is_array()) {
                                // If content is object or other non-string type, convert to string for templates
                                SRV_INF("[CONTENT DEBUG] Predict: BEFORE TEMPLATE - Message %zu (role=%s) content is not string/array, converting\n", idx, role_str.c_str());
                                if (msg["content"].is_object()) {
                                    msg["content"] = msg["content"].dump();
                                } else {
                                    msg["content"] = "";
                                }
                            } else {
                                SRV_INF("[CONTENT DEBUG] Predict: BEFORE TEMPLATE - Message %zu (role=%s): content type=%s\n", 
                                        idx, role_str.c_str(),
                                        msg["content"].is_string() ? "string" :
                                        msg["content"].is_array() ? "array" :
                                        msg["content"].is_object() ? "object" : "other");
                            }
                        } else {
                            SRV_INF("[CONTENT DEBUG] Predict: BEFORE TEMPLATE - Message %zu (role=%s) MISSING content field - ADDING!\n", idx, role_str.c_str());
                            msg["content"] = ""; // Add missing content
                        }
                    }
                }
                
                json parsed_data = oaicompat_chat_params_parse(body_json, parser_opt, files);
                
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

            const auto & prompt = prompt_str;
            const auto type = SERVER_TASK_TYPE_COMPLETION;
            // TODO: this log can become very long, put it behind a flag or think about a more compact format
            //SRV_DBG("Prompt: %s\n", prompt.is_string() ? prompt.get<std::string>().c_str() : prompt.dump(2).c_str());

            // If not using chat templates, extract files from image_data/audio_data fields
            // (If using chat templates, files were already extracted by oaicompat_chat_params_parse)
            if (!request->usetokenizertemplate() || request->messages_size() == 0 || ctx_server.impl->chat_templates == nullptr) {
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

                task.id    = queues.first.get_new_id();
                task.index = i;

                task.tokens    = std::move(inputs[i]);
                task.params           = server_task::params_from_json_cmpl(
                        ctx_server.get_llama_context(),
                        ctx_server.impl->params_base,
                        data);
                task.id_slot = json_value(data, "id_slot", -1);

                // OAI-compat
                task.params.res_type                 = TASK_RESPONSE_TYPE_NONE;
                task.params.oaicompat_cmpl_id         = completion_id;
                // oaicompat_model is already populated by params_from_json_cmpl

                tasks.push_back(std::move(task));
            }

            rd->post_tasks(std::move(tasks));
        } catch (const std::exception & e) {
            return grpc::Status(grpc::StatusCode::INVALID_ARGUMENT, e.what());
        }


        std::cout << "[DEBUG] Waiting for results..." << std::endl;
        
        // Wait for all results
        auto all_results = rd->wait_for_all([&context]() { return context->IsCancelled(); });
        
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
                GGML_ASSERT(dynamic_cast<server_task_result_cmpl_final*>(all_results.results[0].get()) != nullptr);
                json result_json = all_results.results[0]->to_json();
                reply->set_message(result_json.value("content", ""));

                int32_t tokens_predicted = result_json.value("tokens_predicted", 0);
                reply->set_tokens(tokens_predicted);
                int32_t tokens_evaluated = result_json.value("tokens_evaluated", 0);
                reply->set_prompt_tokens(tokens_evaluated);

                if (result_json.contains("timings")) {
                    double timing_prompt_processing = result_json.at("timings").value("prompt_ms", 0.0);
                    reply->set_timing_prompt_processing(timing_prompt_processing);
                    double timing_token_generation = result_json.at("timings").value("predicted_ms", 0.0);
                    reply->set_timing_token_generation(timing_token_generation);
                }

                // Extract and set logprobs if present
                json logprobs_json = extract_logprobs_from_json(result_json);
                if (!logprobs_json.empty() && !logprobs_json.is_null()) {
                    std::string logprobs_str = logprobs_json.dump();
                    reply->set_logprobs(logprobs_str);
                }

            } else {
                // multiple results (multitask)
                json arr = json::array();
                json logprobs_arr = json::array();
                bool has_logprobs = false;
                for (auto & res : all_results.results) {
                    GGML_ASSERT(dynamic_cast<server_task_result_cmpl_final*>(res.get()) != nullptr);
                    json res_json = res->to_json();
                    arr.push_back(res_json.value("content", ""));
                    
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

    grpc::Status Embedding(ServerContext* context, const backend::PredictOptions* request, backend::EmbeddingResult* embeddingResult) {
        if (!params_base_ptr) {
            return grpc::Status(grpc::StatusCode::FAILED_PRECONDITION, "Model not loaded");
        }
        json body = parse_options(false, request, *params_base_ptr, ctx_server.get_llama_context());

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

        int embd_normalize = 2; // default to Euclidean/L2 norm
        // create and queue the task
        auto queues = ctx_server.get_queues();
        const auto rd = std::make_shared<server_response_reader>(queues, 1); // HTTP_POLLING_SECONDS = 1
        {
            std::vector<server_task> tasks;
            for (size_t i = 0; i < tokenized_prompts.size(); i++) {
                server_task task = server_task(SERVER_TASK_TYPE_EMBEDDING);

                task.id            = queues.first.get_new_id();
                task.index         = i;
                task.tokens = std::move(tokenized_prompts[i]);

                task.params.res_type = TASK_RESPONSE_TYPE_NONE;
                task.params.embd_normalize = embd_normalize;
                tasks.push_back(std::move(task));
            }

            rd->post_tasks(std::move(tasks));
        }

        // Wait for all results
        auto all_results = rd->wait_for_all([&context]() { return context->IsCancelled(); });
        
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

    grpc::Status Rerank(ServerContext* context, const backend::RerankRequest* request, backend::RerankResult* rerankResult) {
        if (!ctx_server.impl->params_base.embedding || ctx_server.impl->params_base.pooling_type != LLAMA_POOLING_TYPE_RANK) {
            return grpc::Status(grpc::StatusCode::UNIMPLEMENTED, "This server does not support reranking. Start it with `--reranking` and without `--embedding`");
        }

        // Validate request
        if (request->query().empty()) {
            return grpc::Status(grpc::StatusCode::INVALID_ARGUMENT, "\"query\" must be provided");
        }

        if (request->documents_size() == 0) {
            return grpc::Status(grpc::StatusCode::INVALID_ARGUMENT, "\"documents\" must be a non-empty string array");
        }

        // Create and queue the task
        auto queues = ctx_server.get_queues();
        const auto rd = std::make_shared<server_response_reader>(queues, 1); // HTTP_POLLING_SECONDS = 1
        {
            std::vector<server_task> tasks;
            std::vector<std::string> documents;
            for (int i = 0; i < request->documents_size(); i++) {
                documents.push_back(request->documents(i));
            }
            
            tasks.reserve(documents.size());
            for (size_t i = 0; i < documents.size(); i++) {
                auto tmp = format_prompt_rerank(ctx_server.impl->model, ctx_server.impl->vocab, ctx_server.impl->mctx, request->query(), documents[i]);
                server_task task = server_task(SERVER_TASK_TYPE_RERANK);
                task.id = queues.first.get_new_id();
                task.index = i;
                task.tokens = std::move(tmp);
                tasks.push_back(std::move(task));
            }

            rd->post_tasks(std::move(tasks));
        }

        // Wait for all results
        auto all_results = rd->wait_for_all([&context]() { return context->IsCancelled(); });
        
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

    grpc::Status TokenizeString(ServerContext* context, const backend::PredictOptions* request, backend::TokenizationResponse* response) {
        if (!params_base_ptr) {
            return grpc::Status(grpc::StatusCode::FAILED_PRECONDITION, "Model not loaded");
        }
        json body = parse_options(false, request, *params_base_ptr, ctx_server.get_llama_context());
        body["stream"] = false;
        
        json tokens_response = json::array();
        if (body.count("prompt") != 0) {
            const bool add_special = json_value(body, "add_special", false);
            const bool with_pieces = json_value(body, "with_pieces", false);

            llama_tokens tokens = tokenize_mixed(ctx_server.impl->vocab, body.at("content"), add_special, true);


            for (const auto& token : tokens) {
                std::string piece = common_token_to_piece(ctx_server.get_llama_context(), token);
                response->add_tokens(token);
            }
        }

        return grpc::Status::OK;
    }

    grpc::Status GetMetrics(ServerContext* context, const backend::MetricsRequest* request, backend::MetricsResponse* response) {

// request slots data using task queue
        auto queues = ctx_server.get_queues();
        int task_id = queues.first.get_new_id();
        {
            server_task task(SERVER_TASK_TYPE_METRICS);
            task.id = task_id;
            queues.second.add_waiting_task_id(task_id);
            queues.first.post(std::move(task), true); // high-priority task
        }

        // get the result
        server_task_result_ptr result = queues.second.recv(task_id);
        queues.second.remove_waiting_task_id(task_id);

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
  
    server_context ctx_server;
    BackendServiceImpl service(ctx_server);

    ServerBuilder builder;
    builder.AddListeningPort(server_address, grpc::InsecureServerCredentials());
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
