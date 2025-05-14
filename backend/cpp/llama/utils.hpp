// https://github.com/ggerganov/llama.cpp/blob/master/tools/server/utils.hpp

#pragma once

#include <string>
#include <vector>
#include <set>
#include <mutex>
#include <condition_variable>
#include <unordered_map>

#include "json.hpp"

#include "../mtmd/clip.h"

using json = nlohmann::json;

extern bool server_verbose;

#ifndef SERVER_VERBOSE
#define SERVER_VERBOSE 1
#endif

#if SERVER_VERBOSE != 1
#define LOG_VERBOSE(MSG, ...)
#else
#define LOG_VERBOSE(MSG, ...)                                            \
    do                                                                   \
    {                                                                    \
        if (server_verbose)                                              \
        {                                                                \
            server_log("VERBOSE", __func__, __LINE__, MSG, __VA_ARGS__); \
        }                                                                \
    } while (0)
#endif

#define LOG_ERROR(  MSG, ...) server_log("ERROR",   __func__, __LINE__, MSG, __VA_ARGS__)
#define LOG_WARNING(MSG, ...) server_log("WARNING", __func__, __LINE__, MSG, __VA_ARGS__)
#define LOG_INFO(   MSG, ...) server_log("INFO",    __func__, __LINE__, MSG, __VA_ARGS__)

//
// parallel
//

enum server_state {
    SERVER_STATE_LOADING_MODEL,  // Server is starting up, model not fully loaded yet
    SERVER_STATE_READY,          // Server is ready and model is loaded
    SERVER_STATE_ERROR           // An error occurred, load_model failed
};

enum task_type {
    TASK_TYPE_COMPLETION,
    TASK_TYPE_CANCEL,
    TASK_TYPE_NEXT_RESPONSE
};

struct task_server {
    int id = -1; // to be filled by llama_server_queue
    int target_id;
    task_type type;
    json data;
    bool infill_mode = false;
    bool embedding_mode = false;
    int multitask_id = -1;
};

struct task_result {
    int id;
    int multitask_id = -1;
    bool stop;
    bool error;
    json result_json;
};

struct task_multi {
    int id;
    std::set<int> subtasks_remaining{};
    std::vector<task_result> results{};
};

// TODO: can become bool if we can't find use of more states
enum slot_state
{
    IDLE,
    PROCESSING,
};

enum slot_command
{
    NONE,
    LOAD_PROMPT,
    RELEASE,
};

struct slot_params
{
    bool stream       = true;
    bool cache_prompt = false; // remember the prompt to avoid reprocessing all prompt

    uint32_t seed      = -1; // RNG seed
    int32_t  n_keep    =  0; // number of tokens to keep from initial prompt
    int32_t  n_predict = -1; // new tokens to predict

    std::vector<std::string> antiprompt;

    json input_prefix;
    json input_suffix;
};

struct slot_image
{
    int32_t id;

    bool request_encode_image = false;
    float * image_embedding = nullptr;
    int32_t image_tokens = 0;

    clip_image_u8 * img_data;

    std::string prefix_prompt; // before of this image
};

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
    std::string text_to_send;
};

static inline void server_log(const char *level, const char *function, int line,
                       const char *message, const nlohmann::ordered_json &extra)
{
    nlohmann::ordered_json log
    {
        {"timestamp", time(nullptr)},
        {"level",     level},
        {"function",  function},
        {"line",      line},
        {"message",   message},
    };

    if (!extra.empty())
    {
        log.merge_patch(extra);
    }

    const std::string str = log.dump(-1, ' ', false, json::error_handler_t::replace);
    printf("%.*s\n", (int)str.size(), str.data());
    fflush(stdout);
}

//
// server utils
//

template <typename T>
static T json_value(const json &body, const std::string &key, const T &default_value)
{
    // Fallback null to default value
    return body.contains(key) && !body.at(key).is_null()
        ? body.value(key, default_value)
        : default_value;
}

