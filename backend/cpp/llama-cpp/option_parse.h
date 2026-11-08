// SPDX-License-Identifier: MIT

#pragma once

#include <cstddef>
#include <exception>
#include <optional>
#include <string>

// Value parsers for the model options of grpc-server.cpp that need more than
// "stoi and ignore errors". Each returns std::nullopt for a value the option
// does not accept, so the caller can log a warning and keep the default.

namespace llama_grpc {

namespace option_parse_detail {

inline std::string lower(std::string s) {
    for (auto & c : s) {
        if (c >= 'A' && c <= 'Z') {
            c = static_cast<char>(c - 'A' + 'a');
        }
    }
    return s;
}

inline std::optional<int> whole_int(const std::string & value) {
    if (value.empty()) {
        return std::nullopt;
    }
    try {
        size_t used = 0;
        const int n = std::stoi(value, &used);
        if (used != value.size()) {
            return std::nullopt;
        }
        return n;
    } catch (const std::exception &) {
        return std::nullopt;
    }
}

} // namespace option_parse_detail

// parse_non_negative_int accepts a whole number >= 0 and nothing else. Used by
// n_cpu_ffn, kv_unified_per_slot, spec_draft_n_ubatch and yarn_orig_ctx, which
// upstream rejects below zero (or, for yarn_orig_ctx, treats 0 as "use the
// model value").
inline std::optional<int> parse_non_negative_int(const std::string & value) {
    const auto n = option_parse_detail::whole_int(value);
    if (!n || *n < 0) {
        return std::nullopt;
    }
    return n;
}

// parse_reasoning_budget_tokens parses the reasoning_budget option, which is
// upstream's --reasoning-budget: -1 unlimited, 0 end thinking at once, N > 0 a
// token cap. Anything below -1 is rejected, as upstream does.
inline std::optional<int> parse_reasoning_budget_tokens(const std::string & value) {
    const auto n = option_parse_detail::whole_int(value);
    if (!n || *n < -1) {
        return std::nullopt;
    }
    return n;
}

// parse_enable_reasoning parses the enable_reasoning option, the on/off switch
// of thinking (common_params::enable_reasoning: -1 auto, 0 off, 1 on). It takes
// the numbers and the words of upstream's --reasoning flag. For compatibility
// with configs written when this option was documented as a token cap, any
// number above 1 still means "on"; `capped` tells the caller to warn that it is
// not a cap.
inline std::optional<int> parse_enable_reasoning(const std::string & value, bool & capped) {
    capped = false;
    const std::string v = option_parse_detail::lower(value);
    if (v == "on" || v == "true" || v == "yes" || v == "enabled" || v == "1") {
        return 1;
    }
    if (v == "off" || v == "false" || v == "no" || v == "disabled" || v == "0") {
        return 0;
    }
    if (v == "auto" || v == "-1") {
        return -1;
    }
    const auto n = option_parse_detail::whole_int(v);
    if (n && *n > 1) {
        capped = true;
        return 1;
    }
    return std::nullopt;
}

enum class draft_sampling { greedy, probabilistic };

// parse_draft_sampling parses spec_draft_sampling (upstream
// --spec-draft-sampling): "greedy" or "probabilistic".
inline std::optional<draft_sampling> parse_draft_sampling(const std::string & value) {
    const std::string v = option_parse_detail::lower(value);
    if (v == "greedy") {
        return draft_sampling::greedy;
    }
    if (v == "probabilistic") {
        return draft_sampling::probabilistic;
    }
    return std::nullopt;
}

} // namespace llama_grpc
