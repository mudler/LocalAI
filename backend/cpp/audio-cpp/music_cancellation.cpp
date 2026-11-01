// SPDX-License-Identifier: MIT
#include "music_cancellation.h"
#include <ggml-cpu.h>
#include <stdexcept>

namespace audiocpp_backend {
bool MusicCpuExecution::abort_requested(void *data) noexcept {
    return static_cast<MusicStopState *>(data)->cancelled();
}
MusicCpuExecution::MusicCpuExecution(InferenceLane &lane)
    : lane_(lane, LaneEntry::TryNow{}), stop_(std::make_shared<MusicStopState>()) {
    backend_ = ggml_backend_cpu_init();
    if (!backend_) throw std::runtime_error("CPU backend allocation failed");
    ggml_backend_cpu_set_n_threads(backend_, 2);
    // Must be installed BEFORE plan creation: cached CPU plans copy these
    // pointers, and changing the backend callback does not update those plans.
    ggml_backend_cpu_set_abort_callback(backend_, abort_requested, stop_.get());
}
MusicCpuExecution::~MusicCpuExecution() {
    stop_->cancel();
    if (worker_.joinable()) worker_.join();
    if (plan_) ggml_backend_graph_plan_free(backend_, plan_);
    ggml_backend_cpu_set_abort_callback(backend_, nullptr, nullptr);
    ggml_backend_free(backend_);
}
void MusicCpuExecution::cache(ggml_cgraph *graph) {
    if (worker_.joinable()) throw std::logic_error("join before caching a graph");
    if (!graph) throw std::invalid_argument("missing CPU graph");
    auto replacement = ggml_backend_graph_plan_create(backend_, graph);
    if (!replacement) throw std::runtime_error("CPU graph plan allocation failed");
    if (plan_) ggml_backend_graph_plan_free(backend_, plan_);
    plan_ = replacement;
    cached_graph_ = graph;
}
void MusicCpuExecution::start(ggml_cgraph *graph, bool cached) {
    if (worker_.joinable()) throw std::logic_error("join before starting another CPU graph");
    if (!graph || (cached && (!plan_ || cached_graph_ != graph)))
        throw std::invalid_argument("missing or mismatched cached CPU graph");
    status_ = GGML_STATUS_FAILED;
    worker_ = std::thread([this, graph, cached] {
        // ggml CPU polls after an operation/group, not before the first one or
        // inside a kernel. Avoid any work for already-cancelled requests here.
        status_ = stop_->cancelled() ? GGML_STATUS_ABORTED :
            (cached ? ggml_backend_graph_plan_compute(backend_, plan_) :
                      ggml_backend_graph_compute(backend_, graph));
    });
}
ggml_status MusicCpuExecution::join() {
    if (!worker_.joinable()) throw std::logic_error("no CPU execution to join");
    worker_.join();
    return status_;
}
} // namespace audiocpp_backend
