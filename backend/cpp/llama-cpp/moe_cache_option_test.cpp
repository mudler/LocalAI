// SPDX-License-Identifier: MIT

#include "moe_cache_option.h"

#include <cstdio>

static int failures = 0;

static void expect(const std::string & in, std::optional<size_t> want) {
    const auto got = llama_grpc::parse_moe_cache_bytes(in);
    if (got != want) {
        std::fprintf(stderr, "parse_moe_cache_bytes(\"%s\") returned a wrong result\n", in.c_str());
        failures++;
    }
}

static void expect_share(const std::string & in, std::optional<float> want) {
    const auto got = llama_grpc::parse_moe_cache_cpu_share(in);
    if (got != want) {
        std::fprintf(stderr, "parse_moe_cache_cpu_share(\"%s\") returned a wrong result\n", in.c_str());
        failures++;
    }
}

int main() {
    // MiB to bytes.
    expect("0", 0);
    expect("1", 1024ull * 1024ull);
    expect("2048", 2048ull * 1024ull * 1024ull);
    // 8 GiB must not overflow a 32-bit intermediate.
    expect("8192", 8192ull * 1024ull * 1024ull);

    // Invalid values are rejected so the caller keeps the default.
    expect("-1", std::nullopt);
    expect("", std::nullopt);
    expect("abc", std::nullopt);
    expect("12abc", std::nullopt);
    expect("1.5", std::nullopt);
    expect("99999999999999999999999", std::nullopt);
    // Too large to express in bytes.
    expect("9223372036854775807", std::nullopt);

    // moe_cache_cpu_share accepts 0 to 1 inclusive.
    expect_share("0", 0.0f);
    expect_share("0.0", 0.0f);
    expect_share("0.5", 0.5f);
    expect_share("0.75", 0.75f);
    expect_share("1", 1.0f);
    expect_share("1.0", 1.0f);
    expect_share(".25", 0.25f);

    // Out of range, malformed or non-finite values are rejected.
    expect_share("-0.1", std::nullopt);
    expect_share("1.01", std::nullopt);
    expect_share("2", std::nullopt);
    expect_share("", std::nullopt);
    expect_share("abc", std::nullopt);
    expect_share("0.5x", std::nullopt);
    expect_share(" 0.5", std::nullopt);
    expect_share("nan", std::nullopt);
    expect_share("inf", std::nullopt);
    expect_share("1e400", std::nullopt);

    return failures == 0 ? 0 : 1;
}