inline std::string format_chatml(std::vector<json> messages)
{
    std::ostringstream chatml_msgs;

    for (auto it = messages.begin(); it != messages.end(); ++it) {
        chatml_msgs << "<|im_start|>"
                    << json_value(*it, "role",    std::string("user")) << '\n';
        chatml_msgs << json_value(*it, "content", std::string(""))
                    << "<|im_end|>\n";
    }

    chatml_msgs << "<|im_start|>assistant" << '\n';

    return chatml_msgs.str();
}

//
// work queue utils
//

struct llama_server_queue {
    int id = 0;
    std::mutex mutex_tasks;
    // queues
    std::vector<task_server> queue_tasks;
    std::vector<task_server> queue_tasks_deferred;
    std::vector<task_multi> queue_multitasks;
    std::condition_variable condition_tasks;
    // callback functions
    std::function<void(task_server&)> callback_new_task;
    std::function<void(task_multi&)> callback_finish_multitask;
    std::function<void(void)> callback_all_task_finished;

    // Add a new task to the end of the queue
    int post(task_server task) {
        std::unique_lock<std::mutex> lock(mutex_tasks);
        if (task.id == -1) {
            task.id = id++;
        }
        queue_tasks.push_back(std::move(task));
        condition_tasks.notify_one();
        return task.id;
    }

    // Add a new task, but defer until one slot is available
    void defer(task_server task) {
        std::unique_lock<std::mutex> lock(mutex_tasks);
        queue_tasks_deferred.push_back(std::move(task));
    }

    // Get the next id for creating anew task
    int get_new_id() {
        std::unique_lock<std::mutex> lock(mutex_tasks);
        return id++;
    }

    // Register function to process a new task
    void on_new_task(std::function<void(task_server&)> callback) {
        callback_new_task = callback;
    }

    // Register function to process a multitask
    void on_finish_multitask(std::function<void(task_multi&)> callback) {
        callback_finish_multitask = callback;
    }

    // Register the function to be called when the batch of tasks is finished
    void on_all_tasks_finished(std::function<void(void)> callback) {
        callback_all_task_finished = callback;
    }

    // Call when the state of one slot is changed
    void notify_slot_changed() {
        // move deferred tasks back to main loop
        std::unique_lock<std::mutex> lock(mutex_tasks);
        for (auto & task : queue_tasks_deferred) {
            queue_tasks.push_back(std::move(task));
        }
        queue_tasks_deferred.clear();
    }

    // Start the main loop. This call is blocking
    [[noreturn]]
    void start_loop() {
        while (true) {
            // new task arrived
            LOG_VERBOSE("have new task", {});
            {
                while (true)
                {
                    std::unique_lock<std::mutex> lock(mutex_tasks);
                    if (queue_tasks.empty()) {
                        lock.unlock();
                        break;
                    }
                    task_server task = queue_tasks.front();
                    queue_tasks.erase(queue_tasks.begin());
                    lock.unlock();
                    LOG_VERBOSE("callback_new_task", {});
                    callback_new_task(task);
                }
                LOG_VERBOSE("callback_all_task_finished", {});
                // process and update all the multitasks
                auto queue_iterator = queue_multitasks.begin();
                while (queue_iterator != queue_multitasks.end())
                {
                    if (queue_iterator->subtasks_remaining.empty())
                    {
                        // all subtasks done == multitask is done
                        task_multi current_multitask = *queue_iterator;
                        callback_finish_multitask(current_multitask);
                        // remove this multitask
                        queue_iterator = queue_multitasks.erase(queue_iterator);
                    }
                    else
                    {
                        ++queue_iterator;
                    }
                }
                // all tasks in the current loop is finished
                callback_all_task_finished();
            }
            LOG_VERBOSE("wait for new task", {});
            // wait for new task
            {
                std::unique_lock<std::mutex> lock(mutex_tasks);
                if (queue_tasks.empty()) {
                    condition_tasks.wait(lock, [&]{
                        return !queue_tasks.empty();
                    });
                }
            }
        }
    }

    //
    // functions to manage multitasks
    //

