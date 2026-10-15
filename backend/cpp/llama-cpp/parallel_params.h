#pragma once

#include <optional>
#include <string>

namespace llama_grpc {

// resolve_n_parallel picks the slot count. A value from the model options
// always wins, including an explicit 1: the YAML takes precedence over the
// environment, as documented. LLAMACPP_PARALLEL is only the fallback when
// the options do not set it; a value that does not parse is ignored.
inline int resolve_n_parallel(const std::optional<int>& from_options, const char* env, int fallback = 1) {
    if (from_options) {
        return *from_options;
    }
    if (env != nullptr) {
        try {
            return std::stoi(env);
        } catch (const std::exception&) {
        }
    }
    return fallback;
}

} // namespace llama_grpc
