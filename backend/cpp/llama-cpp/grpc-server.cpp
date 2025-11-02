// llama.cpp gRPC C++ backend server
//
// Ettore Di Giacinto <mudler@localai.io> and llama.cpp authors
//
// This is a gRPC server for llama.cpp compatible with the LocalAI proto
// Note: this is a re-adaptation of the original llama.cpp example/server.cpp for HTTP (https://github.com/ggerganov/llama.cpp/tree/master/examples/server), 
// but modified to work with gRPC
//

#include "server.cpp"
// LocalAI

#include "backend.pb.h"
#include "backend.grpc.pb.h"
#include "common.h"
#include <getopt.h>
#include <grpcpp/ext/proto_server_reflection_plugin.h>
#include <grpcpp/grpcpp.h>
#include <grpcpp/health_check_service_interface.h>
#include <regex>


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
    //     common_chat_templates_source(ctx_server.chat_templates.get()),
    //     common_chat_format_example(ctx_server.chat_templates.get(), ctx_server.params_base.use_jinja).c_str(), ctx_server.params_base.default_template_kwargs);

    // Reset the chat templates
    // TODO: We should make this configurable by respecting the option that is already present in LocalAI for vLLM
    ctx_server.chat_templates.reset();

    ctx_server.queue_tasks.on_new_task([&ctx_server](server_task && task) {
        ctx_server.process_single_task(std::move(task));
    });

    ctx_server.queue_tasks.on_update_slots([&ctx_server]() {
        ctx_server.update_slots();
    });

    shutdown_handler = [&](int) {
        // this will unblock start_loop()
        ctx_server.queue_tasks.terminate();
    };

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

    // this call blocks the main thread until queue_tasks.terminate() is called
    ctx_server.queue_tasks.start_loop();
}

