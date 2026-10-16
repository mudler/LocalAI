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

    return failures == 0 ? 0 : 1;
}
