// SPDX-License-Identifier: MIT

#include "option_parse.h"

#include <cstdio>

static int failures = 0;

static void check(bool ok, const char * what, const char * in) {
    if (!ok) {
        std::fprintf(stderr, "FAIL: %s (input \"%s\")\n", what, in);
        failures++;
    }
}

static void int_case(const char * in, std::optional<int> want) {
    check(llama_grpc::parse_non_negative_int(in) == want, "parse_non_negative_int", in);
}

static void budget_case(const char * in, std::optional<int> want) {
    check(llama_grpc::parse_reasoning_budget_tokens(in) == want, "parse_reasoning_budget_tokens", in);
}

static void enable_case(const char * in, std::optional<int> want, bool want_capped) {
    bool capped = true;
    const auto got = llama_grpc::parse_enable_reasoning(in, capped);
    check(got == want, "parse_enable_reasoning value", in);
    check(capped == want_capped, "parse_enable_reasoning capped flag", in);
}

int main() {
    int_case("0", 0);
    int_case("12", 12);
    int_case("-1", std::nullopt);
    int_case("", std::nullopt);
    int_case("4k", std::nullopt);
    int_case("1.5", std::nullopt);
    int_case("99999999999", std::nullopt);

    budget_case("-1", -1);
    budget_case("0", 0);
    budget_case("2048", 2048);
    budget_case("-2", std::nullopt);
    budget_case("on", std::nullopt);
    budget_case("", std::nullopt);

    // The values that gallery models and old docs use.
    enable_case("0", 0, false);
    enable_case("1", 1, false);
    enable_case("-1", -1, false);
    enable_case("off", 0, false);
    enable_case("ON", 1, false);
    enable_case("auto", -1, false);
    // Written when the option was documented as a token cap: still "on", with a warning.
    enable_case("2048", 1, true);
    enable_case("-2", std::nullopt, false);
    enable_case("maybe", std::nullopt, false);
    enable_case("", std::nullopt, false);

    using llama_grpc::draft_sampling;
    check(llama_grpc::parse_draft_sampling("greedy") == draft_sampling::greedy, "greedy", "greedy");
    check(llama_grpc::parse_draft_sampling("Probabilistic") == draft_sampling::probabilistic, "probabilistic", "Probabilistic");
    check(!llama_grpc::parse_draft_sampling("random"), "unknown sampling", "random");
    check(!llama_grpc::parse_draft_sampling(""), "empty sampling", "");

    return failures == 0 ? 0 : 1;
}