    // add a multitask by specifying the id of all subtask (subtask is a task_server)
    void add_multitask(int multitask_id, std::vector<int>& sub_ids)
    {
        std::lock_guard<std::mutex> lock(mutex_tasks);
        task_multi multi;
        multi.id = multitask_id;
        std::copy(sub_ids.begin(), sub_ids.end(), std::inserter(multi.subtasks_remaining, multi.subtasks_remaining.end()));
        queue_multitasks.push_back(multi);
    }

    // updatethe remaining subtasks, while appending results to multitask
    void update_multitask(int multitask_id, int subtask_id, task_result& result)
    {
        std::lock_guard<std::mutex> lock(mutex_tasks);
        for (auto& multitask : queue_multitasks)
        {
            if (multitask.id == multitask_id)
            {
                multitask.subtasks_remaining.erase(subtask_id);
                multitask.results.push_back(result);
            }
        }
    }
};

struct llama_server_response {
    typedef std::function<void(int, int, task_result&)> callback_multitask_t;
    callback_multitask_t callback_update_multitask;
    // for keeping track of all tasks waiting for the result
    std::set<int> waiting_task_ids;
    // the main result queue
    std::vector<task_result> queue_results;
    std::mutex mutex_results;
    std::condition_variable condition_results;

    void add_waiting_task_id(int task_id) {
        std::unique_lock<std::mutex> lock(mutex_results);
        waiting_task_ids.insert(task_id);
    }

    void remove_waiting_task_id(int task_id) {
        std::unique_lock<std::mutex> lock(mutex_results);
        waiting_task_ids.erase(task_id);
    }

    // This function blocks the thread until there is a response for this task_id
    task_result recv(int task_id) {
        while (true)
        {
            std::unique_lock<std::mutex> lock(mutex_results);
            condition_results.wait(lock, [&]{
                return !queue_results.empty();
            });
            LOG_VERBOSE("condition_results unblock", {});

            for (int i = 0; i < (int) queue_results.size(); i++)
            {
                if (queue_results[i].id == task_id)
                {
                    assert(queue_results[i].multitask_id == -1);
                    task_result res = queue_results[i];
                    queue_results.erase(queue_results.begin() + i);
                    return res;
                }
            }
        }

        // should never reach here
    }

    // Register the function to update multitask
    void on_multitask_update(callback_multitask_t callback) {
        callback_update_multitask = callback;
    }

    // Send a new result to a waiting task_id
    void send(task_result result) {
        std::unique_lock<std::mutex> lock(mutex_results);
        LOG_VERBOSE("send new result", {});
        for (auto& task_id : waiting_task_ids) {
            // LOG_TEE("waiting task id %i \n", task_id);
            // for now, tasks that have associated parent multitasks just get erased once multitask picks up the result
            if (result.multitask_id == task_id)
            {
                LOG_VERBOSE("callback_update_multitask", {});
                callback_update_multitask(task_id, result.id, result);
                continue;
            }

            if (result.id == task_id)
            {
                LOG_VERBOSE("queue_results.push_back", {});
                queue_results.push_back(result);
                condition_results.notify_one();
                return;
            }
        }
    }
};

//
// base64 utils (TODO: move to common in the future)
//

static const std::string base64_chars =
             "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
             "abcdefghijklmnopqrstuvwxyz"
             "0123456789+/";

static inline bool is_base64(uint8_t c)
{
    return (isalnum(c) || (c == '+') || (c == '/'));
}

