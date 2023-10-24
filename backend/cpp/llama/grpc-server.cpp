// llama.cpp gRPC C++ backend server
//
// Ettore Di Giacinto <mudler@localai.io>
//
// This is a gRPC server for llama.cpp compatible with the LocalAI proto
// Note: this is a re-adaptation of the original llama.cpp example/server.cpp for HTTP, 
// but modified to work with gRPC
//

#include <iostream>
#include <memory>
#include <string>
#include <getopt.h>

#include "common.h"
#include "llama.h"
#include "grammar-parser.h"
#include "backend.pb.h"
#include "backend.grpc.pb.h"

// include std::regex
#include <regex>
#include <grpcpp/ext/proto_server_reflection_plugin.h>
#include <grpcpp/grpcpp.h>
#include <grpcpp/health_check_service_interface.h>

using grpc::Server;
using grpc::ServerBuilder;
using grpc::ServerContext;
using grpc::Status;


using backend::HealthMessage;


// completion token output with probabilities
struct completion_token_output
{
    struct token_prob
    {
        llama_token tok;
        float prob;
    };

    std::vector<token_prob> probs;
    llama_token tok;
};

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


template <class Iter>
static std::string tokens_to_str(llama_context *ctx, Iter begin, Iter end)
{
    std::string ret;
    for (; begin != end; ++begin)
    {
        ret += llama_token_to_piece(ctx, *begin);
    }
    return ret;
}


