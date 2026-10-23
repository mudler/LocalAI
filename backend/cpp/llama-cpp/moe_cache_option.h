// SPDX-License-Identifier: MIT

#pragma once

#include <cstddef>
#include <cstdint>
#include <exception>
#include <optional>
#include <string>

namespace llama_grpc {

// parse_moe_cache_bytes converts the value of the moe_cache_mib model option
// (a whole number of MiB) to the byte count that common_params::moe_cache_size
// expects. It returns std::nullopt for anything that is not a non-negative
// integer that fits in size_t once converted, so the caller can log and keep
// the default (0, which means the cache is off).
inline std::optional<size_t> parse_moe_cache_bytes(const std::string & value) {
    constexpr uint64_t MIB = 1024ull * 1024ull;
    try {
        size_t used = 0;
        const long long mib = std::stoll(value, &used);
        if (used != value.size() || mib < 0) {
            return std::nullopt;
        }
        if (static_cast<unsigned long long>(mib) > SIZE_MAX / MIB) {
            return std::nullopt;
        }
        return static_cast<size_t>(mib) * static_cast<size_t>(MIB);
    } catch (const std::exception &) {
        return std::nullopt;
    }
}

} // namespace llama_grpc
