// SPDX-License-Identifier: MIT
#pragma once
#include "inference_lane.h"
#include <ggml-backend.h>
#include <atomic>
#include <memory>
#include <thread>

namespace audiocpp_backend {
// A one-way request, not a quiescence or success/publication acknowledgement.
class MusicStopState final {
  public:
    void cancel() noexcept { stopped_.store(true, std::memory_order_release); }
    bool cancelled() const noexcept { return stopped_.load(std::memory_order_acquire); }
  private:
    std::atomic<bool> stopped_{false};
};
using MusicStopHandle = std::shared_ptr<MusicStopState>;

// CPU primitive only: no model/session, host-loop, DSP or GPU propagation.
// Owner-thread API except stop_handle()->cancel(), which is thread safe.
// Graph, tensor storage and custom-op data must outlive this object. Cached
// plans retain graph metadata; never mutate it while cached.
// Plans cannot escape this owner. Their captured callback points to the
// same heap stop state for their entire lifetime, including between executions.
class MusicCpuExecution final {
  public:
#ifdef MUSIC_CPU_TEST_FAULTS
    // Test-only, per-owner injection; absent from production builds.
    enum class Fault { None, Allocation, Generic };
    explicit MusicCpuExecution(InferenceLane &lane, Fault fault = Fault::None);
#else
    explicit MusicCpuExecution(InferenceLane &lane);
#endif
    ~MusicCpuExecution() noexcept; // cancel, join, free plans, then release lane
    MusicCpuExecution(const MusicCpuExecution &) = delete;
    MusicCpuExecution &operator=(const MusicCpuExecution &) = delete;
    MusicStopHandle stop_handle() const noexcept { return stop_; }
    void cache(ggml_cgraph *graph);
    void start(ggml_cgraph *graph, bool cached);
    ggml_status join(); // only acknowledgement that the worker has quiesced
  private:
    static bool abort_requested(void *data) noexcept;
    LaneEntry lane_; // destroyed last, even when construction throws
    MusicStopHandle stop_;
    struct CpuPlan;
    std::unique_ptr<CpuPlan> plan_;
    ggml_cgraph *cached_graph_ = nullptr;
#ifdef MUSIC_CPU_TEST_FAULTS
    Fault fault_ = Fault::None;
#endif
    std::thread worker_;
    ggml_status status_ = GGML_STATUS_FAILED;
};
} // namespace audiocpp_backend