static inline std::vector<uint8_t> base64_decode(const std::string & encoded_string)
{
    int i = 0;
    int j = 0;
    int in_ = 0;

    int in_len = encoded_string.size();

    uint8_t char_array_4[4];
    uint8_t char_array_3[3];

    std::vector<uint8_t> ret;

    while (in_len-- && (encoded_string[in_] != '=') && is_base64(encoded_string[in_]))
    {
        char_array_4[i++] = encoded_string[in_]; in_++;
        if (i == 4)
        {
            for (i = 0; i <4; i++)
            {
                char_array_4[i] = base64_chars.find(char_array_4[i]);
            }

            char_array_3[0] = ((char_array_4[0]      ) << 2) + ((char_array_4[1] & 0x30) >> 4);
            char_array_3[1] = ((char_array_4[1] & 0xf) << 4) + ((char_array_4[2] & 0x3c) >> 2);
            char_array_3[2] = ((char_array_4[2] & 0x3) << 6) +   char_array_4[3];

            for (i = 0; (i < 3); i++)
            {
                ret.push_back(char_array_3[i]);
            }
            i = 0;
        }
    }

    if (i)
    {
        for (j = i; j <4; j++)
        {
            char_array_4[j] = 0;
        }

        for (j = 0; j <4; j++)
        {
            char_array_4[j] = base64_chars.find(char_array_4[j]);
        }

        char_array_3[0] = ((char_array_4[0]      ) << 2) + ((char_array_4[1] & 0x30) >> 4);
        char_array_3[1] = ((char_array_4[1] & 0xf) << 4) + ((char_array_4[2] & 0x3c) >> 2);
        char_array_3[2] = ((char_array_4[2] & 0x3) << 6) +   char_array_4[3];

        for (j = 0; (j < i - 1); j++)
        {
            ret.push_back(char_array_3[j]);
        }
    }

    return ret;

}



//
// tokenizer and input processing utils
//

static bool json_is_array_of_numbers(const json & data) {
    if (data.is_array()) {
        for (const auto & e : data) {
            if (!e.is_number_integer()) {
                return false;
            }
        }
        return true;
    }
    return false;
}

// is array having BOTH numbers & strings?
static bool json_is_array_of_mixed_numbers_strings(const json & data) {
    bool seen_string = false;
    bool seen_number = false;
    if (data.is_array()) {
        for (const auto & e : data) {
            seen_string |= e.is_string();
            seen_number |= e.is_number_integer();
            if (seen_number && seen_string) {
                return true;
            }
        }
    }
    return false;
}

// get value by path(key1 / key2)
static json json_get_nested_values(const std::vector<std::string> & paths, const json & js) {
    json result = json::object();

    for (const std::string & path : paths) {
        json current = js;
        const auto keys = string_split<std::string>(path, /*separator*/ '/');
        bool valid_path = true;
        for (const std::string & k : keys) {
            if (valid_path && current.is_object() && current.contains(k)) {
                current = current[k];
            } else {
                valid_path = false;
            }
        }
        if (valid_path) {
            result[path] = current;
        }
    }
    return result;
}


/**
 * this handles 2 cases:
 * - only string, example: "string"
 * - mixed string and tokens, example: [12, 34, "string", 56, 78]
 */
static llama_tokens tokenize_mixed(const llama_vocab * vocab, const json & json_prompt, bool add_special, bool parse_special) {
    // If `add_bos` is true, we only add BOS, when json_prompt is a string,
    // or the first element of the json_prompt array is a string.
    llama_tokens prompt_tokens;

    if (json_prompt.is_array()) {
        bool first = true;
        for (const auto & p : json_prompt) {
            if (p.is_string()) {
                auto s = p.template get<std::string>();

                llama_tokens p;
                if (first) {
                    p = common_tokenize(vocab, s, add_special, parse_special);
                    first = false;
                } else {
                    p = common_tokenize(vocab, s, false, parse_special);
                }

                prompt_tokens.insert(prompt_tokens.end(), p.begin(), p.end());
            } else {
                if (first) {
                    first = false;
                }

                prompt_tokens.push_back(p.template get<llama_token>());
            }
        }
    } else {
        auto s = json_prompt.template get<std::string>();
        prompt_tokens = common_tokenize(vocab, s, add_special, parse_special);
    }

    return prompt_tokens;
}

/**
 * break the input "prompt" object into multiple prompt if needed, then tokenize them
 * this supports these cases:
 * - "prompt": "string"
 * - "prompt": [12, 34, 56]
 * - "prompt": [12, 34, "string", 56, 78]
 * and multiple prompts (multi-tasks):
 * - "prompt": ["string1", "string2"]
 * - "prompt": ["string1", [12, 34, 56]]
 * - "prompt": [[12, 34, 56], [78, 90, 12]]
 * - "prompt": [[12, 34, "string", 56, 78], [12, 34, 56]]
 */