// format incomplete utf-8 multibyte character for output
static std::string tokens_to_output_formatted_string(const llama_context *ctx, const llama_token token)
{
    std::string out = token == -1 ? "" : llama_token_to_piece(ctx, token);
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

struct llama_server_context
{
    bool stream = false;
    bool has_next_token = false;
    std::string generated_text;
    std::vector<completion_token_output> generated_token_probs;

    size_t num_prompt_tokens = 0;
    size_t num_tokens_predicted = 0;
    size_t n_past = 0;
    size_t n_remain = 0;

    std::vector<llama_token> embd;

    gpt_params params;

    llama_model *model = nullptr;
    llama_context *ctx = nullptr;
    llama_sampling_context *ctx_sampling = nullptr;

    int n_ctx;

    bool truncated = false;
    bool stopped_eos = false;
    bool stopped_word = false;
    bool stopped_limit = false;
    std::string stopping_word;
    int32_t multibyte_pending = 0;

    std::mutex mutex;

    std::unique_lock<std::mutex> lock()
    {
        return std::unique_lock<std::mutex>(mutex);
    }

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

    void rewind()
    {
        params.antiprompt.clear();
        params.sparams.grammar.clear();
        num_prompt_tokens = 0;
        num_tokens_predicted = 0;
        generated_text = "";
        generated_text.reserve(n_ctx);
        generated_token_probs.clear();
        truncated = false;
        stopped_eos = false;
        stopped_word = false;
        stopped_limit = false;
        stopping_word = "";
        multibyte_pending = 0;
        n_remain = 0;
        n_past = 0;
        params.sparams.n_prev = n_ctx;
    }

    void initSampling() {
        if (ctx_sampling != nullptr) {
            llama_sampling_free(ctx_sampling);
        }
        ctx_sampling = llama_sampling_init(params.sparams);
    }

    bool loadModel(const gpt_params &params_)
    {
        params = params_;
        std::tie(model, ctx) = llama_init_from_gpt_params(params);
        if (model == nullptr)
        {
            return false;
        }
        n_ctx = llama_n_ctx(ctx);
        return true;
    }
    std::vector<llama_token> tokenize_string(const char *prompt, bool add_bos) const {
       // If `add_bos` is true, we only add BOS, when json_prompt is a string,
        // or the first element of the json_prompt array is a string.
        std::vector<llama_token> prompt_tokens; 
        auto s = std::string(prompt);
        prompt_tokens = ::llama_tokenize(ctx, s, add_bos);
        return prompt_tokens;
    }
     std::vector<llama_token> tokenize_array(const char **prompts, bool add_bos) const {
                std::vector<llama_token> prompt_tokens; 

            bool first = true;
            bool is_string = true;
            for (const char **p = prompts; *p != nullptr; ++p)
              {
                if (is_string)
                {
                    auto s = std::string(*p);
                    std::vector<llama_token> p;
                    if (first)
                    {
                        p = ::llama_tokenize(ctx, s, add_bos);
                        first = false;
                    }
                    else
                    {
                        p = ::llama_tokenize(ctx, s, false);
                    }
                    prompt_tokens.insert(prompt_tokens.end(), p.begin(), p.end());
                }
                else
                {
                    if (first)
                    {
                        first = false;
                    }
                    //prompt_tokens.push_back(p.template get<llama_token>());
                }
            }
            return prompt_tokens;
     }

    void truncatePrompt(std::vector<llama_token> &prompt_tokens) {
        const int n_left = n_ctx - params.n_keep;
        const int n_block_size = n_left / 2;
        const int erased_blocks = (prompt_tokens.size() - params.n_keep - n_block_size) / n_block_size;

        // Keep n_keep tokens at start of prompt (at most n_ctx - 4)
        std::vector<llama_token> new_tokens(prompt_tokens.begin(), prompt_tokens.begin() + params.n_keep);

        new_tokens.insert(new_tokens.end(), prompt_tokens.begin() + params.n_keep + erased_blocks * n_block_size, prompt_tokens.end());

        truncated = true;
        prompt_tokens = new_tokens;
    }

    void loadInfill()
    {
        bool suff_rm_leading_spc = true;
        if (params.input_suffix.find_first_of(' ') == 0 && params.input_suffix.size() > 1) {
            params.input_suffix.erase(0, 1);
            suff_rm_leading_spc = false;
        }

        auto prefix_tokens = tokenize_string(params.input_prefix.c_str(), false);
        auto suffix_tokens = tokenize_string(params.input_suffix.c_str(), false);
        const int space_token = 29871;
        if (suff_rm_leading_spc  && suffix_tokens[0] == space_token) {
            suffix_tokens.erase(suffix_tokens.begin());
        }
        prefix_tokens.insert(prefix_tokens.begin(), llama_token_prefix(model));
        prefix_tokens.insert(prefix_tokens.begin(), llama_token_bos(model)); // always add BOS
        prefix_tokens.insert(prefix_tokens.end(), llama_token_suffix(model));
        prefix_tokens.insert(prefix_tokens.end(), suffix_tokens.begin(), suffix_tokens.end());
        prefix_tokens.push_back(llama_token_middle(model));

        auto prompt_tokens = prefix_tokens;

        num_prompt_tokens = prompt_tokens.size();

        if (params.n_keep < 0)
        {
            params.n_keep = (int)num_prompt_tokens;
        }
        params.n_keep = std::min(params.n_ctx - 4, params.n_keep);

        // if input prompt is too big, truncate like normal
        if (num_prompt_tokens >= (size_t) n_ctx)
        {
            truncatePrompt(prompt_tokens);
            num_prompt_tokens = prompt_tokens.size();

            GGML_ASSERT(num_prompt_tokens < (size_t)n_ctx);
        }

        // push the prompt into the sampling context (do not apply grammar)
        for (auto & token : prompt_tokens)
        {
            llama_sampling_accept(ctx_sampling, ctx, token, false);
        }

        // compare the evaluated prompt with the new prompt
        n_past = common_part(embd, prompt_tokens);
        embd = prompt_tokens;

        if (n_past == num_prompt_tokens)
        {
            // we have to evaluate at least 1 token to generate logits.
            printf("we have to evaluate at least 1 token to generate logits\n");
            n_past--;
        }

        // since #3228 we now have to manually manage the KV cache
        llama_kv_cache_seq_rm(ctx, 0, n_past, -1);

        has_next_token = true;
    }
    void loadPrompt(std::string prompt)
    {
        auto prompt_tokens = tokenize_string(prompt.c_str(), true);  // always add BOS

        num_prompt_tokens = prompt_tokens.size();

        if (params.n_keep < 0)
        {
            params.n_keep = (int)num_prompt_tokens;
        }
        params.n_keep = std::min(n_ctx - 4, params.n_keep);

        // if input prompt is too big, truncate like normal
        if (num_prompt_tokens >= (size_t) n_ctx)
        {
            truncatePrompt(prompt_tokens);
            num_prompt_tokens = prompt_tokens.size();

            GGML_ASSERT(num_prompt_tokens < (size_t)n_ctx);
        }

        // push the prompt into the sampling context (do not apply grammar)
        for (auto & token : prompt_tokens)
        {
            llama_sampling_accept(ctx_sampling, ctx, token, false);
        }

        // compare the evaluated prompt with the new prompt
        n_past = common_part(embd, prompt_tokens);

        embd = prompt_tokens;
        if (n_past == num_prompt_tokens)
        {
            // we have to evaluate at least 1 token to generate logits.
            n_past--;
        }

        // since #3228 we now have to manually manage the KV cache
        llama_kv_cache_seq_rm(ctx, 0, n_past, -1);

        has_next_token = true;
    }

    void beginCompletion()
    {
        // number of tokens to keep when resetting context
        n_remain = params.n_predict;
        llama_set_rng_seed(ctx, params.seed);
    }

    completion_token_output nextToken()
    {
        completion_token_output result;
        result.tok = -1;

        if (embd.size() >= (size_t)n_ctx)
        {
            // Shift context

            const int n_left    = n_past - params.n_keep - 1;
            const int n_discard = n_left/2;

            llama_kv_cache_seq_rm   (ctx, 0, params.n_keep + 1            , params.n_keep + n_discard + 1);
            llama_kv_cache_seq_shift(ctx, 0, params.n_keep + 1 + n_discard, n_past, -n_discard);

            for (size_t i = params.n_keep + 1 + n_discard; i < embd.size(); i++)
            {
                embd[i - n_discard] = embd[i];
            }
            embd.resize(embd.size() - n_discard);

            n_past -= n_discard;

            truncated = true;
        }

        bool tg = true;
        while (n_past < embd.size())
        {
            int n_eval = (int)embd.size() - n_past;
            tg = n_eval == 1;
            if (n_eval > params.n_batch)
            {
                n_eval = params.n_batch;
            }

            if (llama_decode(ctx, llama_batch_get_one(&embd[n_past], n_eval, n_past, 0)))
            {
                has_next_token = false;
                return result;
            }
            n_past += n_eval;
        }

        if (params.n_predict == 0)
        {
            has_next_token = false;
            result.tok = llama_token_eos(model);
            return result;
        }

        {
            // out of user input, sample next token
            result.tok = llama_sampling_sample(ctx_sampling, ctx, NULL);

            llama_token_data_array cur_p = { ctx_sampling->cur.data(), ctx_sampling->cur.size(), false };

            const int32_t n_probs = params.sparams.n_probs;
            if (params.sparams.temp <= 0 && n_probs > 0)
            {
                // For llama_sample_token_greedy we need to sort candidates
                llama_sample_softmax(ctx, &cur_p);
            }

            for (size_t i = 0; i < std::min(cur_p.size, (size_t)n_probs); ++i)
            {
                result.probs.push_back({cur_p.data[i].id, cur_p.data[i].p});
            }

            llama_sampling_accept(ctx_sampling, ctx, result.tok, true);

            if (tg) {
                num_tokens_predicted++;
            }
        }

        // add it to the context
        embd.push_back(result.tok);
        // decrement remaining sampling budget
        --n_remain;

        if (!embd.empty() && embd.back() == llama_token_eos(model))
        {
            // stopping_word = llama_token_to_piece(ctx, embd.back());
            has_next_token = false;
            stopped_eos = true;
            return result;
        }

        has_next_token = params.n_predict == -1 || n_remain != 0;
        return result;
    }

    size_t findStoppingStrings(const std::string &text, const size_t last_token_size,
                               const stop_type type)
    {
        size_t stop_pos = std::string::npos;
        for (const std::string &word : params.antiprompt)
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
                    stopping_word = word;
                    stopped_word = true;
                    has_next_token = false;
                }
                stop_pos = pos;
            }
        }
        return stop_pos;
    }

    completion_token_output doCompletion()
    {
        auto token_with_probs = nextToken();

        const std::string token_text = token_with_probs.tok == -1 ? "" : llama_token_to_piece(ctx, token_with_probs.tok);
        generated_text += token_text;

        if (params.sparams.n_probs > 0)
        {
            generated_token_probs.push_back(token_with_probs);
        }

        if (multibyte_pending > 0)
        {
            multibyte_pending -= token_text.size();
        }
        else if (token_text.size() == 1)
        {
            const char c = token_text[0];
            // 2-byte characters: 110xxxxx 10xxxxxx
            if ((c & 0xE0) == 0xC0)
            {
                multibyte_pending = 1;
                // 3-byte characters: 1110xxxx 10xxxxxx 10xxxxxx
            }
            else if ((c & 0xF0) == 0xE0)
            {
                multibyte_pending = 2;
                // 4-byte characters: 11110xxx 10xxxxxx 10xxxxxx 10xxxxxx
            }
            else if ((c & 0xF8) == 0xF0)
            {
                multibyte_pending = 3;
            }
            else
            {
                multibyte_pending = 0;
            }
        }

        if (multibyte_pending > 0 && !has_next_token)
        {
            has_next_token = true;
            n_remain++;
        }

        if (!has_next_token && n_remain == 0)
        {
            stopped_limit = true;
        }

        return token_with_probs;
    }

    std::vector<float> getEmbedding()
    {
        static const int n_embd = llama_n_embd(model);
        if (!params.embedding)
        {
            return std::vector<float>(n_embd, 0.0f);
        }
        const float *data = llama_get_embeddings(ctx);
        std::vector<float> embedding(data, data + n_embd);
        return embedding;
    }
};


