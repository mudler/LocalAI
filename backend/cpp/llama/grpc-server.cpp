// llama.cpp gRPC C++ backend server
//
// Ettore Di Giacinto <mudler@localai.io> and llama.cpp authors
//
// This is a gRPC server for llama.cpp compatible with the LocalAI proto
// Note: this is a re-adaptation of the original llama.cpp example/server.cpp for HTTP (https://github.com/ggerganov/llama.cpp/tree/master/examples/server), 
// but modified to work with gRPC
//

#include <iostream>
#include <memory>
#include <string>
#include <getopt.h>
#include "clip.h"
#include "llava.h"
#include "log.h"
#include "stb_image.h"
#include "common.h"
#include "json.hpp"
#include "llama.h"
#include "backend.pb.h"
#include "backend.grpc.pb.h"
#include "utils.hpp"
#include "sampling.h"
// include std::regex
#include <cstddef>
#include <thread>
#include <mutex>
#include <chrono>
#include <regex>
#include <condition_variable>
#include <grpcpp/ext/proto_server_reflection_plugin.h>
#include <grpcpp/grpcpp.h>
#include <grpcpp/health_check_service_interface.h>
#include <atomic>
#include <signal.h>

using grpc::Server;
using grpc::ServerBuilder;
using grpc::ServerContext;
using grpc::Status;


using backend::HealthMessage;


///// LLAMA.CPP server code below

using json = nlohmann::json;

struct server_params
{
    std::string hostname = "127.0.0.1";
    std::vector<std::string> api_keys;
    std::string public_path = "examples/server/public";
    std::string chat_template = "";
    int32_t port = 8080;
    int32_t read_timeout = 600;
    int32_t write_timeout = 600;
    bool slots_endpoint = true;
    bool metrics_endpoint = false;
};

bool server_verbose = false;
bool server_log_json = true;

static size_t common_part(const std::vector<llama_token> &a, const std::vector<llama_token> &b)
{
    size_t i;
    for (i = 0; i < a.size() && i < b.size() && a[i] == b[i]; i++)
    {
    }
    return i;
}

enum stop_type
{
    STOP_FULL,
    STOP_PARTIAL,
};

static bool ends_with(const std::string &str, const std::string &suffix)
{
    return str.size() >= suffix.size() &&
           0 == str.compare(str.size() - suffix.size(), suffix.size(), suffix);
}

static size_t find_partial_stop_string(const std::string &stop,
                                       const std::string &text)
{
    if (!text.empty() && !stop.empty())
    {
        const char text_last_char = text.back();
        for (int64_t char_index = stop.size() - 1; char_index >= 0; char_index--)
        {
            if (stop[char_index] == text_last_char)
            {
                const std::string current_partial = stop.substr(0, char_index + 1);
                if (ends_with(text, current_partial))
                {
                    return text.size() - char_index - 1;
                }
            }
        }
    }
    return std::string::npos;
}

// TODO: reuse llama_detokenize
template <class Iter>
static std::string tokens_to_str(llama_context *ctx, Iter begin, Iter end)
{
    std::string ret;
    for (; begin != end; ++begin)
    {
        ret += common_token_to_piece(ctx, *begin);
    }
    return ret;
}

// format incomplete utf-8 multibyte character for output
static std::string tokens_to_output_formatted_string(const llama_context *ctx, const llama_token token)
{
    std::string out = token == -1 ? "" : common_token_to_piece(ctx, token);
    // if the size is 1 and first bit is 1, meaning it's a partial character
    //   (size > 1 meaning it's already a known token)
    if (out.size() == 1 && (out[0] & 0x80) == 0x80)
    {
        std::stringstream ss;
        ss << std::hex << (out[0] & 0xff);
        std::string res(ss.str());
        out = "byte: \\x" + res;
    }
    return out;
}

// convert a vector of completion_token_output to json
static json probs_vector_to_json(const llama_context *ctx, const std::vector<completion_token_output> &probs)
{
    json out = json::array();
    for (const auto &prob : probs)
    {
        json probs_for_token = json::array();
        for (const auto &p : prob.probs)
        {
            std::string tok_str = tokens_to_output_formatted_string(ctx, p.tok);
            probs_for_token.push_back(json
            {
                {"tok_str", tok_str},
                {"prob",    p.prob},
            });
        }
        std::string tok_str = tokens_to_output_formatted_string(ctx, prob.tok);
        out.push_back(json{
            {"content", tok_str},
            {"probs",   probs_for_token},
        });
    }
    return out;
}

struct llama_client_slot
{
    int id;
    int task_id = -1;

    struct slot_params params;

    slot_state state = IDLE;
    slot_command command = NONE;

    // used to determine the slot that has been used the longest
    int64_t t_last_used = -1;

    // generation props
    int32_t n_ctx       = 0;  // context size per slot
    int32_t n_past      = 0;
    int32_t n_decoded   = 0;
    int32_t n_remaining = -1;
    int32_t i_batch     = -1;
    int32_t n_predict   = -1;

    int32_t num_prompt_tokens           = 0;
    int32_t num_prompt_tokens_processed = 0;

    json prompt;
    std::string generated_text;
    llama_token sampled;
    std::vector<llama_token> cache_tokens;
    std::vector<completion_token_output> generated_token_probs;

    bool infill = false;
    bool embedding = false;
    bool has_next_token = true;
    bool truncated = false;
    bool stopped_eos = false;
    bool stopped_word = false;
    bool stopped_limit = false;

    bool oaicompat = false;
    std::string oaicompat_model;

    std::string stopping_word;

    // sampling
    struct common_params_sampling sparams;
    common_sampler *ctx_sampling = nullptr;

    int32_t ga_i = 0;   // group-attention state
    int32_t ga_n = 1;   // group-attention factor
    int32_t ga_w = 512; // group-attention width

    int32_t n_past_se = 0; // self-extend

    // multimodal
    std::vector<slot_image> images;

    // stats
    size_t sent_count = 0;
    size_t sent_token_probs_index = 0;

    int64_t t_start_process_prompt;
    int64_t t_start_genereration;

    double t_prompt_processing; // ms
    double t_token_generation; // ms

    // multitasks
    int multitask_id = -1;

    void reset() {
        num_prompt_tokens      = 0;
        generated_text         = "";
        truncated              = false;
        stopped_eos            = false;
        stopped_word           = false;
        stopped_limit          = false;
        stopping_word          = "";
        n_past                 = 0;
        sent_count             = 0;
        sent_token_probs_index = 0;
        infill                 = false;
        ga_i                   = 0;
        n_past_se              = 0;

        generated_token_probs.clear();

        for (slot_image & img : images)
        {
            free(img.image_embedding);
            if (img.img_data) {
                clip_image_u8_free(img.img_data);
            }
            img.prefix_prompt = "";
        }

        images.clear();
    }

    bool has_budget(common_params &global_params) {
        if (params.n_predict == -1 && global_params.n_predict == -1)
        {
            return true; // limitless
        }

        n_remaining = -1;

        if (params.n_predict != -1)
        {
            n_remaining = params.n_predict - n_decoded;
        }
        else if (global_params.n_predict != -1)
        {
            n_remaining = global_params.n_predict - n_decoded;
        }

        return n_remaining > 0; // no budget
    }

    bool available() const {
        return state == IDLE && command == NONE;
    }

    bool is_processing() const {
        return (state == IDLE && command == LOAD_PROMPT) || state == PROCESSING;
    }

    void add_token_string(const completion_token_output &token) {
        if (command == RELEASE)
        {
            return;
        }
        cache_tokens.push_back(token.tok);
        generated_token_probs.push_back(token);
    }

    void release() {
        if (state == PROCESSING)
        {
            t_token_generation = (ggml_time_us() - t_start_genereration) / 1e3;
            command = RELEASE;
        }
    }

    json get_formated_timings() {
        return json
        {
            {"prompt_n",               num_prompt_tokens_processed},
            {"prompt_ms",              t_prompt_processing},
            {"prompt_per_token_ms",    t_prompt_processing / num_prompt_tokens_processed},
            {"prompt_per_second",      1e3 / t_prompt_processing * num_prompt_tokens_processed},

            {"predicted_n",            n_decoded},
            {"predicted_ms",           t_token_generation},
            {"predicted_per_token_ms", t_token_generation / n_decoded},
            {"predicted_per_second",   1e3 / t_token_generation * n_decoded},
        };
    }

    void print_timings() const {
       char buffer[512];
        double t_token = t_prompt_processing / num_prompt_tokens_processed;
        double n_tokens_second = 1e3 / t_prompt_processing * num_prompt_tokens_processed;
        sprintf(buffer, "prompt eval time     = %10.2f ms / %5d tokens (%8.2f ms per token, %8.2f tokens per second)",
                t_prompt_processing, num_prompt_tokens_processed,
                t_token, n_tokens_second);
        LOG_INFO(buffer, {
            {"slot_id",                     id},
            {"task_id",                     task_id},
            {"t_prompt_processing",         t_prompt_processing},
            {"num_prompt_tokens_processed", num_prompt_tokens_processed},
            {"t_token",                     t_token},
            {"n_tokens_second",             n_tokens_second},
        });

        t_token = t_token_generation / n_decoded;
        n_tokens_second = 1e3 / t_token_generation * n_decoded;
        sprintf(buffer, "generation eval time = %10.2f ms / %5d runs   (%8.2f ms per token, %8.2f tokens per second)",
                t_token_generation, n_decoded,
                t_token, n_tokens_second);
        LOG_INFO(buffer, {
            {"slot_id",            id},
            {"task_id",            task_id},
            {"t_token_generation", t_token_generation},
            {"n_decoded",          n_decoded},
            {"t_token",            t_token},
            {"n_tokens_second",    n_tokens_second},
        });

        sprintf(buffer, "          total time = %10.2f ms", t_prompt_processing + t_token_generation);
        LOG_INFO(buffer, {
            {"slot_id",             id},
            {"task_id",             task_id},
            {"t_prompt_processing", t_prompt_processing},
            {"t_token_generation",  t_token_generation},
            {"t_total",             t_prompt_processing + t_token_generation},
        });
    }
};