static std::vector<llama_tokens> tokenize_input_prompts(const llama_vocab * vocab, const json & json_prompt, bool add_special, bool parse_special) {
    std::vector<llama_tokens> result;
    if (json_prompt.is_string() || json_is_array_of_mixed_numbers_strings(json_prompt)) {
        // string or mixed
        result.push_back(tokenize_mixed(vocab, json_prompt, add_special, parse_special));
    } else if (json_is_array_of_numbers(json_prompt)) {
        // array of tokens
        result.push_back(json_prompt.get<llama_tokens>());
    } else if (json_prompt.is_array()) {
        // array of prompts
        result.reserve(json_prompt.size());
        for (const auto & p : json_prompt) {
            if (p.is_string() || json_is_array_of_mixed_numbers_strings(p)) {
                result.push_back(tokenize_mixed(vocab, p, add_special, parse_special));
            } else if (json_is_array_of_numbers(p)) {
                // array of tokens
                result.push_back(p.get<llama_tokens>());
            } else {
                throw std::runtime_error("element of \"prompt\" must be a string, an list of tokens, or a list of mixed strings & tokens");
            }
        }
    } else {
        throw std::runtime_error("\"prompt\" must be a string, an list of tokens, a list of mixed strings & tokens, or a list of prompts");
    }
    if (result.empty()) {
        throw std::runtime_error("\"prompt\" must not be empty");
    }
    return result;
}




//
// utils for interacting with libmtmd
// (may need to refactor in near future)
//

/**
 * server_tokens is a helper to manage the input tokens and image for the server.
 * it is made this way to simplify the logic of KV cache management.
 */
struct server_tokens {
    bool has_mtmd = false;

private: // disallow accessing these members directly, risking out-of-sync

    // map a **start** position in tokens to the image chunk
    std::unordered_map<llama_pos, mtmd::input_chunk_ptr> map_pos_to_image;

    // list of tokens
    // it can include LLAMA_TOKEN_NULL, which is used to indicate a token that is not a text token
    // a mtmd_input_chunk can occupy multiple tokens, one llama_token per **position**
    // important: for models using mrope, an image can contain multiple tokens but will use only one **position**
    llama_tokens tokens;

    // for ex. with input of 5 text tokens and 2 images:
    //      [0] [1] [2] [3] [4] [img0] [img0] [img0] [img1] [img1]
    // pos  0   1   2   3   4   5      6      7      8      9
    // map_pos_to_image will contain: {5, img0}, {8, img1}

public:
    server_tokens() = default;
    ~server_tokens() = default;

    // Prevent copying
    server_tokens(const server_tokens&) = delete;
    server_tokens& operator=(const server_tokens&) = delete;

    // Allow moving (usually implicitly generated if members are movable)
    server_tokens(server_tokens&&) = default;
    server_tokens& operator=(server_tokens&&) = default;

    // Allow accessing elements using [] operator
    llama_token operator[](size_t index) { return tokens[index]; }
    const llama_token& operator[](size_t index) const { return tokens[index]; }

    server_tokens(mtmd::input_chunks & mtmd_chunks, bool has_mtmd) : has_mtmd(has_mtmd) {
        for (size_t i = 0; i < mtmd_chunks.size(); ++i) {
            push_back(mtmd_chunks[i]);
        }
    }

    server_tokens(llama_tokens & tokens, bool has_mtmd) : has_mtmd(has_mtmd), tokens(tokens) {}

    // for debugging
    std::string str() const {
        std::ostringstream oss;
        oss << "tokens: ";
        for (const auto & t : tokens) {
            if (t == LLAMA_TOKEN_NULL) {
                oss << "<embd> ";
            } else {
                oss << t << " ";
            }
        }
        oss << "\n";
        oss << "image pos: ";
        for (const auto & it : map_pos_to_image) {
            oss << it.first << ", ";
        }
        return oss.str();
    }

