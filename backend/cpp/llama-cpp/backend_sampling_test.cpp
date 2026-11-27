#include "backend_sampling.h"

#include <cstdio>

using namespace llama_grpc;

static int failures = 0;

static void check(bool ok, const char * what) {
    if (!ok) {
        std::fprintf(stderr, "FAIL: %s\n", what);
        failures++;
    }
}

int main() {
    // Parsing: on/off/auto, with the boolean spellings a UI toggle writes.
    for (const char * v : {"on", "true", "1", "yes", "enabled", "ON", "True"}) {
        check(parse_backend_sampling_mode(v) == backend_sampling_mode::on, v);
    }
    for (const char * v : {"off", "false", "0", "no", "disabled", "OFF"}) {
        check(parse_backend_sampling_mode(v) == backend_sampling_mode::off, v);
    }
    check(parse_backend_sampling_mode("auto") == backend_sampling_mode::automatic, "auto");
    check(parse_backend_sampling_mode("AUTO") == backend_sampling_mode::automatic, "AUTO");
    check(!parse_backend_sampling_mode("").has_value(), "empty is unknown");
    check(!parse_backend_sampling_mode("gpu").has_value(), "gpu is unknown");

    backend_sampling_model gpu;
    gpu.gpu_offload = true;
    const backend_sampling_request plain;

    // The rule: on only with GPU offload, no tensor split, a plain request, and
    // enough requests running.
    check(gpu.min_slots == 16, "default threshold 16");
    check(use_backend_sampling(gpu, plain, 16), "16 slots on a GPU");
    check(use_backend_sampling(gpu, plain, 32), "32 slots on a GPU");
    check(!use_backend_sampling(gpu, plain, 15), "below the default threshold");
    check(!use_backend_sampling(gpu, plain, 8), "8 slots: no gain measured");
    check(!use_backend_sampling(gpu, plain, 1), "a single slot");

    backend_sampling_model cpu = gpu;
    cpu.gpu_offload = false;
    check(!use_backend_sampling(cpu, plain, 16), "CPU-only model");

    backend_sampling_model tensor = gpu;
    tensor.tensor_split = true;
    check(!use_backend_sampling(tensor, plain, 16), "tensor split");

    backend_sampling_request grammar;
    grammar.grammar = true;
    check(!use_backend_sampling(gpu, grammar, 16), "grammar");

    backend_sampling_request budget;
    budget.reasoning_budget = true;
    check(!use_backend_sampling(gpu, budget, 16), "reasoning budget");

    backend_sampling_request probs;
    probs.pre_sampling_probs = true;
    check(!use_backend_sampling(gpu, probs, 16), "pre-sampling logprobs");

    backend_sampling_model low = gpu;
    low.min_slots = 1;
    check(use_backend_sampling(low, plain, 1), "threshold 1");
    low.min_slots = 0;
    check(use_backend_sampling(low, plain, 1), "threshold 0 counts as 1");
    check(!use_backend_sampling(low, plain, 0), "no request running");

    return failures == 0 ? 0 : 1;
}