struct llama_metrics {
    uint64_t n_prompt_tokens_processed_total = 0;
    uint64_t n_tokens_predicted_total        = 0;

    uint64_t n_prompt_tokens_processed = 0;
    uint64_t t_prompt_processing       = 0;

    uint64_t n_tokens_predicted       = 0;
    uint64_t t_tokens_generation      = 0;


    void on_prompt_eval(const llama_client_slot &slot) {
        n_prompt_tokens_processed_total += slot.num_prompt_tokens_processed;

        n_prompt_tokens_processed += slot.num_prompt_tokens_processed;
        t_prompt_processing       += slot.t_prompt_processing;
    }

    void on_prediction(const llama_client_slot &slot) {
        n_tokens_predicted_total += slot.n_decoded;

        n_tokens_predicted  += slot.n_decoded;
        t_tokens_generation += slot.t_token_generation;
    }

    void reset_bucket() {
        n_prompt_tokens_processed = 0;
        t_prompt_processing       = 0;
        n_tokens_predicted        = 0;
        t_tokens_generation       = 0;
    }
};

struct llava_embd_batch {
    std::vector<llama_pos>      pos;
    std::vector<int32_t>        n_seq_id;
    std::vector<llama_seq_id>   seq_id_0;
    std::vector<llama_seq_id *> seq_ids;
    std::vector<int8_t>         logits;
    llama_batch batch;
    llava_embd_batch(float * embd, int32_t n_tokens, llama_pos pos_0, llama_seq_id seq_id) {
        pos     .resize(n_tokens);
        n_seq_id.resize(n_tokens);
        seq_ids .resize(n_tokens + 1);
        logits  .resize(n_tokens);
        seq_id_0.resize(1);
        seq_id_0[0] = seq_id;
        seq_ids [n_tokens] = nullptr;
        batch = {
            /*n_tokens       =*/ n_tokens,
            /*tokens         =*/ nullptr,
            /*embd           =*/ embd,
            /*pos            =*/ pos.data(),
            /*n_seq_id       =*/ n_seq_id.data(),
            /*seq_id         =*/ seq_ids.data(),
            /*logits         =*/ logits.data(),
        };
        for (int i = 0; i < n_tokens; i++) {
            batch.pos     [i] = pos_0 + i;
            batch.n_seq_id[i] = 1;
            batch.seq_id  [i] = seq_id_0.data();
            batch.logits  [i] = false;
        }
    }
};

struct llama_server_context
{
    llama_model *model = nullptr;
    llama_context *ctx = nullptr;

    clip_ctx *clp_ctx = nullptr;

    common_params params;

    llama_batch batch;

    bool multimodal         = false;
    bool clean_kv_cache     = true;
    bool all_slots_are_idle = false;
    bool add_bos_token      = true;

    int32_t n_ctx;  // total context for all clients / slots

    // system prompt
    bool system_need_update = false;

    std::string              system_prompt;
    std::vector<llama_token> system_tokens;

    std::string name_user;      // this should be the antiprompt
    std::string name_assistant;

    // slots / clients
    std::vector<llama_client_slot> slots;
    json default_generation_settings_for_props;

    llama_server_queue queue_tasks;
    llama_server_response queue_results;

    llama_metrics metrics;

    ~llama_server_context()
    {
        if (ctx)
        {
            llama_free(ctx);
            ctx = nullptr;
        }
        if (model)
        {
            llama_free_model(model);
            model = nullptr;
        }
    }

    bool load_model(const common_params &params_)
    {
        params = params_;
        if (!params.mmproj.empty()) {
            multimodal = true;
            LOG_INFO("Multi Modal Mode Enabled", {});
            clp_ctx = clip_model_load(params.mmproj.c_str(), /*verbosity=*/ 1);
            if(clp_ctx == nullptr) {
                LOG_ERR("unable to load clip model: %s", params.mmproj.c_str());
                return false;
            }

            if (params.n_ctx < 2048) { // request larger context for the image embedding
                params.n_ctx = 2048;
            }
        }

        common_init_result common_init = common_init_from_params(params);
        model = common_init.model;
        ctx = common_init.context;
        if (model == nullptr)
        {
            LOG_ERR("unable to load model: %s", params.model.c_str());
            return false;
        }

        if (multimodal) {
            const int n_embd_clip = clip_n_mmproj_embd(clp_ctx);
            const int n_embd_llm  = llama_n_embd(model);
            if (n_embd_clip != n_embd_llm) {
                LOG("%s: embedding dim of the multimodal projector (%d) is not equal to that of LLaMA (%d). Make sure that you use the correct mmproj file.\n", __func__, n_embd_clip, n_embd_llm);
                llama_free(ctx);
                llama_free_model(model);
                return false;
            }
        }

        n_ctx = llama_n_ctx(ctx);

        add_bos_token = llama_add_bos_token(model);

        return true;
    }

    void validate_model_chat_template(server_params & sparams) {
        llama_chat_message chat[] = {{"user", "test"}};
        std::vector<char> buf(1);
        int res = llama_chat_apply_template(model, nullptr, chat, 1, true, buf.data(), buf.size());
        if (res < 0) {
            LOG_ERR("The chat template comes with this model is not yet supported, falling back to chatml. This may cause the model to output suboptimal responses", __func__);
            sparams.chat_template = "<|im_start|>"; // llama_chat_apply_template only checks if <|im_start|> exist in the template
        }
    }

    llama_client_slot* get_active_slot() {
        for (llama_client_slot& slot : slots) {
            // Check if the slot is currently processing
            if (slot.is_processing()) {
                return &slot;  // Return the active slot
            }
        }
        return nullptr;  // No active slot found
    }

    void initialize() {
        // create slots
        all_slots_are_idle = true;

        const int32_t n_ctx_slot = n_ctx / params.n_parallel;

        LOG_INFO("initializing slots", {{"n_slots", params.n_parallel}});
        for (int i = 0; i < params.n_parallel; i++)
        {
            llama_client_slot slot;

            slot.id = i;
            slot.n_ctx = n_ctx_slot;
            slot.n_predict = params.n_predict;

            LOG_INFO("new slot", {
                {"slot_id",    slot.id},
                {"n_ctx_slot", slot.n_ctx}
            });

            const int ga_n = params.grp_attn_n;
            const int ga_w = params.grp_attn_w;

            if (ga_n != 1) {
                GGML_ASSERT(ga_n > 0                    && "ga_n must be positive");                     // NOLINT
                GGML_ASSERT(ga_w % ga_n == 0            && "ga_w must be a multiple of ga_n");     // NOLINT
                //GGML_ASSERT(n_ctx_train % ga_w == 0     && "n_ctx_train must be a multiple of ga_w");    // NOLINT
                //GGML_ASSERT(n_ctx >= n_ctx_train * ga_n && "n_ctx must be at least n_ctx_train * ga_n"); // NOLINT

                LOG_INFO("slot self-extend", {
                    {"slot_id",   slot.id},
                    {"ga_n",      ga_n},
                    {"ga_w",      ga_w}
                });
            }

            slot.ga_i = 0;
            slot.ga_n = ga_n;
            slot.ga_w = ga_w;

            slot.reset();

            slots.push_back(slot);
        }

        default_generation_settings_for_props = get_formated_generation(slots.front());
        default_generation_settings_for_props["seed"] = -1;

        batch = llama_batch_init(n_ctx, 0, params.n_parallel);
    }

    std::vector<llama_token> tokenize(const json & json_prompt, bool add_bos) const
    {
        // TODO: currently, we tokenize using special tokens by default
        //       this is not always correct (see https://github.com/ggerganov/llama.cpp/pull/4160#issuecomment-1824826216)
        //       but it's better compared to completely ignoring ChatML and other chat templates
        const bool TMP_FORCE_SPECIAL = true;

        // If `add_bos` is true, we only add BOS, when json_prompt is a string,
        // or the first element of the json_prompt array is a string.
        std::vector<llama_token> prompt_tokens;

        if (json_prompt.is_array())
        {
            bool first = true;
            for (const auto& p : json_prompt)
            {
                if (p.is_string())
                {
                    auto s = p.template get<std::string>();
                    std::vector<llama_token> p;
                    if (first)
                    {
                        p = common_tokenize(ctx, s, add_bos, TMP_FORCE_SPECIAL);
                        first = false;
                    }
                    else
                    {
                        p = common_tokenize(ctx, s, false, TMP_FORCE_SPECIAL);
                    }
                    prompt_tokens.insert(prompt_tokens.end(), p.begin(), p.end());
                }
                else
                {
                    if (first)
                    {
                        first = false;
                    }
                    prompt_tokens.push_back(p.template get<llama_token>());
                }
            }
        }
        else
        {
            auto s = json_prompt.template get<std::string>();
            prompt_tokens = common_tokenize(ctx, s, add_bos, TMP_FORCE_SPECIAL);
        }

        return prompt_tokens;
    }

    llama_client_slot* get_slot(int id) {
        int64_t t_last = ggml_time_us();
        llama_client_slot *last_used = nullptr;

        for (llama_client_slot & slot : slots)
        {
            if (slot.id == id && slot.available())
            {
                return &slot;
            }

            if (slot.available() && slot.t_last_used < t_last)
            {
                last_used = &slot;
                t_last = slot.t_last_used;
            }
        }

        return last_used;
    }