json parse_options(bool streaming, const backend::PredictOptions* predict, const server_context& ctx_server)
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
    data["grammar"] = predict->grammar();
    data["prompt"] = predict->prompt();
    data["ignore_eos"] = predict->ignoreeos();
    data["embeddings"] = predict->embeddings();
    // TODO: add back json_schema and let this be controlled by the user
    // data["json_schema"] = predict->jsonschema();

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
    if (!ctx_server.params_base.sampling.grammar_triggers.empty()) {
        json grammar_triggers = json::array();
        for (const auto& trigger : ctx_server.params_base.sampling.grammar_triggers) {
            json trigger_json;
            trigger_json["value"] = trigger.value;
            // Always serialize as WORD type since upstream converts WORD to TOKEN internally
            trigger_json["type"] = static_cast<int>(COMMON_GRAMMAR_TRIGGER_TYPE_WORD);
            grammar_triggers.push_back(trigger_json);
        }
        data["grammar_triggers"] = grammar_triggers;
    }

    // Serialize preserved tokens from server context to JSON array
    if (!ctx_server.params_base.sampling.preserved_tokens.empty()) {
        json preserved_tokens = json::array();
        for (const auto& token : ctx_server.params_base.sampling.preserved_tokens) {
            preserved_tokens.push_back(common_token_to_piece(ctx_server.ctx, token));
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
    params.n_ubatch = request->nbatch(); // fixes issue with reranking models being limited to 512 tokens (the default n_ubatch size); allows for setting the maximum input amount of tokens thereby avoiding this error "input is too large to process. increase the physical batch size"
    // Set params.n_parallel by environment variable (LLAMA_PARALLEL), defaults to 1
    //params.n_parallel = 1;
    const char *env_parallel = std::getenv("LLAMACPP_PARALLEL");
    if (env_parallel != NULL) {
        params.n_parallel = std::stoi(env_parallel);
        params.cont_batching = true;
    } else {
        params.n_parallel = 1;
    }


    const char *llama_grpc_servers = std::getenv("LLAMACPP_GRPC_SERVERS");
    if (llama_grpc_servers != NULL) {
        add_rpc_devices(std::string(llama_grpc_servers));
    }
    
    // Initialize ctx_shift to false by default (can be overridden by options)
    params.ctx_shift = false;
    // Initialize cache_ram_mib to -1 by default (no limit, can be overridden by options)
    params.cache_ram_mib = -1;

     // decode options. Options are in form optname:optvale, or if booleans only optname.
    for (int i = 0; i < request->options_size(); i++) {
        std::string opt = request->options(i);
        char *optname = strtok(&opt[0], ":");
        char *optval = strtok(NULL, ":");
        if (optval == NULL) {
            optval = "true";
        }

        if (!strcmp(optname, "context_shift")) {
            if (!strcmp(optval, "true") || !strcmp(optval, "1") || !strcmp(optval, "yes") || !strcmp(optval, "on") || !strcmp(optval, "enabled")) {
                params.ctx_shift = true;
            } else if (!strcmp(optval, "false") || !strcmp(optval, "0") || !strcmp(optval, "no") || !strcmp(optval, "off") || !strcmp(optval, "disabled")) {
                params.ctx_shift = false;
            }
        } else if (!strcmp(optname, "cache_ram")) {
            if (optval != NULL) {
                try {
                    params.cache_ram_mib = std::stoi(optval);
                } catch (const std::exception& e) {
                    // If conversion fails, keep default value (-1)
                }
            }
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
     params.lora_adapters.push_back({ model_dir + "/"+request->loraadapter(), scale_factor });
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

public:
    BackendServiceImpl(server_context& ctx) : ctx_server(ctx) {}

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
                    auto ids = common_tokenize(ctx_server.vocab, trigger.value, /* add_special= */ false, /* parse_special= */ true);
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
            ctx_server.params_base.sampling.grammar_triggers = std::move(processed_triggers);
            // Also update preserved_tokens in params_base
            ctx_server.params_base.sampling.preserved_tokens = params.sampling.preserved_tokens;
        }

        //ctx_server.init();
        result->set_message("Loading succeeded");
        result->set_success(true);
        loaded_model = true;
        ctx_server.slot_prompt_similarity = params.slot_prompt_similarity;

        return Status::OK;
    }

    grpc::Status PredictStream(grpc::ServerContext* context, const backend::PredictOptions* request, grpc::ServerWriter<backend::Reply>* writer) override {
        json data = parse_options(true, request, ctx_server);


        //Raise error if embeddings is set to true
        if (ctx_server.params_base.embedding) {
            return grpc::Status(grpc::StatusCode::INVALID_ARGUMENT, "Embedding is not supported in streaming mode");
        }


        auto completion_id = gen_chatcmplid();
        std::unordered_set<int> task_ids;
        try {
            std::vector<server_task> tasks;

            const auto & prompt = data.at("prompt");
            const auto type = SERVER_TASK_TYPE_COMPLETION;
            // TODO: this log can become very long, put it behind a flag or think about a more compact format
            //SRV_DBG("Prompt: %s\n", prompt.is_string() ? prompt.get<std::string>().c_str() : prompt.dump(2).c_str());

            std::vector<raw_buffer> files;
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

            const bool has_mtmd = ctx_server.mctx != nullptr;

            // process prompt
            std::vector<server_tokens> inputs;
            if (!prompt.is_string()) {
                throw std::runtime_error("prompt must be a string");
            }

            if (has_mtmd) {
                // multimodal
                inputs.push_back(process_mtmd_prompt(ctx_server.mctx, prompt.get<std::string>(), files));
            } else {
                 // Everything else, including multimodal completions.
                inputs = tokenize_input_prompts(ctx_server.vocab, ctx_server.mctx, prompt, true, true);
            }

            tasks.reserve(inputs.size());
            for (size_t i = 0; i < inputs.size(); i++) {
                server_task task = server_task(type);

                task.id    = ctx_server.queue_tasks.get_new_id();
                task.index = i;

                task.tokens    = std::move(inputs[i]);
                task.params           = server_task::params_from_json_cmpl(
                        ctx_server.ctx,
                        ctx_server.params_base,
                        data);
                task.id_slot = json_value(data, "id_slot", -1);

                // OAI-compat
                task.params.oaicompat                 = OAICOMPAT_TYPE_NONE;
                task.params.oaicompat_cmpl_id         = completion_id;
                // oaicompat_model is already populated by params_from_json_cmpl

                tasks.push_back(std::move(task));
            }

            task_ids = server_task::get_list_id(tasks);
            ctx_server.queue_results.add_waiting_tasks(tasks);
            ctx_server.queue_tasks.post(std::move(tasks));
        } catch (const std::exception & e) {
            return grpc::Status(grpc::StatusCode::INVALID_ARGUMENT, e.what());
        }

        ctx_server.receive_cmpl_results_stream(task_ids, [&](server_task_result_ptr & result) -> bool {
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

                    // Log Request Correlation Id
                
                    // Send the reply
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

                

                // Send the reply
                    writer->Write(reply);
           
            }
                 return true;
        }, [&](const json & error_data) {
            backend::Reply reply;
            reply.set_message(error_data.value("content", ""));
            writer->Write(reply);
            return true;
        }, [&]() {
            // NOTE: we should try to check when the writer is closed here
            return false;
        });

        ctx_server.queue_results.remove_waiting_task_ids(task_ids);

        return grpc::Status::OK;
    }

    grpc::Status Predict(ServerContext* context, const backend::PredictOptions* request, backend::Reply* reply) {
         json data = parse_options(true, request, ctx_server);

        data["stream"] = false;
        //Raise error if embeddings is set to true
        if (ctx_server.params_base.embedding) {
            return grpc::Status(grpc::StatusCode::INVALID_ARGUMENT, "Embedding is not supported in Predict mode");
        }
        std::cout << "[PREDICT] Received result: " << data.dump(2) << std::endl;
        auto completion_id = gen_chatcmplid();
        std::unordered_set<int> task_ids;
        try {
            std::vector<server_task> tasks;

            const auto & prompt = data.at("prompt");
            const auto type = SERVER_TASK_TYPE_COMPLETION;
            // TODO: this log can become very long, put it behind a flag or think about a more compact format
            //SRV_DBG("Prompt: %s\n", prompt.is_string() ? prompt.get<std::string>().c_str() : prompt.dump(2).c_str());

            std::vector<raw_buffer> files;
            const auto &images_data = data.find("image_data");
           // std::cout << "[PREDICT] Images data: " << images_data->dump(2) << std::endl;
           
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

            // process files
            const bool has_mtmd = ctx_server.mctx != nullptr;

            // process prompt
            std::vector<server_tokens> inputs;
            if (!prompt.is_string()) {
                std::cout << "[PREDICT] Prompt must be a string" << std::endl;
                throw std::runtime_error("prompt must be a string");
            }

            if (has_mtmd) {
                // multimodal
                inputs.push_back(process_mtmd_prompt(ctx_server.mctx, prompt.get<std::string>(), files));
            } else {
                 // Everything else, including multimodal completions.
                inputs = tokenize_input_prompts(ctx_server.vocab, ctx_server.mctx, prompt, true, true);
            }

            tasks.reserve(inputs.size());
            for (size_t i = 0; i < inputs.size(); i++) {
                server_task task = server_task(type);

                task.id    = ctx_server.queue_tasks.get_new_id();
                task.index = i;

                task.tokens    = std::move(inputs[i]);
                task.params           = server_task::params_from_json_cmpl(
                        ctx_server.ctx,
                        ctx_server.params_base,
                        data);
                task.id_slot = json_value(data, "id_slot", -1);

                // OAI-compat
                task.params.oaicompat                 = OAICOMPAT_TYPE_NONE;
                task.params.oaicompat_cmpl_id         = completion_id;
                // oaicompat_model is already populated by params_from_json_cmpl

                tasks.push_back(std::move(task));
            }

            task_ids = server_task::get_list_id(tasks);
            ctx_server.queue_results.add_waiting_tasks(tasks);
            ctx_server.queue_tasks.post(std::move(tasks));
        } catch (const std::exception & e) {
            return grpc::Status(grpc::StatusCode::INVALID_ARGUMENT, e.what());
        }


        std::cout << "[DEBUG] Waiting for results..." << std::endl;
        ctx_server.receive_multi_results(task_ids, [&](std::vector<server_task_result_ptr> & results) {
            std::cout << "[DEBUG] Received " << results.size() << " results" << std::endl;
            if (results.size() == 1) {
                // single result
                reply->set_message(results[0]->to_json().value("content", ""));

                int32_t tokens_predicted = results[0]->to_json().value("tokens_predicted", 0);
                reply->set_tokens(tokens_predicted);
                int32_t tokens_evaluated = results[0]->to_json().value("tokens_evaluated", 0);
                reply->set_prompt_tokens(tokens_evaluated);

                if (results[0]->to_json().contains("timings")) {
                    double timing_prompt_processing = results[0]->to_json().at("timings").value("prompt_ms", 0.0);
                    reply->set_timing_prompt_processing(timing_prompt_processing);
                    double timing_token_generation = results[0]->to_json().at("timings").value("predicted_ms", 0.0);
                    reply->set_timing_token_generation(timing_token_generation);
                }

            } else {
                // multiple results (multitask)
                json arr = json::array();
                for (auto & res : results) {
                    arr.push_back(res->to_json().value("content", ""));
                }
                reply->set_message(arr);
            }

            
        }, [&](const json & error_data) {
            std::cout << "[DEBUG] Error in results: " << error_data.value("content", "") << std::endl;
            reply->set_message(error_data.value("content", ""));
        }, [&]() {
            return false;
        });

        ctx_server.queue_results.remove_waiting_task_ids(task_ids);
        std::cout << "[DEBUG] Predict request completed successfully" << std::endl;

        return grpc::Status::OK;
    }

    grpc::Status Embedding(ServerContext* context, const backend::PredictOptions* request, backend::EmbeddingResult* embeddingResult) {

        json body = parse_options(false, request, ctx_server);

        body["stream"] = false;

        /*
        if (llama_pooling_type(ctx_server.ctx) == LLAMA_POOLING_TYPE_NONE) {
            return grpc::Status(grpc::StatusCode::INVALID_ARGUMENT, "Pooling type 'none' is not OAI compatible. Please use a different pooling type");
        }
        */

        // for the shape of input/content, see tokenize_input_prompts()
        json prompt = body.at("embeddings");


        auto tokenized_prompts = tokenize_input_prompts(ctx_server.vocab, ctx_server.mctx, prompt, true, true);
        for (const auto & tokens : tokenized_prompts) {
            // this check is necessary for models that do not add BOS token to the input
            if (tokens.empty()) {
                return grpc::Status(grpc::StatusCode::INVALID_ARGUMENT, "Input content cannot be empty");
            }
        }

        int embd_normalize = 2; // default to Euclidean/L2 norm
        // create and queue the task
        json responses = json::array();
        bool error = false;
        std::unordered_set<int> task_ids;
        {
            std::vector<server_task> tasks;
            for (size_t i = 0; i < tokenized_prompts.size(); i++) {
                server_task task = server_task(SERVER_TASK_TYPE_EMBEDDING);

                task.id            = ctx_server.queue_tasks.get_new_id();
                task.index         = i;
                task.tokens = std::move(tokenized_prompts[i]);

                task.params.oaicompat = OAICOMPAT_TYPE_NONE;
                task.params.embd_normalize = embd_normalize;
                tasks.push_back(std::move(task));
            }

            task_ids = server_task::get_list_id(tasks);
            ctx_server.queue_results.add_waiting_tasks(tasks);
            ctx_server.queue_tasks.post(std::move(tasks));
        }

        // get the result
        ctx_server.receive_multi_results(task_ids, [&](std::vector<server_task_result_ptr> & results) {
            for (auto & res : results) {
                GGML_ASSERT(dynamic_cast<server_task_result_embd*>(res.get()) != nullptr);
                responses.push_back(res->to_json());
            }
        }, [&](const json & error_data) {
            error = true;
        }, [&]() {
            return false;
        });

        ctx_server.queue_results.remove_waiting_task_ids(task_ids);

        if (error) {
            return grpc::Status(grpc::StatusCode::INTERNAL, "Error in receiving results");
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
        if (!ctx_server.params_base.embedding || ctx_server.params_base.pooling_type != LLAMA_POOLING_TYPE_RANK) {
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
        json responses = json::array();
        bool error = false;
        std::unordered_set<int> task_ids;
        {
            std::vector<server_task> tasks;
            std::vector<std::string> documents;
            for (int i = 0; i < request->documents_size(); i++) {
                documents.push_back(request->documents(i));
            }
            
            tasks.reserve(documents.size());
            for (size_t i = 0; i < documents.size(); i++) {
                auto tmp = format_rerank(ctx_server.model, ctx_server.vocab, ctx_server.mctx, request->query(), documents[i]);
                server_task task = server_task(SERVER_TASK_TYPE_RERANK);
                task.id = ctx_server.queue_tasks.get_new_id();
                task.index = i;
                task.tokens = std::move(tmp);
                tasks.push_back(std::move(task));
            }

            task_ids = server_task::get_list_id(tasks);
            ctx_server.queue_results.add_waiting_tasks(tasks);
            ctx_server.queue_tasks.post(std::move(tasks));
        }

        // Get the results
        ctx_server.receive_multi_results(task_ids, [&](std::vector<server_task_result_ptr> & results) {
            for (auto & res : results) {
                GGML_ASSERT(dynamic_cast<server_task_result_rerank*>(res.get()) != nullptr);
                responses.push_back(res->to_json());
            }
        }, [&](const json & error_data) {
            error = true;
        }, [&]() {
            return false;
        });

        ctx_server.queue_results.remove_waiting_task_ids(task_ids);

        if (error) {
            return grpc::Status(grpc::StatusCode::INTERNAL, "Error in receiving results");
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
        json body = parse_options(false, request, ctx_server);
        body["stream"] = false;
        
        json tokens_response = json::array();
        if (body.count("prompt") != 0) {
            const bool add_special = json_value(body, "add_special", false);
            const bool with_pieces = json_value(body, "with_pieces", false);

            llama_tokens tokens = tokenize_mixed(ctx_server.vocab, body.at("content"), add_special, true);


            for (const auto& token : tokens) {
                std::string piece = common_token_to_piece(ctx_server.ctx, token);
                response->add_tokens(token);
            }
        }

        return grpc::Status::OK;
    }

    grpc::Status GetMetrics(ServerContext* context, const backend::MetricsRequest* request, backend::MetricsResponse* response) {

// request slots data using task queue
        int task_id = ctx_server.queue_tasks.get_new_id();
        {
            server_task task(SERVER_TASK_TYPE_METRICS);
            task.id = task_id;
            ctx_server.queue_results.add_waiting_task_id(task_id);
            ctx_server.queue_tasks.post(std::move(task), true); // high-priority task
        }

        // get the result
        server_task_result_ptr result = ctx_server.queue_results.recv(task_id);
        ctx_server.queue_results.remove_waiting_task_id(task_id);

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
        ctx_server.queue_results.terminate();
        llama_backend_free();
    };


    //);
    start_llama_server(ctx_server);
    std::cout << "stopping" << std::endl;


    clean_up();
    t.join();

    return 0;
}
