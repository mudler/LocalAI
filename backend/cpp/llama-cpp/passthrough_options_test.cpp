// SPDX-License-Identifier: MIT

#include <cstdio>

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

int main() {
    test_stages_resolved_gpu_layers_for_upstream_parser();
    test_keeps_explicit_passthrough_overrides();
    test_keeps_explicit_auto_passthrough_overrides();
    return failures == 0 ? 0 : 1;
}