    bool launch_slot_with_data(llama_client_slot* &slot, json data) {
        slot_params default_params;
        common_params_sampling default_sparams;
 
        slot->params.stream             = json_value(data, "stream",            false);
        slot->params.cache_prompt       = json_value(data, "cache_prompt",      false);
        slot->params.n_predict          = json_value(data, "n_predict",         default_params.n_predict);
        slot->sparams.top_k             = json_value(data, "top_k",             default_sparams.top_k);
        slot->sparams.top_p             = json_value(data, "top_p",             default_sparams.top_p);
        slot->sparams.min_p             = json_value(data, "min_p",             default_sparams.min_p);
        slot->sparams.typ_p             = json_value(data, "typical_p",         default_sparams.typ_p);
        slot->sparams.temp              = json_value(data, "temperature",       default_sparams.temp);
        slot->sparams.dynatemp_range    = json_value(data, "dynatemp_range",    default_sparams.dynatemp_range);
        slot->sparams.dynatemp_exponent = json_value(data, "dynatemp_exponent", default_sparams.dynatemp_exponent);
        slot->sparams.penalty_last_n    = json_value(data, "repeat_last_n",     default_sparams.penalty_last_n);
        slot->sparams.penalty_repeat    = json_value(data, "repeat_penalty",    default_sparams.penalty_repeat);
        slot->sparams.penalty_freq      = json_value(data, "frequency_penalty", default_sparams.penalty_freq);
        slot->sparams.penalty_present   = json_value(data, "presence_penalty",  default_sparams.penalty_present);
        slot->sparams.mirostat          = json_value(data, "mirostat",          default_sparams.mirostat);
        slot->sparams.mirostat_tau      = json_value(data, "mirostat_tau",      default_sparams.mirostat_tau);
        slot->sparams.mirostat_eta      = json_value(data, "mirostat_eta",      default_sparams.mirostat_eta);
        slot->params.n_keep             = json_value(data, "n_keep",            slot->params.n_keep);
        slot->sparams.seed               = json_value(data, "seed",              default_sparams.seed);
        slot->sparams.grammar           = json_value(data, "grammar",           default_sparams.grammar);
        slot->sparams.n_probs           = json_value(data, "n_probs",           default_sparams.n_probs);
        slot->sparams.min_keep          = json_value(data, "min_keep",          default_sparams.min_keep);

        if (slot->n_predict > 0 && slot->params.n_predict > slot->n_predict) {
            // Might be better to reject the request with a 400 ?
            LOG_WARNING("Max tokens to predict exceeds server configuration", {
                {"params.n_predict", slot->params.n_predict},
                {"slot.n_predict", slot->n_predict},
            });
            slot->params.n_predict = slot->n_predict;
        }

        // infill
        if (data.count("input_prefix") != 0)
        {
            slot->params.input_prefix = data["input_prefix"];
        }
        else
        {
            slot->params.input_prefix = "";
        }


        if (data.count("input_suffix") != 0)
        {
            slot->params.input_suffix = data["input_suffix"];
        }
        else
        {
            slot->params.input_suffix = "";
        }

        if (data.count("prompt") != 0)
        {
            slot->prompt = data["prompt"];
        }
        else
        {
            slot->prompt = "";
        }

        if (json_value(data, "ignore_eos", false)) {
                slot->sparams.logit_bias.push_back({llama_token_eos(model), -INFINITY});
        }
        /*
        slot->sparams.penalty_prompt_tokens.clear();
        slot->sparams.use_penalty_prompt_tokens = false;
        const auto &penalty_prompt = data.find("penalty_prompt");
        if (penalty_prompt != data.end())
        {
            if (penalty_prompt->is_string())
            {
                const auto penalty_prompt_string = penalty_prompt->get<std::string>();
                auto penalty_tokens = llama_tokenize(model, penalty_prompt_string, false);
                slot->sparams.penalty_prompt_tokens.swap(penalty_tokens);
                if (slot->params.n_predict > 0)
                {
                    slot->sparams.penalty_prompt_tokens.reserve(slot->sparams.penalty_prompt_tokens.size() + slot->params.n_predict);
                }
                slot->sparams.use_penalty_prompt_tokens = true;
            }
            else if (penalty_prompt->is_array())
            {
                const auto n_tokens = penalty_prompt->size();
                slot->sparams.penalty_prompt_tokens.reserve(n_tokens + std::max(0, slot->params.n_predict));
                const int n_vocab = llama_n_vocab(model);
                for (const auto &penalty_token : *penalty_prompt)
                {
                    if (penalty_token.is_number_integer())
                    {
                        const auto tok = penalty_token.get<llama_token>();
                        if (tok >= 0 && tok < n_vocab)
                        {
                            slot->sparams.penalty_prompt_tokens.push_back(tok);
                        }
                    }
                }
                slot->sparams.use_penalty_prompt_tokens = true;
            }
        }
      */

        slot->sparams.logit_bias.clear();

        const auto &logit_bias = data.find("logit_bias");
        if (logit_bias != data.end() && logit_bias->is_array())
        {
            const int n_vocab = llama_n_vocab(model);
            for (const auto &el : *logit_bias)
            {
                if (el.is_array() && el.size() == 2)
                {
                    float bias;
                    if (el[1].is_number())
                    {
                        bias = el[1].get<float>();
                    }
                    else if (el[1].is_boolean() && !el[1].get<bool>())
                    {
                        bias = -INFINITY;
                    }
                    else
                    {
                        continue;
                    }

                    if (el[0].is_number_integer())
                    {
                        llama_token tok = el[0].get<llama_token>();
                        if (tok >= 0 && tok < n_vocab)
                        {
                            slot->sparams.logit_bias.push_back({tok, bias});
                        }
                    }
                    else if (el[0].is_string())
                    {
                        auto toks = common_tokenize(model, el[0].get<std::string>(), false);
                        for (auto tok : toks)
                        {
                            slot->sparams.logit_bias.push_back({tok, bias});
                        }
                    }
                }
            }
        }
        
        slot->params.antiprompt.clear();

        const auto &stop = data.find("stop");
        if (stop != data.end() && stop->is_array())
        {
            for (const auto &word : *stop)
            {
                if (!word.empty())
                {
                    slot->params.antiprompt.push_back(word);
                }
            }
        }
        
        const auto & samplers = data.find("samplers");
        if (samplers != data.end() && samplers->is_array()) {
            std::vector<std::string> sampler_names;
                for (const auto & name : *samplers) {
                    if (name.is_string()) {
                        sampler_names.emplace_back(name);
                    }
                }
                slot->sparams.samplers = common_sampler_types_from_names(sampler_names, false);
        }
        else
        {
                slot->sparams.samplers = default_sparams.samplers;
        }
        

        if (multimodal)
        {
            const auto &images_data = data.find("image_data");
            if (images_data != data.end() && images_data->is_array())
            {
                for (const auto &img : *images_data)
                {
                    const std::vector<uint8_t> image_buffer = base64_decode(img["data"].get<std::string>());

                    slot_image img_sl;
                    img_sl.id = img.count("id") != 0 ? img["id"].get<int>() : slot->images.size();
                    img_sl.img_data = clip_image_u8_init();
                    if (!clip_image_load_from_bytes(image_buffer.data(), image_buffer.size(), img_sl.img_data))
                    {
                        LOG_ERR("%s: failed to load image, slot_id: %d, img_sl_id: %d", 
                             __func__,
                             slot->id,
                             img_sl.id
                        );
                        return false;
                    }
                    LOG_VERBOSE("image loaded", {
                        {"slot_id",   slot->id},
                        {"img_sl_id", img_sl.id}
                    });
                    img_sl.request_encode_image = true;
                    slot->images.push_back(img_sl);
                }
                // process prompt
                // example: system prompt [img-102] user [img-103] describe [img-134] -> [{id: 102, prefix: 'system prompt '}, {id: 103, prefix: ' user '}, {id: 134, prefix: ' describe '}]}
                if (slot->images.size() > 0 && !slot->prompt.is_array())
                {
                    std::string prompt = slot->prompt.get<std::string>();
                    size_t pos = 0, begin_prefix = 0;
                    std::string pattern = "[img-";
                    while ((pos = prompt.find(pattern, pos)) != std::string::npos) {
                        size_t end_prefix = pos;
                        pos += pattern.length();
                        size_t end_pos = prompt.find(']', pos);
                        if (end_pos != std::string::npos)
                        {
                            std::string image_id = prompt.substr(pos, end_pos - pos);
                            try
                            {
                                int img_id = std::stoi(image_id);
                                bool found = false;
                                for (slot_image &img : slot->images)
                                {
                                    if (img.id == img_id) {
                                        found = true;
                                        img.prefix_prompt = prompt.substr(begin_prefix, end_prefix - begin_prefix);
                                        begin_prefix = end_pos + 1;
                                        break;
                                    }
                                }
                                if (!found) {
                                    LOG("ERROR: Image with id: %i, not found.\n", img_id);
                                    slot->images.clear();
                                    return false;
                                }
                            } catch (const std::invalid_argument& e) {
                                LOG("Invalid image number id in prompt\n");
                                slot->images.clear();
                                return false;
                            }
                        }
                    }
                    slot->prompt = "";
                    slot->params.input_suffix = prompt.substr(begin_prefix);
                    slot->params.cache_prompt = false; // multimodal doesn't support cache prompt
                }
            }
        }

        if (slot->ctx_sampling != nullptr)
        {
            common_sampler_free(slot->ctx_sampling);
        }
        slot->ctx_sampling = common_sampler_init(model, slot->sparams);
        //llama_set_rng_seed(ctx, slot->params.seed);
        slot->command = LOAD_PROMPT;

        all_slots_are_idle = false;

        LOG_INFO("slot is processing task", {
            {"slot_id", slot->id},
            {"task_id", slot->task_id},
        });

      //  LOG("sampling: \n%s\n", llama_sampling_print(slot->sparams).c_str());

        return true;
    }

    void kv_cache_clear() {
        // clear the entire KV cache
        llama_kv_cache_clear(ctx);
        clean_kv_cache = false;
    }

    void update_system_prompt() {
        kv_cache_clear();
        system_tokens.clear();

        if (!system_prompt.empty()) {
            system_tokens = common_tokenize(ctx, system_prompt, add_bos_token);

            common_batch_clear(batch);

            for (int i = 0; i < (int)system_tokens.size(); ++i)
            {
                common_batch_add(batch, system_tokens[i], i, { 0 }, false);
            }

            for (int32_t i = 0; i < (int32_t) batch.n_tokens; i += params.n_batch)
            {
                const int32_t n_tokens = std::min(params.n_batch, (int32_t) (batch.n_tokens - i));
                llama_batch batch_view = {
                    n_tokens,
                    batch.token    + i,
                    nullptr,
                    batch.pos      + i,
                    batch.n_seq_id + i,
                    batch.seq_id   + i,
                    batch.logits   + i,
                };
                if (llama_decode(ctx, batch_view) != 0)
                {
                    LOG("%s: llama_decode() failed\n", __func__);
                    return;
                }
            }

            // assign the system KV cache to all parallel sequences
            for (int32_t i = 1; i < params.n_parallel; ++i)
            {
                llama_kv_cache_seq_cp(ctx, 0, i, 0, system_tokens.size());
            }
        }

        LOG("system prompt updated\n");
        system_need_update = false;
    }

