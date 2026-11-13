// SPDX-License-Identifier: MIT

#pragma once

#include <cstdint>

#include <nlohmann/json.hpp>

namespace llama_grpc {

// Reads the prompt-cache hit count from a llama.cpp server result JSON.
// Prefers the OpenAI-shaped usage.prompt_tokens_details.cached_tokens field
// (what llama.cpp's HTTP server and the OpenAI API emit), then the native
// timings.cache_n field used by the non-OAI result shape.
template <typename Json>
inline int32_t json_int32_field(const Json & obj, const char * key) {
    if (!obj.contains(key) || !obj.at(key).is_number_integer()) {
        return 0;
    }
    return static_cast<int32_t>(obj.at(key).template get<int64_t>());
}

template <typename Json>
inline int32_t cached_tokens_from_result_json(const Json & result_json) {
    if (result_json.contains("usage") && result_json.at("usage").is_object()) {
        const auto & usage = result_json.at("usage");
        if (usage.contains("prompt_tokens_details") && usage.at("prompt_tokens_details").is_object()) {
            return json_int32_field(usage.at("prompt_tokens_details"), "cached_tokens");
        }
    }
    if (result_json.contains("timings") && result_json.at("timings").is_object()) {
        return json_int32_field(result_json.at("timings"), "cache_n");
    }
    return 0;
}

} // namespace llama_grpc
