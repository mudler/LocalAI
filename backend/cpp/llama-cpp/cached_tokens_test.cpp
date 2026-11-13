// SPDX-License-Identifier: MIT
//
// Standalone tests for cached_tokens.h. Depends only on the C++ standard
// library and nlohmann/json — picked up by backend/cpp/run-unit-tests.sh.
//
// Regression coverage for issue #13006: llama.cpp already reports prompt
// cache hits (usage.prompt_tokens_details.cached_tokens and timings.cache_n)
// but the gRPC layer dropped them.

#include <cstdint>
#include <cstdio>
#include <string>

#include <nlohmann/json.hpp>

#include "cached_tokens.h"

static int failures = 0;

static void check(bool ok, const char * name) {
    if (!ok) {
        ++failures;
        std::fprintf(stderr, "FAIL: %s\n", name);
    }
}

static void test_openai_usage_cached_tokens() {
    const nlohmann::json result = nlohmann::json::parse(R"({
        "usage": {
            "prompt_tokens": 2837,
            "completion_tokens": 4,
            "total_tokens": 2841,
            "prompt_tokens_details": {"cached_tokens": 2816}
        }
    })");
    check(llama_grpc::cached_tokens_from_result_json(result) == 2816,
          "OpenAI usage.prompt_tokens_details.cached_tokens");
}

static void test_native_timings_cache_n() {
    const nlohmann::json result = nlohmann::json::parse(R"({
        "tokens_evaluated": 2837,
        "tokens_predicted": 4,
        "timings": {"prompt_ms": 12.5, "predicted_ms": 3.0, "cache_n": 2816}
    })");
    check(llama_grpc::cached_tokens_from_result_json(result) == 2816,
          "native timings.cache_n");
}

static void test_prefers_openai_details_over_timings() {
    const nlohmann::json result = nlohmann::json::parse(R"({
        "usage": {
            "prompt_tokens": 100,
            "prompt_tokens_details": {"cached_tokens": 80}
        },
        "timings": {"cache_n": 12}
    })");
    check(llama_grpc::cached_tokens_from_result_json(result) == 80,
          "prefer prompt_tokens_details when both are present");
}

static void test_zero_when_absent() {
    const nlohmann::json result = nlohmann::json::parse(R"({
        "usage": {"prompt_tokens": 10, "completion_tokens": 2},
        "timings": {"prompt_ms": 1.0}
    })");
    check(llama_grpc::cached_tokens_from_result_json(result) == 0,
          "absent cache fields yield 0");
}

static void test_zero_on_empty_object() {
    const nlohmann::json result = nlohmann::json::object();
    check(llama_grpc::cached_tokens_from_result_json(result) == 0,
          "empty result yields 0");
}

static void test_ignores_non_object_usage() {
    const nlohmann::json result = nlohmann::json::parse(R"({
        "usage": 3,
        "timings": {"cache_n": 9}
    })");
    check(llama_grpc::cached_tokens_from_result_json(result) == 9,
          "non-object usage falls through to timings.cache_n");
}

int main() {
    test_openai_usage_cached_tokens();
    test_native_timings_cache_n();
    test_prefers_openai_details_over_timings();
    test_zero_when_absent();
    test_zero_on_empty_object();
    test_ignores_non_object_usage();

    if (failures != 0) {
        std::fprintf(stderr, "%d check(s) failed\n", failures);
        return 1;
    }
    return 0;
}