    void notify_system_prompt_changed() {
        // release all slots
        for (llama_client_slot &slot : slots)
        {
            slot.release();
        }

        system_need_update = true;
    }

    void process_system_prompt_data(const json &sys_props) {
        system_prompt  = sys_props.value("prompt", "");
        name_user      = sys_props.value("anti_prompt", "");
        name_assistant = sys_props.value("assistant_name", "");


        notify_system_prompt_changed();
    }

    static size_t find_stopping_strings(const std::string &text, const size_t last_token_size,
                                        const stop_type type, llama_client_slot &slot)
    {
        size_t stop_pos = std::string::npos;

        for (const std::string &word : slot.params.antiprompt)
        {
            size_t pos;
            if (type == STOP_FULL)
            {
                const size_t tmp = word.size() + last_token_size;
                const size_t from_pos = text.size() > tmp ? text.size() - tmp : 0;
                pos = text.find(word, from_pos);
            }
            else
            {
                pos = find_partial_stop_string(word, text);
            }
            if (pos != std::string::npos &&
                (stop_pos == std::string::npos || pos < stop_pos))
            {
                if (type == STOP_FULL)
                {
                    slot.stopped_word = true;
                    slot.stopping_word = word;
                    slot.has_next_token = false;
                }
                stop_pos = pos;
            }
        }

        return stop_pos;
    }

    bool process_token(completion_token_output &result, llama_client_slot &slot) {
        // remember which tokens were sampled - used for repetition penalties during sampling
        const std::string token_str = common_token_to_piece(ctx, result.tok);
        slot.sampled = result.tok;

        // search stop word and delete it
        slot.generated_text += token_str;
        slot.has_next_token = true;

/*
        if (slot.ctx_sampling->params.use_penalty_prompt_tokens && result.tok != -1)
        {
            // we can change penalty_prompt_tokens because it is always created from scratch each request
            slot.ctx_sampling->params.penalty_prompt_tokens.push_back(result.tok);
        }
        */

        // check if there is incomplete UTF-8 character at the end
        bool incomplete = false;
        for (unsigned i = 1; i < 5 && i <= slot.generated_text.size(); ++i)
        {
            unsigned char c = slot.generated_text[slot.generated_text.size() - i];
            if ((c & 0xC0) == 0x80)
            {
                // continuation byte: 10xxxxxx
                continue;
            }
            if ((c & 0xE0) == 0xC0)
            {
                // 2-byte character: 110xxxxx ...
                incomplete = i < 2;
            }
            else if ((c & 0xF0) == 0xE0)
            {
                // 3-byte character: 1110xxxx ...
                incomplete = i < 3;
            }
            else if ((c & 0xF8) == 0xF0)
            {
                // 4-byte character: 11110xxx ...
                incomplete = i < 4;
            }
            // else 1-byte character or invalid byte
            break;
        }

        if (!incomplete)
        {
            size_t pos = std::min(slot.sent_count, slot.generated_text.size());
            const std::string str_test = slot.generated_text.substr(pos);
            bool is_stop_full = false;
            size_t stop_pos = find_stopping_strings(str_test, token_str.size(), STOP_FULL, slot);
            if (stop_pos != std::string::npos)
            {
                is_stop_full = true;
                slot.generated_text.erase(
                    slot.generated_text.begin() + pos + stop_pos,
                    slot.generated_text.end());
                pos = std::min(slot.sent_count, slot.generated_text.size());
            }
            else
            {
                is_stop_full = false;
                stop_pos = find_stopping_strings(str_test, token_str.size(), STOP_PARTIAL, slot);
            }

            // check if there is any token to predict
            if (stop_pos == std::string::npos || (!slot.has_next_token && !is_stop_full && stop_pos > 0))
            {
                // no send the stop word in the response
                result.text_to_send = slot.generated_text.substr(pos, std::string::npos);
                slot.sent_count += result.text_to_send.size();
                // add the token to slot queue and cache
            }
            slot.add_token_string(result);
            if (slot.params.stream)
            {
                send_partial_response(slot, result);
            }
        }

        if (incomplete)
        {
            slot.has_next_token = true;
        }

        // check the limits
        if (slot.n_decoded > 0 && slot.has_next_token && !slot.has_budget(params))
        {
            slot.stopped_limit = true;
            slot.has_next_token = false;
        }

        if (result.tok == llama_token_eos(model))
        {
            slot.stopped_eos = true;
            slot.has_next_token = false;
            LOG_VERBOSE("eos token found", {});
        }

        LOG_VERBOSE("next token", {
                                      {"token", result.tok},
                                      {"token_text", tokens_to_output_formatted_string(ctx, result.tok)},
                                      {"has_next_token", slot.has_next_token},
                                      {"n_remain", slot.n_remaining},
                                      {"num_tokens_predicted", slot.n_decoded},
                                      {"stopped_eos", slot.stopped_eos},
                                      {"stopped_word", slot.stopped_word},
                                      {"stopped_limit", slot.stopped_limit},
                                      {"stopping_word", slot.stopping_word},
                                  });

        return slot.has_next_token; // continue
    }

    bool process_images(llama_client_slot &slot) const
    {
        for (slot_image &img : slot.images)
        {
            if (!img.request_encode_image)
            {
                continue;
            }

            if (!llava_image_embed_make_with_clip_img(clp_ctx, params.cpuparams.n_threads, img.img_data, &img.image_embedding, &img.image_tokens)) {
                LOG("Error processing the given image");
                return false;
            }

            img.request_encode_image = false;
        }

        return slot.images.size() > 0;
    }

    void send_error(task_server& task, const std::string &error)
    {
        LOG("task %i - error: %s\n", task.id, error.c_str());
        task_result res;
        res.id = task.id;
        res.multitask_id = task.multitask_id;
        res.stop = false;
        res.error = true;
        res.result_json = { { "content", error } };
        queue_results.send(res);
    }

    json get_formated_generation(llama_client_slot &slot)
    {
        std::vector<std::string> samplers;
        samplers.reserve(slot.sparams.samplers.size());
        for (const auto & sampler : slot.sparams.samplers)
        {
            samplers.emplace_back(common_sampler_type_to_str(sampler));
        }

        return json {
            {"n_ctx",             slot.n_ctx},
            {"n_predict",         slot.n_predict},
            {"model",             params.model_alias},
            {"seed",              slot.params.seed},
            {"temperature",       slot.sparams.temp},
            {"dynatemp_range",    slot.sparams.dynatemp_range},
            {"dynatemp_exponent", slot.sparams.dynatemp_exponent},
            {"top_k",             slot.sparams.top_k},
            {"top_p",             slot.sparams.top_p},
            {"min_p",             slot.sparams.min_p},
            {"typical_p",         slot.sparams.typ_p},
            {"repeat_last_n",     slot.sparams.penalty_last_n},
            {"repeat_penalty",    slot.sparams.penalty_repeat},
            {"presence_penalty",  slot.sparams.penalty_present},
            {"frequency_penalty", slot.sparams.penalty_freq},
            {"mirostat",          slot.sparams.mirostat},
            {"mirostat_tau",      slot.sparams.mirostat_tau},
            {"mirostat_eta",      slot.sparams.mirostat_eta},
            {"stop",              slot.params.antiprompt},
            {"n_predict",         slot.params.n_predict},
            {"n_keep",            params.n_keep},
            {"ignore_eos",        slot.sparams.ignore_eos},
            {"stream",            slot.params.stream},
             //      {"logit_bias",        slot.sparams.logit_bias},
            {"n_probs",           slot.sparams.n_probs},
            {"min_keep",          slot.sparams.min_keep},
            {"grammar",           slot.sparams.grammar},
            {"samplers",          samplers}
        };
    }

    void send_partial_response(llama_client_slot &slot, completion_token_output tkn)
    {
        task_result res;
        res.id = slot.task_id;
        res.multitask_id = slot.multitask_id;
        res.error = false;
        res.stop = false;

        res.result_json = json
        {
            {"content",    tkn.text_to_send},
            {"stop",       false},
            {"slot_id",    slot.id},
            {"multimodal", multimodal}
        };

        if (slot.sparams.n_probs > 0)
        {
            std::vector<completion_token_output> probs_output = {};
            const std::vector<llama_token> to_send_toks = common_tokenize(ctx, tkn.text_to_send, false);
            size_t probs_pos      = std::min(slot.sent_token_probs_index,                       slot.generated_token_probs.size());
            size_t probs_stop_pos = std::min(slot.sent_token_probs_index + to_send_toks.size(), slot.generated_token_probs.size());
            if (probs_pos < probs_stop_pos)
            {
                probs_output = std::vector<completion_token_output>(slot.generated_token_probs.begin() + probs_pos, slot.generated_token_probs.begin() + probs_stop_pos);
            }
            slot.sent_token_probs_index = probs_stop_pos;
            res.result_json["completion_probabilities"] = probs_vector_to_json(ctx, probs_output);
        }

        if (slot.oaicompat)
        {
            res.result_json["oaicompat_token_ctr"] = slot.n_decoded;
            res.result_json["model"] = slot.oaicompat_model;
        }

        queue_results.send(res);
    }