static void parse_options_completion(bool streaming,const backend::PredictOptions* predict, llama_server_context &llama)
{
    gpt_params default_params;

    llama.stream = streaming;
    llama.params.n_predict = predict->tokens() == 0 ? -1 : predict->tokens();
    llama.params.sparams.top_k = predict->topk();
    llama.params.sparams.top_p = predict->topp();
    llama.params.sparams.tfs_z = predict->tailfreesamplingz();
    llama.params.sparams.typical_p = predict->typicalp();
    llama.params.sparams.penalty_last_n = predict->repeat();
    llama.params.sparams.temp = predict->temperature();
    llama.params.sparams.penalty_repeat = predict->penalty();
    llama.params.sparams.penalty_present = predict->presencepenalty();
    llama.params.sparams.penalty_freq = predict->frequencypenalty();
    llama.params.sparams.mirostat = predict->mirostat();
    llama.params.sparams.mirostat_tau = predict->mirostattau();
    llama.params.sparams.mirostat_eta = predict->mirostateta();
    llama.params.sparams.penalize_nl = predict->penalizenl();
    llama.params.n_keep = predict->nkeep();
    llama.params.seed = predict->seed();
    llama.params.sparams.grammar = predict->grammar();
    // llama.params.n_probs = predict->
    llama.params.prompt = predict->prompt();

    llama.params.sparams.logit_bias.clear();

    if (predict->ignoreeos())
    {
        llama.params.sparams.logit_bias[llama_token_eos(llama.model)] = -INFINITY;
    }

    // const auto &logit_bias = body.find("logit_bias");
    // if (logit_bias != body.end() && logit_bias->is_array())
    // {
    //     const int n_vocab = llama_n_vocab(llama.model);
    //     for (const auto &el : *logit_bias)
    //     {
    //         if (el.is_array() && el.size() == 2 && el[0].is_number_integer())
    //         {
    //             llama_token tok = el[0].get<llama_token>();
    //             if (tok >= 0 && tok < n_vocab)
    //             {
    //                 if (el[1].is_number())
    //                 {
    //                     llama.params.logit_bias[tok] = el[1].get<float>();
    //                 }
    //                 else if (el[1].is_boolean() && !el[1].get<bool>())
    //                 {
    //                     llama.params.logit_bias[tok] = -INFINITY;
    //                 }
    //             }
    //         }
    //     }
    // }

    llama.params.antiprompt.clear();
    for (const std::string& stopPrompt : predict->stopprompts()) {
    if (!stopPrompt.empty())
            {
                llama.params.antiprompt.push_back(stopPrompt);
            }
    }
}



