// SPDX-License-Identifier: MIT
#include "engine/framework/core/execution_context.h"
#include "engine/framework/runtime/graph_executor.h"
#include <ggml-cpu.h>
#include <chrono>
#include <condition_variable>
#include <cstdio>
#include <cstdlib>
#include <mutex>
#include <stdexcept>
#include <thread>
#include <type_traits>
#include <vector>
using namespace engine::core;
static void require(bool ok, const char * what) {
    if (!ok) { std::fprintf(stderr, "FAIL: %s\n", what); std::abort(); }
}
struct Progress {
    std::mutex mutex;
    std::condition_variable cv;
    int nodes = 0;
    bool block = false, reached = false, release = false;
    RunStopHandle stop_at_node;
};
static void node(ggml_tensor * dst, const ggml_tensor * src, int ith, int, void * data) {
    require(ith == 0, "single CPU task");
    auto & p = *static_cast<Progress *>(data);
    for (int i = 0; i < 32; ++i)
        // GraphExecutor owns allocation and can relocate input storage after a
        // failed graph. This model-free source node generates a known constant.
        static_cast<float *>(dst->data)[i] = (src->op == GGML_OP_NONE ? 1.0f :
            static_cast<const float *>(src->data)[i]) + 1;
    std::unique_lock<std::mutex> lock(p.mutex);
    ++p.nodes;
    if (p.stop_at_node) p.stop_at_node->cancel();
    if (p.block) {
        p.reached = true;
        p.cv.notify_all();
        require(p.cv.wait_for(lock, std::chrono::seconds(10), [&] { return p.release; }), "bounded node release");
    }
}
struct Graph {
    ggml_context * ctx;
    ggml_cgraph * graph;
    ggml_tensor * output;
    ggml_tensor * input;
    Progress progress;
    explicit Graph(bool allocate = true) {
        ctx = ggml_init({16 * 1024 * 1024, nullptr, !allocate});
        require(ctx, "graph allocation");
        input = ggml_new_tensor_1d(ctx, GGML_TYPE_F32, 32);
        ggml_set_input(input);
        if (allocate) ggml_set_f32(input, 1);
        output = ggml_map_custom1(ctx, input, node, 1, &progress);
        for (int i = 0; i < 6; ++i) {
            output = ggml_map_custom1(ctx, output, node, 1, &progress);
            if (allocate) ggml_set_f32(output, -123);
        }
        graph = ggml_new_graph(ctx);
        ggml_build_forward_expand(graph, output);
    }
    ~Graph() { ggml_free(ctx); }
};
static void exercise(int mode, bool during_binding) {
    ExecutionContext context(BackendConfig{});
    require(context.supports_cancellation(), "real CPU registry abort setter");
    Graph graph;
    HostGraphPlan plan;
    auto a = std::make_shared<RunStopState>();
    auto b = std::make_shared<RunStopState>();
    auto prepare = [&] {
        if (mode) prepare_host_graph_plan(context, graph.graph, plan);
        if (mode) require(plan.active(), "actual backend captured plan");
    };
    if (!during_binding) prepare();
    auto compute = [&] {
        if (mode == 2) return compute_graph(context, graph.graph, plan);
        return compute_backend_graph(context, graph.graph, plan.plan);
    };
    {
        auto binding = context.begin_cancellable_run(a);
        if (during_binding) prepare();
        bool rejected = false;
        try { auto overlap = context.begin_cancellable_run(b); }
        catch (const std::logic_error &) { rejected = true; }
        require(rejected, "overlap refused");
        graph.progress.block = true;
        std::thread canceller([&] {
            std::unique_lock<std::mutex> lock(graph.progress.mutex);
            require(graph.progress.cv.wait_for(lock, std::chrono::seconds(10), [&] { return graph.progress.reached; }), "real node started");
            a->cancel(); a->cancel();
            graph.progress.release = true;
            graph.progress.cv.notify_all();
        });
        const auto status = compute();
        canceller.join();
        require(status == GGML_STATUS_ABORTED, "real backend ABORTED");
        require(graph.progress.nodes == 1, "graph truncated after prefix");
        require(static_cast<float *>(graph.output->data)[0] == -123, "sentinel untouched");
        require(compute_backend_graph(context, nullptr) == GGML_STATUS_FAILED, "invalid graph precedes stop");
        require(compute() == GGML_STATUS_ABORTED && graph.progress.nodes == 1, "pre-cancel submits no nodes");
    }
    {
        auto binding = context.begin_cancellable_run(b);
        graph.progress.nodes = 0;
        graph.progress.reached = graph.progress.release = false;
        std::thread late([&] {
            std::unique_lock<std::mutex> lock(graph.progress.mutex);
            require(graph.progress.cv.wait_for(lock, std::chrono::seconds(10), [&] { return graph.progress.reached; }), "B started");
            a->cancel();
            graph.progress.release = true;
            graph.progress.cv.notify_all();
        });
        const auto status = compute();
        late.join();
        require(status == GGML_STATUS_SUCCESS && graph.progress.nodes == 7, "late A cannot cancel B");
        require(static_cast<float *>(graph.output->data)[0] == 8, "full output");
    }
    graph.progress.block = false;
    require(compute() == GGML_STATUS_SUCCESS, "legacy unbound compute");
    ExecutionContext isolated(BackendConfig{});
    if (mode) require(compute_graph(isolated, graph.graph, plan) == GGML_STATUS_FAILED, "context association");
    auto c = std::make_shared<RunStopState>(); c->cancel();
    {
        auto binding = isolated.begin_cancellable_run(c);
        require(compute() == GGML_STATUS_SUCCESS, "context isolation");
    }
    auto * wrong = ggml_new_graph(graph.ctx);
    if (mode) require(compute_graph(context, wrong, plan) == GGML_STATUS_FAILED, "graph association");
    ExecutionContext moved(std::move(context));
    if (mode) require(compute_graph(moved, graph.graph, plan) == GGML_STATUS_SUCCESS, "inactive move retains bridge");
    ExecutionContext assigned(BackendConfig{});
    assigned = std::move(moved);
    require(compute_backend_graph(assigned, graph.graph, plan.plan) == GGML_STATUS_SUCCESS,
            "inactive move assignment preserves backend and callback");
    if (mode) require(compute_graph(assigned, graph.graph, plan) == GGML_STATUS_SUCCESS,
                      "inactive move assignment preserves plan bridge");
    auto stopped = std::make_shared<RunStopState>(); stopped->cancel();
    { auto binding = assigned.begin_cancellable_run(stopped);
      require(compute_backend_graph(assigned, graph.graph, plan.plan) == GGML_STATUS_ABORTED,
              "move-assigned callback sees bound stop"); }
    plan.reset();
    a->cancel();
}
namespace engine::core {
// Deliberately not in a production header: compiled only by the test target.
using AbortLookupForTest = void * (*)(ggml_backend_reg_t, const char *);
AbortLookupForTest exchange_abort_lookup_for_test(AbortLookupForTest);
}
struct ScopedMissingAbortHook {
    AbortLookupForTest previous;
    ScopedMissingAbortHook() : previous(exchange_abort_lookup_for_test(
        +[](ggml_backend_reg_t, const char *) -> void * { return nullptr; })) {}
    ~ScopedMissingAbortHook() { exchange_abort_lookup_for_test(previous); }
};
static void missing_hook_tests() {
    ExecutionContext separate(BackendConfig{});
    Graph graph;
    {
        ScopedMissingAbortHook missing;
        ExecutionContext unsupported(BackendConfig{});
        require(unsupported.backend() != nullptr, "real CPU backend acquired without hook");
        require(!unsupported.supports_cancellation(), "missing registry hook unsupported");
        bool refused = false;
        try { auto binding = unsupported.begin_cancellable_run(std::make_shared<RunStopState>()); }
        catch (const std::logic_error &) { refused = true; }
        require(refused && graph.progress.nodes == 0, "missing hook refuses admission before compute");
        require(separate.supports_cancellation(), "separate existing context unaffected");
        auto binding = separate.begin_cancellable_run(std::make_shared<RunStopState>());
        require(compute_backend_graph(separate, graph.graph) == GGML_STATUS_SUCCESS,
                "separate real backend still computes");
    }
    ExecutionContext restored(BackendConfig{});
    require(restored.supports_cancellation(), "registry lookup restored");
    std::puts("PASS: actual CPU missing-hook admission refusal and scoped restoration");
}
static void injected_status_tests() {
    ExecutionContext context(BackendConfig{});
    Graph graph;
    for (auto status : {GGML_STATUS_FAILED, GGML_STATUS_ALLOC_FAILED}) {
        inject_backend_graph_status_for_test(status);
        require(compute_backend_graph(context, graph.graph) == status, "injected wrapper status unchanged");
    }
    Graph allocated(false);
    engine::runtime::GraphExecutor executor(context);
    for (auto status : {GGML_STATUS_FAILED, GGML_STATUS_ALLOC_FAILED}) {
        for (int warmup : {0, 2}) {
            inject_backend_graph_status_for_test(status);
            auto result = executor.run(allocated.ctx, allocated.output, warmup, 3);
            require(result.status == status && result.average_ms == 0 &&
                    result.completed_iterations == 0 && result.completed_warmup == 0,
                    "injected executor exact failure, no average or later iterations");
        }
    }
    require(allocated.progress.nodes == 0, "injected statuses submit no nodes");
    require(executor.run(allocated.ctx, allocated.output, 1, 2).status == GGML_STATUS_SUCCESS,
            "executor rebuilds after injected failures");
    std::puts("PASS: injected FAILED/ALLOC_FAILED dispatch status tests (not real GGML failure)");
}
static void executor_tests() {
    ExecutionContext context(BackendConfig{});
    Graph graph(false);
    engine::runtime::GraphExecutor executor(context);
    auto a = std::make_shared<RunStopState>();
    graph.progress.stop_at_node = a;
    {
        auto binding = context.begin_cancellable_run(a);
        auto result = executor.run(graph.ctx, graph.output, 3, 4);
        require(result.status == GGML_STATUS_ABORTED && result.average_ms == 0 &&
                result.completed_warmup == 0 && result.completed_iterations == 0 &&
                graph.progress.nodes == 1, "warmup abort stops all iterations");
    }
    auto b = std::make_shared<RunStopState>();
    graph.progress.stop_at_node = b;
    graph.progress.nodes = 0;
    {
        auto binding = context.begin_cancellable_run(b);
        auto result = executor.run(graph.ctx, graph.output, 0, 4);
        require(result.status == GGML_STATUS_ABORTED && result.average_ms == 0 &&
                result.completed_iterations == 0 && graph.progress.nodes == 1, "measured abort stops iterations");
    }
    std::vector<float> values(32, 1.0f);
    ggml_backend_tensor_set(graph.input, values.data(), 0, values.size() * sizeof(float));
    graph.progress.stop_at_node.reset();
    graph.progress.nodes = 0;
    auto c = std::make_shared<RunStopState>();
    {
        auto binding = context.begin_cancellable_run(c);
        auto result = executor.run(graph.ctx, graph.output, 2, 3);
        require(result.status == GGML_STATUS_SUCCESS && result.completed_warmup == 2 &&
                result.completed_iterations == 3 && graph.progress.nodes == 35, "executor rebuilt after failure");
    }
    require(static_cast<float *>(graph.output->data)[0] == 8, "executor complete output");
    auto result = executor.run(graph.ctx, graph.output, 1, 2);
    require(result.status == GGML_STATUS_SUCCESS && result.completed_iterations == 2 && result.average_ms >= 0, "legacy timings");
    bool rejected = false;
    try { executor.run(nullptr, graph.output, 0, 1); } catch (const std::runtime_error &) { rejected = true; }
    require(rejected, "null executor input");
    rejected = false;
    try { executor.run(graph.ctx, nullptr, 0, 1); } catch (const std::runtime_error &) { rejected = true; }
    require(rejected, "null executor output");
}
int main() {
    static_assert(!std::is_copy_constructible<HostGraphPlan>::value, "owning plans cannot copy");
    require(!set_backend_abort_callback(nullptr, nullptr, nullptr), "missing backend unsupported");
    for (int mode = 0; mode < 3; ++mode) for (bool during : {false, true}) exercise(mode, during);
    missing_hook_tests();
    executor_tests();
    injected_status_tests();
    std::puts("PASS: actual CPU ordinary/captured/host plans, run ownership, isolation, executor abort/reuse");
}