    void send_final_response(llama_client_slot &slot)
    {
        task_result res;
        res.id = slot.task_id;
        res.multitask_id = slot.multitask_id;
        res.error = false;
        res.stop = true;

        res.result_json = json
        {
            {"content",             !slot.params.stream ? slot.generated_text : ""},
            {"slot_id",             slot.id},
            {"stop",                true},
            {"model",               params.model_alias},
            {"tokens_predicted",    slot.n_decoded},
            {"tokens_evaluated",    slot.num_prompt_tokens},
            {"generation_settings", get_formated_generation(slot)},
            {"prompt",              slot.prompt},
            {"truncated",           slot.truncated},
            {"stopped_eos",         slot.stopped_eos},
            {"stopped_word",        slot.stopped_word},
            {"stopped_limit",       slot.stopped_limit},
            {"stopping_word",       slot.stopping_word},
            {"tokens_cached",       slot.n_past},
            {"timings",             slot.get_formated_timings()}
        };

        if (slot.sparams.n_probs > 0)
        {
            std::vector<completion_token_output> probs = {};
            if (!slot.params.stream && slot.stopped_word)
            {
                const std::vector<llama_token> stop_word_toks = common_tokenize(ctx, slot.stopping_word, false);
                probs = std::vector<completion_token_output>(slot.generated_token_probs.begin(), slot.generated_token_probs.end() - stop_word_toks.size());
            }
            else
            {
                probs = std::vector<completion_token_output>(
                                    slot.generated_token_probs.begin(),
                                    slot.generated_token_probs.end());
            }
            res.result_json["completion_probabilities"] = probs_vector_to_json(ctx, probs);
        }

        if (slot.oaicompat)
        {
            res.result_json["oaicompat_token_ctr"] = slot.n_decoded;
            res.result_json["model"] = slot.oaicompat_model;
        }

        queue_results.send(res);
    }

    void send_embedding(llama_client_slot &slot)
    {
        task_result res;
        res.id = slot.task_id;
        res.multitask_id = slot.multitask_id;
        res.error = false;
        res.stop = true;

        const int n_embd = llama_n_embd(model);
        if (!params.embedding)
        {
            LOG_WARNING("embedding disabled", {
                                                  {"params.embedding", params.embedding},
                                              });
            res.result_json = json
            {
                {"embedding", std::vector<float>(n_embd, 0.0f)},
            };
        }
        else
        {
            const float *data = llama_get_embeddings(ctx);
            std::vector<float> embedding(data, data + n_embd);
            res.result_json = json
            {
                {"embedding", embedding },
            };
        }
        queue_results.send(res);
    }

    void request_completion(int task_id, json data, bool infill, bool embedding, int multitask_id)
    {
        task_server task;
        task.id = task_id;
        task.target_id = 0;
        task.data = std::move(data);
        task.infill_mode = infill;
        task.embedding_mode = embedding;
        task.type = TASK_TYPE_COMPLETION;
        task.multitask_id = multitask_id;

        // when a completion task's prompt array is not a singleton, we split it into multiple requests
        // otherwise, it's a single-prompt task, we actually queue it
        // if there's numbers in the prompt array it will be treated as an array of tokens
        if (task.data.count("prompt") != 0 && task.data.at("prompt").size() > 1) {
            bool numbers = false;
            for (const auto& e : task.data.at("prompt")) {
                if (e.is_number()) {
                    numbers = true;
                    break;
                }
            }

            // NOTE: split_multiprompt_task() does not handle a mix of strings and numbers,
            // it will completely stall the server. I don't know where the bug for this is.
            //
            // if there are numbers, it needs to be treated like a single prompt,
            // queue_tasks handles a mix of strings and numbers just fine.
            if (numbers) {
                queue_tasks.post(task);
            } else {
                split_multiprompt_task(task_id, task);
            }
        } else {
            queue_tasks.post(task);
        }
    }

    // for multiple images processing
    bool ingest_images(llama_client_slot &slot, int n_batch)
    {
        int image_idx = 0;

        while (image_idx < (int) slot.images.size())
        {
            slot_image &img = slot.images[image_idx];

            // process prefix prompt
            for (int32_t i = 0; i < (int32_t) batch.n_tokens; i += n_batch)
            {
                const int32_t n_tokens = std::min(n_batch, (int32_t) (batch.n_tokens - i));
                llama_batch batch_view = {
                    n_tokens,
                    batch.token    + i,
                    nullptr,
                    batch.pos      + i,
                    batch.n_seq_id + i,
                    batch.seq_id   + i,
                    batch.logits   + i,
                };
                if (llama_decode(ctx, batch_view))
                {
                    LOG("%s : failed to eval\n", __func__);
                    return false;
                }
            }

            // process image with llm
            for (int i = 0; i < img.image_tokens; i += n_batch)
            {
                int n_eval = img.image_tokens - i;
                if (n_eval > n_batch)
                {
                    n_eval = n_batch;
                }

                const int n_embd = llama_n_embd(model);
                float * embd = img.image_embedding + i * n_embd;
                llava_embd_batch llava_batch = llava_embd_batch(embd, n_eval, slot.n_past, 0);
                if (llama_decode(ctx, llava_batch.batch))
                {
                    LOG("%s : failed to eval image\n", __func__);
                    return false;
                }
                slot.n_past += n_eval;
            }
            image_idx++;

            common_batch_clear(batch);

            // append prefix of next image
            const auto json_prompt = (image_idx >= (int) slot.images.size()) ?
                slot.params.input_suffix : // no more images, then process suffix prompt
                (json)(slot.images[image_idx].prefix_prompt);

            std::vector<llama_token> append_tokens = tokenize(json_prompt, false); // has next image
            for (int i = 0; i < (int) append_tokens.size(); ++i)
            {
                common_batch_add(batch, append_tokens[i], system_tokens.size() + slot.n_past, { slot.id }, true);
                slot.n_past += 1;
            }
        }

        return true;
    }

    void request_cancel(int task_id)
    {
        task_server task;
        task.type = TASK_TYPE_CANCEL;
        task.target_id = task_id;
        queue_tasks.post(task);
    }

    void split_multiprompt_task(int multitask_id, task_server& multiprompt_task)
    {
        int prompt_count = multiprompt_task.data.at("prompt").size();
        if (prompt_count <= 1) {
            send_error(multiprompt_task, "error while handling multiple prompts");
            return;
        }

        // generate all the ID for subtask
        std::vector<int> subtask_ids(prompt_count);
        for (int i = 0; i < prompt_count; i++)
        {
            subtask_ids[i] = queue_tasks.get_new_id();
        }

        // queue up the multitask so we can track its subtask progression
        queue_tasks.add_multitask(multitask_id, subtask_ids);

        // add subtasks
        for (int i = 0; i < prompt_count; i++)
        {
            json subtask_data = multiprompt_task.data;
            subtask_data["prompt"] = subtask_data["prompt"][i];

            // subtasks inherit everything else (infill mode, embedding mode, etc.)
            request_completion(subtask_ids[i], subtask_data, multiprompt_task.infill_mode, multiprompt_task.embedding_mode, multitask_id);
        }
    }

    void process_single_task(task_server& task)
    {
        switch (task.type)
        {
            case TASK_TYPE_COMPLETION: {
                llama_client_slot *slot = get_slot(json_value(task.data, "slot_id", -1));
                if (slot == nullptr)
                {
                    // if no slot is available, we defer this task for processing later
                    LOG_VERBOSE("no slot is available", {{"task_id", task.id}});
                    queue_tasks.defer(task);
                    break;
                }

                if (task.data.contains("system_prompt"))
                {
                    if (!all_slots_are_idle) {
                        send_error(task, "system prompt can only be updated when all slots are idle");
                        break;
                    }
                    process_system_prompt_data(task.data["system_prompt"]);

                    // reset cache_tokens for all slots
                    for (llama_client_slot &slot : slots)
                    {
                        slot.cache_tokens.clear();
                        slot.n_past    = 0;
                        slot.n_past_se = 0;
                    }
                }

                slot->reset();

                slot->infill       = task.infill_mode;
                slot->embedding    = task.embedding_mode;
                slot->task_id      = task.id;
                slot->multitask_id = task.multitask_id;

                if (!launch_slot_with_data(slot, task.data))
                {
                    // send error result
                    send_error(task, "internal_error");
                    break;
                }
            } break;
            case TASK_TYPE_CANCEL: { // release slot linked with the task id
                for (auto & slot : slots)
                {
                    if (slot.task_id == task.target_id)
                    {
                        slot.release();
                        break;
                    }
                }
            } break;
            case TASK_TYPE_NEXT_RESPONSE: {
                // do nothing
            } break;
        }
    }

    void on_finish_multitask(task_multi& multitask)
    {
        // all subtasks done == multitask is done
        task_result result;
        result.id = multitask.id;
        result.stop = true;
        result.error = false;

        // collect json results into one json result
        std::vector<json> result_jsons;
        for (auto& subres : multitask.results)
        {
            result_jsons.push_back(subres.result_json);
            result.error = result.error && subres.error;
        }
        result.result_json = json{ { "results", result_jsons } };
        queue_results.send(result);
    }