static void params_parse(const backend::ModelOptions* request,
                                gpt_params & params) {
   
    params.model = request->modelfile();
    //  params.model_alias ??
    params.model_alias =  request->modelfile();
    params.n_ctx = request->contextsize();
    params.memory_f16 = request->f16memory();
    params.n_threads = request->threads();
    params.n_gpu_layers = request->ngpulayers();
    params.n_batch = request->nbatch();

    if (!request->tensorsplit().empty()) {
        std::string arg_next = request->tensorsplit();

        // split string by , and /
        const std::regex regex{ R"([,/]+)" };
        std::sregex_token_iterator it{ arg_next.begin(), arg_next.end(), regex, -1 };
        std::vector<std::string> split_arg{ it, {} };

        GGML_ASSERT(split_arg.size() <= LLAMA_MAX_DEVICES);

        for (size_t i_device = 0; i_device < LLAMA_MAX_DEVICES; ++i_device) {
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
    // TODO: lora needs also a scale factor
    //params.lora_adapter = request->loraadapter();
    //params.lora_base = request->lorabase();
    params.use_mlock = request->mlock();
    params.use_mmap = request->mmap();
    params.embedding = request->embeddings();
}

static bool is_at_eob(llama_server_context &server_context, const llama_token *tokens, const size_t n_tokens) {
    return n_tokens && tokens[n_tokens-1] == llama_token_eos(server_context.model);
}

// Function matching type llama_beam_search_callback_fn_t.
// Custom callback example is called each time the beams lengths increase:
//  * Show progress by printing ',' following by number of convergent beam tokens if any.
//  * When all beams converge to a common prefix, they are made available in beams_state.beams[0].
//    This is also called when the stop condition is met.
//    Collect tokens into std::vector<llama_token> response which is pointed to by callback_data.
static void beam_search_callback(void *callback_data, llama_beams_state beams_state) {
    auto & llama = *static_cast<llama_server_context*>(callback_data);
    // Mark beams as EOS as needed.
    for (size_t i = 0 ; i < beams_state.n_beams ; ++i) {
        llama_beam_view& beam_view = beams_state.beam_views[i];
        if (!beam_view.eob && is_at_eob(llama, beam_view.tokens, beam_view.n_tokens)) {
            beam_view.eob = true;
        }
    }
    printf(",");  // Show progress
    if (const size_t n = beams_state.common_prefix_length) {
        llama.generated_token_probs.resize(llama.generated_token_probs.size() + n);
        assert(0u < beams_state.n_beams);
        const llama_token * tokens = beams_state.beam_views[0].tokens;
        const auto map = [](llama_token tok) { return completion_token_output{{},tok}; };
        std::transform(tokens, tokens + n, llama.generated_token_probs.end() - n, map);
        printf("%zu", n);
    }
    fflush(stdout);
#if 0 // DEBUG: print current beams for this iteration
    std::cout << "\n\nCurrent beams:\n";
    for (size_t i=0 ; i < beams_state.n_beams ; ++i) {
        std::cout << "beams["<<i<<"]: " << ostream_beam_view{state.ctx,beams_state.beam_views[i]} << std::endl;
    }
#endif
}
struct token_translator {
    llama_context * ctx;
    std::string operator()(llama_token tok) const { return llama_token_to_piece(ctx, tok); }
    std::string operator()(const completion_token_output & cto) const { return (*this)(cto.tok); }
};


static void append_to_generated_text_from_generated_token_probs(llama_server_context &llama)
{
    auto & gtps = llama.generated_token_probs;
    auto translator = token_translator{llama.ctx};
    auto add_strlen = [=](size_t sum, const completion_token_output & cto) { return sum + translator(cto).size(); };
    const size_t len = std::accumulate(gtps.begin(), gtps.end(), size_t(0), add_strlen);
    if (llama.generated_text.capacity() < llama.generated_text.size() + len) {
        llama.generated_text.reserve(llama.generated_text.size() + len);
    }
    for (const completion_token_output & cto : gtps) {
        llama.generated_text += translator(cto);
    }
}

// GRPC Server start
class BackendServiceImpl final : public backend::Backend::Service {
  // The class has a llama instance that is shared across all RPCs
  llama_server_context llama;
public:
  grpc::Status Health(ServerContext* context, const backend::HealthMessage* request, backend::Reply* reply) {
    // Implement Health RPC
    reply->set_message("OK");
    return Status::OK;
  }

  grpc::Status LoadModel(ServerContext* context, const backend::ModelOptions* request, backend::Result* result) {
    // Implement LoadModel RPC
    gpt_params params;
    params_parse(request, params);

    llama_backend_init(params.numa);

    // load the model
    if (!llama.loadModel(params))
    {
        result->set_message("Failed loading model");
        result->set_success(false);
        return Status::CANCELLED;
    }
    result->set_message("Loading succeeded");
    result->set_success(true);
    return Status::OK;
  }
  grpc::Status PredictStream(grpc::ServerContext* context, const backend::PredictOptions* request, grpc::ServerWriter<backend::Reply>* writer) override {
        // Implement the streaming logic here based on the request options
        // You can use writer->Write(response) to send a reply to the client
        // and return grpc::Status::OK when the operation is complete.
        auto lock = llama.lock();

        llama.rewind();

        llama_reset_timings(llama.ctx);

        parse_options_completion(false, request, llama);

        llama.initSampling();
        llama.loadPrompt(request->prompt());
        llama.beginCompletion();
        size_t sent_count = 0;
        size_t sent_token_probs_index = 0;

        while (llama.has_next_token) {
            const completion_token_output token_with_probs = llama.doCompletion();
            if (token_with_probs.tok == -1 || llama.multibyte_pending > 0) {
                continue;
            }
            const std::string token_text = llama_token_to_piece(llama.ctx, token_with_probs.tok);

            size_t pos = std::min(sent_count, llama.generated_text.size());

            const std::string str_test = llama.generated_text.substr(pos);
            bool is_stop_full = false;
            size_t stop_pos =
                llama.findStoppingStrings(str_test, token_text.size(), STOP_FULL);
            if (stop_pos != std::string::npos) {
                is_stop_full = true;
                llama.generated_text.erase(
                    llama.generated_text.begin() + pos + stop_pos,
                    llama.generated_text.end());
                pos = std::min(sent_count, llama.generated_text.size());
            } else {
                is_stop_full = false;
                stop_pos = llama.findStoppingStrings(str_test, token_text.size(),
                    STOP_PARTIAL);
            }

            if (
                stop_pos == std::string::npos ||
                // Send rest of the text if we are at the end of the generation
                (!llama.has_next_token && !is_stop_full && stop_pos > 0)
            ) {
                const std::string to_send = llama.generated_text.substr(pos, std::string::npos);

                sent_count += to_send.size();

                std::vector<completion_token_output> probs_output = {};

                if (llama.params.sparams.n_probs > 0) {
                    const std::vector<llama_token> to_send_toks = llama_tokenize(llama.ctx, to_send, false);
                    size_t probs_pos = std::min(sent_token_probs_index, llama.generated_token_probs.size());
                    size_t probs_stop_pos = std::min(sent_token_probs_index + to_send_toks.size(), llama.generated_token_probs.size());
                    if (probs_pos < probs_stop_pos) {
                        probs_output = std::vector<completion_token_output>(llama.generated_token_probs.begin() + probs_pos, llama.generated_token_probs.begin() + probs_stop_pos);
                    }
                    sent_token_probs_index = probs_stop_pos;
                }
                backend::Reply reply;
                reply.set_message(to_send);

                // Send the reply
                writer->Write(reply);
            }
        }

        llama_print_timings(llama.ctx);

        llama.mutex.unlock();
        lock.release();
        return grpc::Status::OK;
    }


    grpc::Status Predict(ServerContext* context, const backend::PredictOptions* request, backend::Reply* reply) {
        auto lock = llama.lock();
        llama.rewind();
        llama_reset_timings(llama.ctx);
        parse_options_completion(false, request, llama);

        llama.initSampling();
        llama.loadPrompt(request->prompt());
        llama.beginCompletion();

        if (llama.params.n_beams) {
            // Fill llama.generated_token_probs vector with final beam.
            llama_beam_search(llama.ctx, beam_search_callback, &llama, llama.params.n_beams,
                                llama.n_past, llama.n_remain);
            // Translate llama.generated_token_probs to llama.generated_text.
            append_to_generated_text_from_generated_token_probs(llama);
        } else {
            size_t stop_pos = std::string::npos;

            while (llama.has_next_token) {
                const completion_token_output token_with_probs = llama.doCompletion();
                const std::string token_text = token_with_probs.tok == -1 ? "" : llama_token_to_piece(llama.ctx, token_with_probs.tok);

                stop_pos = llama.findStoppingStrings(llama.generated_text,
                    token_text.size(), STOP_FULL);
            }

            if (stop_pos == std::string::npos) {
                stop_pos = llama.findStoppingStrings(llama.generated_text, 0, STOP_PARTIAL);
            }
            if (stop_pos != std::string::npos) {
                llama.generated_text.erase(llama.generated_text.begin() + stop_pos,
                    llama.generated_text.end());
            }
        }

        auto probs = llama.generated_token_probs;
        if (llama.params.sparams.n_probs > 0 && llama.stopped_word) {
            const std::vector<llama_token> stop_word_toks = llama_tokenize(llama.ctx, llama.stopping_word, false);
            probs = std::vector<completion_token_output>(llama.generated_token_probs.begin(), llama.generated_token_probs.end() - stop_word_toks.size());
        }
        reply->set_message(llama.generated_text);
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

  RunServer(server_address);
  return 0;
}
