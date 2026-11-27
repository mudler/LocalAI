#pragma once

#include <algorithm>
#include <cctype>
#include <optional>
#include <string>

namespace llama_grpc {

// backend_sampling_mode is the value of the backend_sampling model option.
// Backend sampling runs the sampler chain on the device that holds the output
// (the GPU) instead of copying the logits to the host and sampling each slot in
// turn on the CPU.
enum class backend_sampling_mode {
    off,       // sample on the CPU (llama.cpp's default)
    on,        // sample on the backend for every request llama.cpp allows
    automatic, // decide per request with use_backend_sampling
};

// parse_backend_sampling_mode reads on/off/auto. true/false, 1/0, yes/no and
// enabled/disabled are on and off. An unknown value returns nullopt.
inline std::optional<backend_sampling_mode> parse_backend_sampling_mode(std::string value) {
    std::transform(value.begin(), value.end(), value.begin(),
                   [](unsigned char c) { return static_cast<char>(std::tolower(c)); });
    if (value == "on" || value == "true" || value == "1" || value == "yes" || value == "enabled") {
        return backend_sampling_mode::on;
    }
    if (value == "off" || value == "false" || value == "0" || value == "no" || value == "disabled") {
        return backend_sampling_mode::off;
    }
    if (value == "auto") {
        return backend_sampling_mode::automatic;
    }
    return std::nullopt;
}

// The default of backend_sampling_min_slots. On a GB10 with Qwen3-8B, backend
// sampling for every request gained nothing at 8 decoding streams, 2.5% at
// 12 and 6.5% at 16 (aggregate tokens per second).
constexpr int backend_sampling_default_min_slots = 16;

// backend_sampling_model is what backend_sampling:auto knows about the loaded
// model.
struct backend_sampling_model {
    bool gpu_offload  = false; // a GPU device is present and n_gpu_layers is not 0
    bool tensor_split = false; // split_mode is tensor
    int  min_slots    = backend_sampling_default_min_slots;
};

// backend_sampling_request is what backend_sampling:auto knows about a request.
// Each field is a case where llama.cpp turns backend sampling off for the
// request anyway, so asking for it there gains nothing and changes the sampler
// state of the slot for no reason.
struct backend_sampling_request {
    bool grammar          = false; // a grammar, JSON schema or tool grammar
    bool reasoning_budget = false; // a reasoning budget sampler
    bool pre_sampling_probs = false; // logprobs taken before sampling
};

// use_backend_sampling is the decision of backend_sampling:auto for one
// request, given the number of completion requests running, this one included.
// The gain of backend sampling grows with the number of slots that decode
// together: one slot saves only a logits copy, many slots also stop the CPU
// from sampling them one after the other.
inline bool use_backend_sampling(const backend_sampling_model & model,
                                 const backend_sampling_request & request,
                                 int active_slots) {
    return model.gpu_offload &&
           !model.tensor_split &&
           !request.grammar &&
           !request.reasoning_budget &&
           !request.pre_sampling_probs &&
           active_slots >= std::max(1, model.min_slots);
}

} // namespace llama_grpc