    bool update_slots() {
        if (system_need_update)
        {
            LOG_INFO("updating system prompt", {});
            update_system_prompt();
        }

        common_batch_clear(batch);

        if (all_slots_are_idle)
        {
            if (system_prompt.empty() && clean_kv_cache)
            {
                LOG_INFO("all slots are idle and system prompt is empty, clear the KV cache", {});
                kv_cache_clear();
            }
            return true;
        }

        LOG_VERBOSE("posting NEXT_RESPONSE", {});
        task_server task;
        task.type = TASK_TYPE_NEXT_RESPONSE;
        task.target_id = -1;
        queue_tasks.post(task);

        for (llama_client_slot &slot : slots)
        {
            if (slot.ga_n == 1)
            {
                if (slot.is_processing() && system_tokens.size() + slot.cache_tokens.size() >= (size_t) slot.n_ctx)
                {
                    // START LOCALAI changes
                    // Temporary disable context-shifting as it can lead to infinite loops (issue: https://github.com/ggerganov/llama.cpp/issues/3969)
                    // See: https://github.com/mudler/LocalAI/issues/1333
                    // Context is exhausted, release the slot
                    slot.release();
                    send_final_response(slot);
                    slot.cache_tokens.clear();
                    slot.n_past = 0;
                    slot.truncated = false;
                    slot.has_next_token = true;
                    LOG("Context exhausted. Slot %d released (%d tokens in cache)\n", slot.id, (int) slot.cache_tokens.size());

                    continue;
                    // END LOCALAI changes
                }
            }
        }

        // decode any currently ongoing sequences
        LOG_VERBOSE("decoding ongoing sequences", {});
        for (auto & slot : slots)
        {
            // release the slot
            if (slot.command == RELEASE)
            {
                slot.state = IDLE;
                slot.command = NONE;
                slot.t_last_used = ggml_time_us();

                LOG_INFO("slot released", {
                    {"slot_id",         slot.id},
                    {"task_id",         slot.task_id},
                    {"n_ctx",           n_ctx},
                    {"n_past",          slot.n_past},
                    {"n_system_tokens", system_tokens.size()},
                    {"n_cache_tokens",  slot.cache_tokens.size()},
                    {"truncated",       slot.truncated}
                });
                queue_tasks.notify_slot_changed();

                continue;
            }

            if (slot.state == IDLE)
            {
                continue;
            }

            slot.i_batch = batch.n_tokens;

            const int32_t slot_npast = slot.n_past_se > 0 ? slot.n_past_se : slot.n_past;

            // TODO: we always have to take into account the "system_tokens"
            //       this is not great and needs to be improved somehow
            common_batch_add(batch, slot.sampled, system_tokens.size() + slot_npast, { slot.id }, true);
            slot.n_past += 1;
        }

        // process in chunks of params.n_batch
        int32_t n_batch = params.n_batch;

        // assign workload to the slots
        if (params.cont_batching || batch.n_tokens == 0)
        {
            for (auto & slot : slots)
            {
                const bool has_prompt = slot.prompt.is_array() || (slot.prompt.is_string() && !slot.prompt.get<std::string>().empty()) || !slot.images.empty();

                // empty prompt passed -> release the slot and send empty response
                // note: infill mode allows empty prompt
                if (slot.state == IDLE && slot.command == LOAD_PROMPT && !has_prompt && !slot.infill)
                {
                    slot.release();
                    slot.print_timings();
                    send_final_response(slot);
                    continue;
                }

                // need process the prompt
                if (slot.state == IDLE && slot.command == LOAD_PROMPT)
                {
                    slot.state = PROCESSING;
                    slot.command = NONE;
                    std::vector<llama_token> prompt_tokens;
                    slot.t_start_process_prompt = ggml_time_us();
                    slot.t_start_genereration = 0;

                    if (slot.infill)
                    {
                        bool suff_rm_leading_spc = true;
                        if (params.input_suffix.find_first_of(' ') == 0 && params.input_suffix.size() > 1)
                        {
                            params.input_suffix.erase(0, 1);
                            suff_rm_leading_spc = false;
                        }
                        auto prefix_tokens = tokenize(slot.params.input_prefix, false);
                        auto suffix_tokens = tokenize(slot.params.input_suffix, false);

                        const int space_token = 29871; // TODO: this should not be hardcoded
                        if (suff_rm_leading_spc && !suffix_tokens.empty() && suffix_tokens[0] == space_token) {
                            suffix_tokens.erase(suffix_tokens.begin());
                        }

                        prefix_tokens.insert(prefix_tokens.begin(), llama_token_prefix(model));
                        prefix_tokens.insert(prefix_tokens.begin(), llama_token_bos(model)); // always add BOS
                        prefix_tokens.insert(prefix_tokens.end(),   llama_token_suffix(model));
                        prefix_tokens.insert(prefix_tokens.end(),   suffix_tokens.begin(), suffix_tokens.end());
                        prefix_tokens.push_back(llama_token_middle(model));
                        prompt_tokens = prefix_tokens;
                    }
                    else
                    {
                        prompt_tokens = tokenize(slot.prompt, system_prompt.empty() && add_bos_token);  // add BOS if there isn't system prompt
                    }

                    slot.num_prompt_tokens = prompt_tokens.size();

                    if (slot.params.n_keep < 0)
                    {
                        slot.params.n_keep = slot.num_prompt_tokens;
                    }
                    slot.params.n_keep = std::min(slot.n_ctx - 4, slot.params.n_keep);

                    // if input prompt is too big, truncate it
                    if (slot.num_prompt_tokens >= slot.n_ctx)
                    {
                        const int n_left = slot.n_ctx - slot.params.n_keep;
                        const int n_block_size = n_left / 2;
                        const int erased_blocks = (slot.num_prompt_tokens - slot.params.n_keep - n_block_size) / n_block_size;

                        std::vector<llama_token> new_tokens(prompt_tokens.begin(), prompt_tokens.begin() + slot.params.n_keep);
                        new_tokens.insert(new_tokens.end(), prompt_tokens.begin() + slot.params.n_keep + erased_blocks * n_block_size, prompt_tokens.end());

                        LOG_VERBOSE("input truncated", {
                            {"n_ctx",  slot.n_ctx},
                            {"n_keep", slot.params.n_keep},
                            {"n_left", n_left},
                            {"new_tokens", tokens_to_str(ctx, new_tokens.cbegin(), new_tokens.cend())},
                        });
                        slot.truncated = true;
                        prompt_tokens = new_tokens;

                        slot.num_prompt_tokens = prompt_tokens.size();
                        GGML_ASSERT(slot.num_prompt_tokens < slot.n_ctx);
                    }

                    if (!slot.params.cache_prompt)
                    {
                        common_sampler_reset(slot.ctx_sampling);

                        slot.n_past = 0;
                        slot.n_past_se = 0;
                        slot.ga_i = 0;
                        slot.num_prompt_tokens_processed = slot.num_prompt_tokens;
                    }
                    else
                    {
                        // push the prompt into the sampling context (do not apply grammar)
                        for (auto &token : prompt_tokens)
                        {
                            common_sampler_accept(slot.ctx_sampling, token, false);
                        }

                        slot.n_past = common_part(slot.cache_tokens, prompt_tokens);

                        // the last token of the cache is not in the KV cache until the next call to llama_decode
                        // (it was sampled, pushed into the "cache_tokens", but not yet put in the context)
                        if (slot.n_past > 0 && slot.n_past == (int32_t) slot.cache_tokens.size())
                        {
                            slot.n_past -= 1;
                        }

                        slot.num_prompt_tokens_processed = slot.num_prompt_tokens - slot.n_past;

                        if (slot.ga_n != 1)
                        {
                            int ga_i = 0;
                            int32_t ga_n = slot.ga_n;
                            int32_t ga_w = slot.ga_w;
                            int32_t slot_npast = 0;
                            for (int k = 0; k < slot.n_past; ++k)
                            {
                                while (slot_npast >= ga_i + ga_w) {
                                    const int bd = (ga_w/ga_n)*(ga_n - 1);
                                    slot_npast -= bd;
                                    ga_i += ga_w/ga_n;
                                }
                                slot_npast++;
                            }
                            slot.n_past_se = slot_npast;
                            slot.ga_i = ga_i;
                        }

                        LOG_INFO("slot progression", {
                            { "slot_id", slot.id },
                            { "task_id", slot.task_id },
                            { "n_past",  slot.n_past },
                            { "num_prompt_tokens_processed", slot.num_prompt_tokens_processed }
                        });
                    }

                    slot.cache_tokens = prompt_tokens;

                    if (slot.n_past == slot.num_prompt_tokens && slot.n_past > 0)
                    {
                        // we have to evaluate at least 1 token to generate logits.
                        LOG_INFO("we have to evaluate at least 1 token to generate logits", {
                            { "slot_id", slot.id },
                            { "task_id", slot.task_id }
                        });
                        slot.n_past--;
                        if (slot.ga_i > 0)
                        {
                            slot.n_past_se--;
                        }
                    }

                    int p0 = (int) system_tokens.size() + slot.n_past;
                    LOG_INFO("kv cache rm [p0, end)", {
                        { "slot_id", slot.id },
                        { "task_id", slot.task_id },
                        { "p0",      p0 }
                    });
                    llama_kv_cache_seq_rm(ctx, slot.id, p0, -1);

                    LOG_VERBOSE("prompt ingested", {
                                                    {"n_past",  slot.n_past},
                                                    {"cached",  tokens_to_str(ctx, slot.cache_tokens.cbegin(), slot.cache_tokens.cbegin() + slot.n_past)},
                                                    {"to_eval", tokens_to_str(ctx, slot.cache_tokens.cbegin() + slot.n_past, slot.cache_tokens.cend())},
                                                });

                    const bool has_images = process_images(slot);

                    // process the prefix of first image
                    std::vector<llama_token> prefix_tokens = has_images ? tokenize(slot.images[0].prefix_prompt, add_bos_token) : prompt_tokens;

                    int32_t slot_npast = slot.n_past_se > 0 ? slot.n_past_se : slot.n_past;

                    int32_t ga_i = slot.ga_i;
                    int32_t ga_n = slot.ga_n;
                    int32_t ga_w = slot.ga_w;

                    for (; slot.n_past < (int) prefix_tokens.size(); ++slot.n_past)
                    {
                        if (slot.ga_n != 1)
                        {
                            while (slot_npast >= ga_i + ga_w) {
                                const int bd = (ga_w/ga_n)*(ga_n - 1);
                                slot_npast -= bd;
                                ga_i += ga_w/ga_n;
                            }
                        }
                        common_batch_add(batch, prefix_tokens[slot.n_past], system_tokens.size() + slot_npast, {slot.id }, false);
                        slot_npast++;
                    }

                    if (has_images && !ingest_images(slot, n_batch))
                    {
                        LOG_ERR("%s: failed processing images Slot id : %d, Task id: %d", 
                            __func__,
                            slot.id,
                            slot.task_id
                        );
                        // FIXME @phymbert: to be properly tested
                        //  early returning without changing the slot state will block the slot for ever
                        // no one at the moment is checking the return value
                        return false;
                    }

                    // extract the logits only for the last token
                    if (batch.n_tokens > 0)
                    {
                        batch.logits[batch.n_tokens - 1] = true;
                    }

                    slot.n_decoded = 0;
                    slot.i_batch   = batch.n_tokens - 1;
                }
            }
        }

        if (batch.n_tokens == 0)
        {
            all_slots_are_idle = true;
            return true;
        }

        for (int32_t i = 0; i < (int32_t) batch.n_tokens; i += n_batch)
        {
            const int32_t n_tokens = std::min(n_batch, (int32_t) (batch.n_tokens - i));

            for (auto & slot : slots)
            {
                if (slot.ga_n != 1)
                {
                    // context extension via Self-Extend
                    while (slot.n_past_se >= slot.ga_i + slot.ga_w)
                    {
                        const int ib = (slot.ga_n * slot.ga_i) / slot.ga_w;
                        const int bd = (slot.ga_w / slot.ga_n) * (slot.ga_n - 1);
                        const int dd = (slot.ga_w / slot.ga_n) - ib * bd - slot.ga_w;

                        LOG("\n");
                        LOG("shift: [%6d, %6d] + %6d -> [%6d, %6d]\n", slot.ga_i, slot.n_past_se, ib * bd, slot.ga_i + ib * bd, slot.n_past_se + ib * bd);
                        LOG("div:   [%6d, %6d] / %6d -> [%6d, %6d]\n", slot.ga_i + ib * bd, slot.ga_i + ib * bd + slot.ga_w, slot.ga_n, (slot.ga_i + ib * bd) / slot.ga_n, (slot.ga_i + ib * bd + slot.ga_w) / slot.ga_n);
                        LOG("shift: [%6d, %6d] + %6d -> [%6d, %6d]\n", slot.ga_i + ib * bd + slot.ga_w, slot.n_past_se + ib * bd, dd, slot.ga_i + ib * bd + slot.ga_w + dd, slot.n_past_se + ib * bd + dd);

                        llama_kv_cache_seq_add(ctx, slot.id, slot.ga_i, slot.n_past_se, ib * bd);
                        llama_kv_cache_seq_div(ctx, slot.id, slot.ga_i + ib * bd, slot.ga_i + ib * bd + slot.ga_w,slot.ga_n);
                        llama_kv_cache_seq_add(ctx, slot.id, slot.ga_i + ib * bd + slot.ga_w,slot.n_past_se + ib * bd, dd);

                        slot.n_past_se -= bd;

                        slot.ga_i += slot.ga_w / slot.ga_n;

                        LOG("\nn_past_old = %d, n_past = %d, ga_i = %d\n\n", slot.n_past_se + bd, slot.n_past_se, slot.ga_i);
                    }
                    slot.n_past_se += n_tokens;
                }
            }

            llama_batch batch_view =
            {
                n_tokens,
                batch.token    + i,
                nullptr,
                batch.pos      + i,
                batch.n_seq_id + i,
                batch.seq_id   + i,
                batch.logits   + i,
            };

            const int ret = llama_decode(ctx, batch_view);

            if (ret != 0)
            {
                if (n_batch == 1 || ret < 0)
                {
                    // if you get here, it means the KV cache is full - try increasing it via the context size
                    LOG("%s : failed to decode the batch, n_batch = %d, ret = %d\n", __func__, n_batch, ret);
                    return false;
                }

                LOG("%s : failed to find free space in the KV cache, retrying with smaller n_batch = %d\n", __func__, n_batch / 2);

                // retry with half the batch size to try to find a free slot in the KV cache
                n_batch /= 2;
                i -= n_batch;
                continue;
            }

            for (auto & slot : slots)
            {
                if (slot.i_batch < (int) i || slot.i_batch >= (int) (i + n_tokens))
                {
                    continue;
                }

                // prompt evaluated for embedding
                if (slot.embedding)
                {
                    send_embedding(slot);
                    slot.release();
                    slot.i_batch = -1;
                    continue;
                }

                completion_token_output result;
                const llama_token id = common_sampler_sample(slot.ctx_sampling, ctx, slot.i_batch - i);

                common_sampler_accept(slot.ctx_sampling, id, true);

                slot.n_decoded += 1;
                if (slot.n_decoded == 1)
                {
                    slot.t_start_genereration = ggml_time_us();
                    slot.t_prompt_processing = (slot.t_start_genereration - slot.t_start_process_prompt) / 1e3;
                    metrics.on_prompt_eval(slot);
                }

                result.tok = id;
                const auto * cur_p = common_sampler_get_candidates(slot.ctx_sampling);

                for (size_t i = 0; i < (size_t) slot.sparams.n_probs; ++i) {
                    result.probs.push_back({
                        cur_p->data[i].id,
                        i >= cur_p->size ? 0.0f : cur_p->data[i].p,
                    });
                }

                if (!process_token(result, slot))
                {
                    slot.release();
                    slot.print_timings();
                    send_final_response(slot);
                    metrics.on_prediction(slot);
                }

                slot.i_batch = -1;
            }
        }

        LOG_VERBOSE("slots updated", {});
        return true;
    }