    const mtmd::input_chunk_ptr & find_chunk(llama_pos pos) const {
        auto it = map_pos_to_image.find(pos);
        if (it != map_pos_to_image.end()) {
            return it->second;
        } else {
            throw std::runtime_error("Chunk not found");
        }
    }

    void push_back(llama_token tok) {
        if (tok == LLAMA_TOKEN_NULL) {
            throw std::runtime_error("Invalid token");
        }
        tokens.emplace_back(tok);
    }

    // will create a copy of the chunk if it contains non-text data
    void push_back(const mtmd_input_chunk * chunk) {
        auto type = mtmd_input_chunk_get_type(chunk);
        if (type == MTMD_INPUT_CHUNK_TYPE_IMAGE) {
            GGML_ASSERT(has_mtmd);
            auto img_tokens = mtmd_input_chunk_get_tokens_image(chunk);
            const int n_pos = mtmd_image_tokens_get_n_pos(img_tokens);
            llama_pos start_pos = tokens.size();
            for (int i = 0; i < n_pos; ++i) {
                tokens.emplace_back(LLAMA_TOKEN_NULL);
            }
            mtmd::input_chunk_ptr new_chunk(mtmd_input_chunk_copy(chunk));
            map_pos_to_image[start_pos] = std::move(new_chunk);
        } else if (type == MTMD_INPUT_CHUNK_TYPE_TEXT) {
            size_t n_tokens;
            auto text_tokens = mtmd_input_chunk_get_tokens_text(chunk, &n_tokens);
            for (size_t i = 0; i < n_tokens; ++i) {
                push_back(text_tokens[i]);
            }
        } else {
            GGML_ABORT("Invalid chunk type");
        }
    }

    // for compatibility with context shift and prompt truncation
    void insert(const llama_tokens & inp_tokens) {
        GGML_ASSERT(!has_mtmd); // only allow this if mtmd is disabled
        tokens.insert(tokens.end(), inp_tokens.begin(), inp_tokens.end());
    }

    // for compatibility with speculative decoding, ctx shift, slot save/load
    const llama_tokens & get_text_tokens() const {
        GGML_ASSERT(!has_mtmd); // only allow this if mtmd is disabled
        return tokens;
    }

    // for compatibility with speculative decoding
    void set_token(llama_pos pos, llama_token id) {
        GGML_ASSERT(!has_mtmd); // only allow this if mtmd is disabled
        tokens[pos] = id;
    }

    size_t size() const {
        return tokens.size();
    }

    bool empty() const {
        return tokens.empty();
    }

    void clear() {
        tokens.clear();
    }

    void resize(size_t n) {
        GGML_ASSERT(n <= tokens.size());
        if (has_mtmd) {
            // we throw an error if we try to remove a token in the middle of an image
            // for ex. with input of 5 text tokens and 2 images:
            //    [0] [1] [2] [3] [4] [img0] [img0] [img0] [img1] [img1]
            // n  1   2   3   4   5   6      7      8      9      10
            // allowed to resize      ^                    ^
            // disallowed to resize          ^      ^             ^
            if (n > 0) {
                llama_token last_token = tokens[n - 1];
                // make sure we never remove tokens in the middle of an image
                if (last_token == LLAMA_TOKEN_NULL) {
                    find_chunk(n - 1); // will throw an error if the token is not begin-of-chunk
                }
            }
            // remove all image chunks that are not used anymore
            for (auto it = map_pos_to_image.begin(); it != map_pos_to_image.end(); ) {
                llama_pos pos = it->first;
                if (pos >= (llama_pos)n) {
                    it = map_pos_to_image.erase(it);
                } else {
                    ++it;
                }
            }
        }
        tokens.resize(n);
    }

    std::string detokenize(const llama_context * ctx, bool special) const {
        llama_tokens text_tokens;
        text_tokens.reserve(tokens.size());
        for (const auto & t : tokens) {
            if (t != LLAMA_TOKEN_NULL) {
                text_tokens.push_back(t);
            }
        }
        return common_detokenize(ctx, text_tokens, special);
    }

