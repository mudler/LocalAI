// SPDX-License-Identifier: MIT

#include <cstdio>
#include <cstdlib>
#include <string>
#include <vector>

#include "passthrough_options.h"

static int failures = 0;

static void check(bool ok, const char * name) {
    if (!ok) {
        ++failures;
        std::fprintf(stderr, "FAIL: %s\n", name);
    }
}

static void test_stages_resolved_gpu_layers_for_upstream_parser() {
    int main_gpu_layers = 99;
    int draft_gpu_layers = 12;

    const auto saved = llama_grpc::prepare_passthrough_gpu_layers(
        main_gpu_layers, draft_gpu_layers);

    check(main_gpu_layers == -1, "main GPU layers use parser sentinel");
    check(draft_gpu_layers == -1, "draft GPU layers use parser sentinel");

    llama_grpc::restore_passthrough_gpu_layers(
        main_gpu_layers, draft_gpu_layers, saved);

    check(main_gpu_layers == 99, "main GPU layers restored");
    check(draft_gpu_layers == 12, "draft GPU layers restored");
}

static void test_keeps_explicit_passthrough_overrides() {
    int main_gpu_layers = 99;
    int draft_gpu_layers = 12;

    const auto saved = llama_grpc::prepare_passthrough_gpu_layers(
        main_gpu_layers, draft_gpu_layers);

    main_gpu_layers = 4;
    draft_gpu_layers = 2;
    llama_grpc::restore_passthrough_gpu_layers(
        main_gpu_layers, draft_gpu_layers, saved);

    check(main_gpu_layers == 4, "main passthrough override retained");
    check(draft_gpu_layers == 2, "draft passthrough override retained");
}

static void test_keeps_explicit_auto_passthrough_overrides() {
    int main_gpu_layers = 99;
    int draft_gpu_layers = 12;

    const auto saved = llama_grpc::prepare_passthrough_gpu_layers(
        main_gpu_layers, draft_gpu_layers);

    llama_grpc::restore_passthrough_gpu_layers(
        main_gpu_layers, draft_gpu_layers, saved, true, true);

    check(main_gpu_layers == -1, "main auto passthrough override retained");
    check(draft_gpu_layers == -1, "draft auto passthrough override retained");
}

#ifndef _WIN32
static void test_env_scrub_hides_and_restores() {
    setenv("LLAMA_ARG_N_PARALLEL", "9", 1);
    setenv("LLAMA_ARG_CTX_SIZE", "123", 1);
    setenv("XDG_CONFIG_HOME", "/tmp/keep-me", 1);
    setenv("UNRELATED_VAR", "x", 1);
    {
        llama_grpc::scoped_upstream_env_scrub scrub;
        check(std::getenv("LLAMA_ARG_N_PARALLEL") == nullptr, "LLAMA_ARG_N_PARALLEL hidden");
        check(std::getenv("LLAMA_ARG_CTX_SIZE") == nullptr, "LLAMA_ARG_CTX_SIZE hidden");
        check(std::string(std::getenv("XDG_CONFIG_HOME")) != "/tmp/keep-me", "config dir redirected");
        check(std::getenv("UNRELATED_VAR") != nullptr, "other variables kept");
    }
    check(std::string(std::getenv("LLAMA_ARG_N_PARALLEL")) == "9", "LLAMA_ARG_N_PARALLEL restored");
    check(std::string(std::getenv("LLAMA_ARG_CTX_SIZE")) == "123", "LLAMA_ARG_CTX_SIZE restored");
    check(std::string(std::getenv("XDG_CONFIG_HOME")) == "/tmp/keep-me", "config dir restored");

    unsetenv("XDG_CONFIG_HOME");
    {
        llama_grpc::scoped_upstream_env_scrub scrub;
    }
    check(std::getenv("XDG_CONFIG_HOME") == nullptr, "unset config dir stays unset");
    unsetenv("LLAMA_ARG_N_PARALLEL");
    unsetenv("LLAMA_ARG_CTX_SIZE");
}
#endif

static void test_keep_parsable_flags_drops_only_bad_ones() {
    const std::vector<llama_grpc::passthrough_flag> flags = {
        {"--jinja"}, {"--spec-ngram-size-n", "3"}, {"-ngl", "10"}, {"--bogus"}};
    std::vector<llama_grpc::passthrough_flag> dropped;
    const auto kept = llama_grpc::keep_parsable_flags(
        flags,
        [](const llama_grpc::passthrough_flag & f) {
            return f[0] != "--bogus" && f[0] != "--spec-ngram-size-n";
        },
        dropped);
    check(kept.size() == 2 && kept[0][0] == "--jinja" && kept[1][0] == "-ngl", "good flags kept in order");
    check(dropped.size() == 2 && dropped[0][0] == "--spec-ngram-size-n" && dropped[1][0] == "--bogus", "bad flags reported");
}

int main() {
#ifndef _WIN32
    test_env_scrub_hides_and_restores();
#endif
    test_keep_parsable_flags_drops_only_bad_ones();
    test_stages_resolved_gpu_layers_for_upstream_parser();
    test_keeps_explicit_passthrough_overrides();
    test_keeps_explicit_auto_passthrough_overrides();
    return failures == 0 ? 0 : 1;
}