    void run_on_all_tasks_finished() {
        update_slots();
    }
};

/* llama.cpp completion api semantics */
static json format_partial_response(
    llama_server_context &llama, llama_client_slot *slot, const std::string &content, const std::vector<completion_token_output> &probs
) {
    json res = json
    {
        {"content",    content },
        {"stop",       false},
        {"slot_id",    slot->id },
        {"multimodal", llama.multimodal }
    };

    if (slot->sparams.n_probs > 0)
    {
        res["completion_probabilities"] = probs_vector_to_json(llama.ctx, probs);
    }

    return res;
}

struct token_translator
{
    llama_context * ctx;
    std::string operator()(llama_token tok)                    const { return common_token_to_piece(ctx, tok); }
    std::string operator()(const completion_token_output &cto) const { return (*this)(cto.tok); }
};

static void append_to_generated_text_from_generated_token_probs(llama_server_context &llama, llama_client_slot *slot)
{
    auto & gtps = slot->generated_token_probs;
    auto translator = token_translator{llama.ctx};
    auto add_strlen = [=](size_t sum, const completion_token_output & cto) { return sum + translator(cto).size(); };
    const size_t len = std::accumulate(gtps.begin(), gtps.end(), size_t(0), add_strlen);
    if (slot->generated_text.capacity() < slot->generated_text.size() + len)
    {
        slot->generated_text.reserve(slot->generated_text.size() + len);
    }
    for (const completion_token_output & cto : gtps)
    {
        slot->generated_text += translator(cto);
    }
}

std::function<void(int)> shutdown_handler;
inline void signal_handler(int signal) { shutdown_handler(signal); }

/////////////////////////////////
////////////////////////////////
//////// LOCALAI code starts below here
/////////////////////////////////
////////////////////////////////

bool loaded_model; // TODO: add a mutex for this, but happens only once loading the model

// The class has a llama instance that is shared across all RPCs
llama_server_context llama;

static void start_llama_server() {
    // Wait for model to be loaded first
    while (!loaded_model) {
        std::this_thread::sleep_for(std::chrono::milliseconds(100));
    }

    llama.queue_tasks.on_new_task(std::bind(
        &llama_server_context::process_single_task, &llama, std::placeholders::_1));
    llama.queue_tasks.on_finish_multitask(std::bind(
        &llama_server_context::on_finish_multitask, &llama, std::placeholders::_1));
    llama.queue_tasks.on_all_tasks_finished(std::bind(
        &llama_server_context::run_on_all_tasks_finished, &llama));
    llama.queue_results.on_multitask_update(std::bind(
        &llama_server_queue::update_multitask,
        &llama.queue_tasks,
        std::placeholders::_1,
        std::placeholders::_2,
        std::placeholders::_3
    ));
    llama.queue_tasks.start_loop();
}

json parse_options(bool streaming, const backend::PredictOptions* predict, llama_server_context &llama)
{
    
    // This is for example a slot data from the json data
    //     slot->params.stream           = json_value(data, "stream",            false);
    //     slot->params.cache_prompt     = json_value(data, "cache_prompt",      false);
    //     slot->params.n_predict        = json_value(data, "n_predict",         default_params.n_predict);
    //     slot->sparams.top_k           = json_value(data, "top_k",             default_sparams.top_k);
    //     slot->sparams.top_p           = json_value(data, "top_p",             default_sparams.top_p);
    //     slot->sparams.typical_p       = json_value(data, "typical_p",         default_sparams.typical_p);
    //     slot->sparams.temp            = json_value(data, "temperature",       default_sparams.temp);
    //     slot->sparams.penalty_last_n  = json_value(data, "repeat_last_n",     default_sparams.penalty_last_n);
    //     slot->sparams.penalty_repeat  = json_value(data, "repeat_penalty",    default_sparams.penalty_repeat);
    //     slot->sparams.penalty_freq    = json_value(data, "frequency_penalty", default_sparams.penalty_freq);
    //     slot->sparams.penalty_present = json_value(data, "presence_penalty",  default_sparams.penalty_present);
    //     slot->sparams.mirostat        = json_value(data, "mirostat",          default_sparams.mirostat);
    //     slot->sparams.mirostat_tau    = json_value(data, "mirostat_tau",      default_sparams.mirostat_tau);
    //     slot->sparams.mirostat_eta    = json_value(data, "mirostat_eta",      default_sparams.mirostat_eta);
    //     slot->params.n_keep           = json_value(data, "n_keep",            slot->params.n_keep);
    //     slot->params.seed             = json_value(data, "seed",              default_params.seed);
    //     slot->sparams.grammar         = json_value(data, "grammar",           default_sparams.grammar);
    //     slot->sparams.n_probs         = json_value(data, "n_probs",           default_sparams.n_probs);

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

    data["stop"] = predict->stopprompts();
    // data["n_probs"] = predict->nprobs();
    //TODO: images,

    return data;
}

// static void parse_options_completion(bool streaming,const backend::PredictOptions* predict, llama_server_context &llama)
// {
//     // https://github.com/ggerganov/llama.cpp/blob/d9b33fe95bd257b36c84ee5769cc048230067d6f/examples/server/server.cpp#L673
//     gpt_params default_params;