    size_t get_common_prefix(const server_tokens & b) const {
        size_t max_idx = std::min(tokens.size(), b.tokens.size());
        for (size_t i = 0; i < max_idx; ++i) {
            auto & ai =   tokens[i];
            auto & bi = b.tokens[i];

            if (ai == LLAMA_TOKEN_NULL && bi == LLAMA_TOKEN_NULL) {
                GGML_ASSERT(has_mtmd);
                const auto & a_chunk =   find_chunk(i);
                const auto & b_chunk = b.find_chunk(i);
                GGML_ASSERT(a_chunk && b_chunk);
                const auto * a_img = mtmd_input_chunk_get_tokens_image(a_chunk.get());
                const auto * b_img = mtmd_input_chunk_get_tokens_image(b_chunk.get());
                std::string ai_id  = mtmd_image_tokens_get_id(a_img);
                std::string bi_id  = mtmd_image_tokens_get_id(b_img);
                size_t a_pos       = mtmd_image_tokens_get_n_pos(a_img);
                size_t b_pos       = mtmd_image_tokens_get_n_pos(b_img);
                if (ai_id == bi_id && a_pos == b_pos) {
                    GGML_ASSERT(a_pos > 0 && "Invalid image token"); // should never happen
                    i += a_pos - 1; // will be +1 by the for loop
                    continue;
                } else {
                    return i;
                }
            } else if (ai == bi) {
                continue;
            } else {
                return i;
            }
        }
        return max_idx; // all tokens are equal
    }

    // make sure all text tokens are within the vocab range
    bool validate(const struct llama_context * ctx) const {
        const llama_model * model = llama_get_model(ctx);
        const llama_vocab * vocab = llama_model_get_vocab(model);
        const int32_t n_vocab = llama_vocab_n_tokens(vocab);

        for (size_t i = 0; i < tokens.size(); ++i) {
            auto & t = tokens[i];
            if (t == LLAMA_TOKEN_NULL) {
                try {
                    const auto & chunk = find_chunk(i);
                    const auto * img_tokens = mtmd_input_chunk_get_tokens_image(chunk.get());
                    size_t n_pos = mtmd_image_tokens_get_n_pos(img_tokens);
                    i += n_pos - 1; // will be +1 by the for loop
                } catch (const std::exception & e) {
                    return false;
                }
            } else if (t < 0 || t >= n_vocab) {
                return false;
            }
        }
        return true;
    }

    // encode and decode the image chunk
    int32_t process_chunk(
                llama_context * ctx,
                mtmd_context * mctx,
                llama_pos n_past,
                int32_t seq_id,
                llama_pos & n_pos_out) {
        auto it = map_pos_to_image.find(n_past);
        if (it == map_pos_to_image.end()) {
            throw std::runtime_error("Chunk not found");
        }
     //   SRV_INF("%s\n", "processing image...");
        int32_t n_batch = llama_n_batch(ctx);
        int64_t t0 = ggml_time_ms();
        llama_pos new_n_past = n_past;
        int32_t result = mtmd_helper_eval_chunk_single(mctx, ctx,
            it->second.get(), // chunk
            n_past,
            seq_id,
            n_batch,
            true, // logits last
            &new_n_past);
        //SRV_INF("image processed in %" PRId64 " ms\n", ggml_time_ms() - t0);
        if (result != 0) {
            LOG_ERR("mtmd_helper_eval failed with status %d", result);
            n_pos_out = n_past;
            return result;
        }
        n_pos_out = new_n_past;
        return 0;
    }
};

// Computes FNV-1a hash of the data
static std::string fnv_hash(const uint8_t * data, size_t len) {
    const uint64_t fnv_prime = 0x100000001b3ULL;
    uint64_t hash = 0xcbf29ce484222325ULL;

    for (size_t i = 0; i < len; ++i) {
        hash ^= data[i];
        hash *= fnv_prime;
    }
    return std::to_string(hash);
}