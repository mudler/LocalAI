// SPDX-License-Identifier: MIT
#include "music_cancellation.h"
#include <ggml-cpu.h>
#include <stdexcept>

namespace audiocpp_backend {
struct MusicCpuExecution::CpuPlan {
    ggml_cplan value;
    std::unique_ptr<uint8_t[]> workspace;
    CpuPlan(ggml_cgraph *graph, MusicStopState *stop)
        : value(ggml_graph_plan(graph, 2, nullptr)),
          workspace(value.work_size ? new uint8_t[value.work_size] : nullptr) {
        value.work_data = workspace.get();
        // Install before any execution; cached plans keep the stable stop state.
        value.abort_callback = abort_requested;
        value.abort_callback_data = stop;
    }
};
bool MusicCpuExecution::abort_requested(void *data) noexcept {
    return static_cast<MusicStopState *>(data)->cancelled();
}
MusicCpuExecution::MusicCpuExecution(InferenceLane &lane
#ifdef MUSIC_CPU_TEST_FAULTS
    , Fault fault
#endif
)
    : lane_(lane, LaneEntry::TryNow{}), stop_(std::make_shared<MusicStopState>()) {
#ifdef MUSIC_CPU_TEST_FAULTS
    fault_ = fault;
#endif
    // The pinned backend factories use raw new before another throwing new:
    // init can leak its context, plan creation can leak its plan, and workspace
    // growth deletes the old buffer before throwing (leaving a dangling pointer).
    // Own the public CPU cplan and workspace instead; same real CPU planner,
    // compute and abort polling, with no unsafe backend factory to unwind.
    ggml_cpu_init();
}
MusicCpuExecution::~MusicCpuExecution() noexcept {
    stop_->cancel();
    if (worker_.joinable()) worker_.join();
}
void MusicCpuExecution::cache(ggml_cgraph *graph) {
    if (worker_.joinable()) throw std::logic_error("join before caching a graph");
    if (!graph) throw std::invalid_argument("missing CPU graph");
    auto replacement = std::make_unique<CpuPlan>(graph, stop_.get());
    plan_ = std::move(replacement); // strong guarantee if planning/allocation fails
    cached_graph_ = graph;
}
void MusicCpuExecution::start(ggml_cgraph *graph, bool cached) {
    if (worker_.joinable()) throw std::logic_error("join before starting another CPU graph");
    if (!graph || (cached && (!plan_ || cached_graph_ != graph)))
        throw std::invalid_argument("missing or mismatched cached CPU graph");
    status_ = GGML_STATUS_FAILED;
    worker_ = std::thread([this, graph, cached]() noexcept {
        try {
#ifdef MUSIC_CPU_TEST_FAULTS
            // Only this actual std::thread can inject, before entering GGML/C
            // or OpenMP. Never a replacement compute function or global OOM.
            if (fault_ == Fault::Allocation) throw std::bad_alloc();
            if (fault_ == Fault::Generic) throw 17;
#endif
            // ggml CPU polls after an operation/group, not within a kernel.
            if (stop_->cancelled()) {
                status_ = GGML_STATUS_ABORTED;
            } else if (cached) {
                status_ = ggml_graph_compute(graph, &plan_->value);
            } else {
                CpuPlan transient(graph, stop_.get());
                status_ = ggml_graph_compute(graph, &transient.value);
            }
        } catch (const std::bad_alloc &) {
            status_ = GGML_STATUS_ALLOC_FAILED;
        } catch (...) {
            status_ = GGML_STATUS_FAILED;
        }
    });
}
ggml_status MusicCpuExecution::join() {
    if (!worker_.joinable()) throw std::logic_error("no CPU execution to join");
    worker_.join();
    return status_;
}
} // namespace audiocpp_backend