//     llama.stream = streaming;
//     llama.params.n_predict = predict->tokens() == 0 ? -1 : predict->tokens();
//     llama.params.sparams.top_k = predict->topk();
//     llama.params.sparams.top_p = predict->topp();
//     llama.params.sparams.typical_p = predict->typicalp();
//     llama.params.sparams.penalty_last_n = predict->repeat();
//     llama.params.sparams.temp = predict->temperature();
//     llama.params.sparams.penalty_repeat = predict->penalty();
//     llama.params.sparams.penalty_present = predict->presencepenalty();
//     llama.params.sparams.penalty_freq = predict->frequencypenalty();
//     llama.params.sparams.mirostat = predict->mirostat();
//     llama.params.sparams.mirostat_tau = predict->mirostattau();
//     llama.params.sparams.mirostat_eta = predict->mirostateta();
//     llama.params.n_keep = predict->nkeep();
//     llama.params.seed = predict->seed();
//     llama.params.sparams.grammar = predict->grammar();
//     // llama.params.n_probs = predict->
//     llama.params.prompt = predict->prompt();

//     llama.params.sparams.logit_bias.clear();

//     if (predict->ignoreeos())
//     {
//         llama.params.sparams.logit_bias[llama_token_eos(llama.model)] = -INFINITY;
//     }

//     // const auto &logit_bias = body.find("logit_bias");
//     // if (logit_bias != body.end() && logit_bias->is_array())
//     // {
//     //     const int n_vocab = llama_n_vocab(llama.model);
//     //     for (const auto &el : *logit_bias)
//     //     {
//     //         if (el.is_array() && el.size() == 2 && el[0].is_number_integer())
//     //         {
//     //             llama_token tok = el[0].get<llama_token>();
//     //             if (tok >= 0 && tok < n_vocab)
//     //             {
//     //                 if (el[1].is_number())
//     //                 {
//     //                     llama.params.logit_bias[tok] = el[1].get<float>();
//     //                 }
//     //                 else if (el[1].is_boolean() && !el[1].get<bool>())
//     //                 {
//     //                     llama.params.logit_bias[tok] = -INFINITY;
//     //                 }
//     //             }
//     //         }
//     //     }
//     // }

//     llama.params.antiprompt.clear();
//     for (const std::string& stopPrompt : predict->stopprompts()) {
//     if (!stopPrompt.empty())
//             {
//                 llama.params.antiprompt.push_back(stopPrompt);
//             }
//     }
// }

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

static void params_parse(const backend::ModelOptions* request,
                                common_params & params) {
   
    // this is comparable to: https://github.com/ggerganov/llama.cpp/blob/d9b33fe95bd257b36c84ee5769cc048230067d6f/examples/server/server.cpp#L1809

    params.model = request->modelfile();
    if (!request->mmproj().empty()) {
    // get the directory of modelfile
      std::string model_dir = params.model.substr(0, params.model.find_last_of("/\\"));
      params.mmproj = model_dir + "/"+ request->mmproj();
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
        params.rpc_servers = std::string(llama_grpc_servers);
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
     std::string model_dir = params.model.substr(0, params.model.find_last_of("/\\"));
     params.lora_adapters.push_back({ model_dir + "/"+request->loraadapter(), scale_factor });
    }
    params.use_mlock = request->mlock();
    params.use_mmap = request->mmap();
    params.flash_attn = request->flashattention();
    params.no_kv_offload = request->nokvoffload();
    params.ctx_shift = false; // We control context-shifting in any case (and we disable it as it could just lead to infinite loops)

    params.embedding = request->embeddings();

    if (request->ropescaling() == "none")   { params.rope_scaling_type = LLAMA_ROPE_SCALING_TYPE_NONE; }
    else if (request->ropescaling() == "yarn")   { params.rope_scaling_type = LLAMA_ROPE_SCALING_TYPE_YARN; }
    else { params.rope_scaling_type = LLAMA_ROPE_SCALING_TYPE_LINEAR; }
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
}


// GRPC Server start
class BackendServiceImpl final : public backend::Backend::Service {
public:
  grpc::Status Health(ServerContext* context, const backend::HealthMessage* request, backend::Reply* reply) {
    // Implement Health RPC
    reply->set_message("OK");
    return Status::OK;
  }

  grpc::Status LoadModel(ServerContext* context, const backend::ModelOptions* request, backend::Result* result) {
    // Implement LoadModel RPC
    common_params params;
    params_parse(request, params);

    llama_backend_init();
    llama_numa_init(params.numa);

    // load the model
    if (!llama.load_model(params))
    {
        result->set_message("Failed loading model");
        result->set_success(false);
        return Status::CANCELLED;
    }
    llama.initialize();
    result->set_message("Loading succeeded");
    result->set_success(true);
    loaded_model = true;
    return Status::OK;
  }
  grpc::Status PredictStream(grpc::ServerContext* context, const backend::PredictOptions* request, grpc::ServerWriter<backend::Reply>* writer) override {
        json data = parse_options(true, request, llama);
        const int task_id = llama.queue_tasks.get_new_id();
        llama.queue_results.add_waiting_task_id(task_id);
        llama.request_completion(task_id, data, false, false, -1);
        while (true)
        {
            task_result result = llama.queue_results.recv(task_id);
            if (!result.error) {
                const std::string str =
                "data: " +
                result.result_json.dump(-1, ' ', false, json::error_handler_t::replace) +
                "\n\n";
                LOG_VERBOSE("data stream", {
                    { "to_send", str }
                });

                backend::Reply reply;
                // print it
                std::string completion_text = result.result_json.value("content", "");

                reply.set_message(completion_text);
                int32_t tokens_predicted = result.result_json.value("tokens_predicted", 0);
                reply.set_tokens(tokens_predicted);
                int32_t tokens_evaluated = result.result_json.value("tokens_evaluated", 0);
                reply.set_prompt_tokens(tokens_evaluated);

                // Log Request Correlation Id
                LOG_VERBOSE("correlation:", {
                    { "id", data["correlation_id"] }
                });

                // Send the reply
                writer->Write(reply);

                if (result.stop) {
                    break;
                }
            } else {
                break;
            }
        }

        return grpc::Status::OK;
    }


    grpc::Status Predict(ServerContext* context, const backend::PredictOptions* request, backend::Reply* reply) {
        json data = parse_options(false, request, llama);
        const int task_id = llama.queue_tasks.get_new_id();
        llama.queue_results.add_waiting_task_id(task_id);
        llama.request_completion(task_id, data, false, false, -1);
        std::string completion_text;
        task_result result = llama.queue_results.recv(task_id);
        if (!result.error && result.stop) {
            
            // Log Request Correlation Id
            LOG_VERBOSE("correlation:", {
                { "id", data["correlation_id"] }
            });

            completion_text = result.result_json.value("content", "");
            int32_t tokens_predicted = result.result_json.value("tokens_predicted", 0);
            int32_t tokens_evaluated = result.result_json.value("tokens_evaluated", 0);
            reply->set_prompt_tokens(tokens_evaluated);
            reply->set_tokens(tokens_predicted);
            reply->set_message(completion_text);
        }
        else
        {
            return grpc::Status::OK;
        }

        return grpc::Status::OK;
    }

    /// https://github.com/ggerganov/llama.cpp/blob/aa2341298924ac89778252015efcb792f2df1e20/examples/server/server.cpp#L2969
    grpc::Status Embedding(ServerContext* context, const backend::PredictOptions* request, backend::EmbeddingResult* embeddingResult) {
        json data = parse_options(false, request, llama);
        const int task_id = llama.queue_tasks.get_new_id();
        llama.queue_results.add_waiting_task_id(task_id);
        llama.request_completion(task_id, { {"prompt", data["embeddings"]}, { "n_predict", 0}, {"image_data", ""} }, false, true, -1);
        // get the result
        task_result result = llama.queue_results.recv(task_id);
        //std::cout << "Embedding result JSON" << result.result_json.dump() << std::endl;
        llama.queue_results.remove_waiting_task_id(task_id);
        if (!result.error && result.stop) {
            std::vector<float> embeddings = result.result_json.value("embedding", std::vector<float>());
            // loop the vector and set the embeddings results
            for (int i = 0; i < embeddings.size(); i++) {
                embeddingResult->add_embeddings(embeddings[i]);
            }
        }
        else
        {
            return grpc::Status::OK;
        }

        return grpc::Status::OK;
    }

    grpc::Status GetMetrics(ServerContext* context, const backend::MetricsRequest* request, backend::MetricsResponse* response) {
        llama_client_slot* active_slot = llama.get_active_slot();

        if (active_slot != nullptr) {
            // Calculate the tokens per second using existing logic
            double tokens_per_second = 1e3 / active_slot->t_token_generation * active_slot->n_decoded;

            // Populate the response with metrics
            response->set_slot_id(active_slot->id);
            response->set_prompt_json_for_slot(active_slot->prompt.dump());
            response->set_tokens_per_second(tokens_per_second);
            response->set_tokens_generated(active_slot->n_decoded);
            response->set_prompt_tokens_processed(active_slot->num_prompt_tokens_processed);
        } else {
            // Handle case when no active slot exists
            response->set_slot_id(0);
            response->set_prompt_json_for_slot("");
            response->set_tokens_per_second(0);
            response->set_tokens_generated(0);
            response->set_prompt_tokens_processed(0);
        }

        return grpc::Status::OK;
    } 
};

void RunServer(const std::string& server_address) {
  BackendServiceImpl service;

  ServerBuilder builder;
  builder.AddListeningPort(server_address, grpc::InsecureServerCredentials());
  builder.RegisterService(&service);

  std::unique_ptr<Server> server(builder.BuildAndStart());
  std::cout << "Server listening on " << server_address << std::endl;
  server->Wait();
}

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

   // run the HTTP server in a thread - see comment below
    std::thread t([&]()
            {
                RunServer(server_address);
                return 0;
            });


    //);
    start_llama_server();
                        std::cout << "stopping" << std::endl;

    t.join();

    llama_backend_free();
  return 0;
}
