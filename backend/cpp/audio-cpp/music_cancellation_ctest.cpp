// SPDX-License-Identifier: MIT
#include "music_cancellation.h"
#include <ggml.h>
#include <ggml-cpu.h>
#include <chrono>
#include <condition_variable>
#include <cstdio>
#include <cstdlib>
#include <mutex>
#include <thread>
#include <vector>

using namespace audiocpp_backend;
static void require(bool ok, const char *message) {
    if (!ok) { std::fprintf(stderr, "FAIL: %s\n", message); std::abort(); }
}

// A real custom CPU operation provides deterministic progress rendezvous, not
// a fake worker. Stock square nodes after it must remain untouched on abort.
struct Progress {
    std::mutex mutex;
    std::condition_variable cv;
    bool reached = false;
    bool proceed = false;
    int completed = 0;
    MusicStopHandle destructor_stop;
    InferenceLane *lane = nullptr;
    bool held_during_destruction = false;
};
static void observe(ggml_tensor *dst, const ggml_tensor *src, int ith, int,
                    void *data) {
    require(ith == 0, "single-task observer");
    auto &p = *static_cast<Progress *>(data);
    auto *out = static_cast<float *>(dst->data);
    const auto *in = static_cast<const float *>(src->data);
    for (int i = 0; i < 32; ++i) out[i] = in[i] + 1.0f;
    if (p.destructor_stop) {
        {
            std::lock_guard<std::mutex> lock(p.mutex);
            p.reached = true;
            p.cv.notify_all();
        }
        const auto deadline = std::chrono::steady_clock::now() + std::chrono::seconds(10);
        while (!p.destructor_stop->cancelled() && std::chrono::steady_clock::now() < deadline)
            std::this_thread::yield();
        require(p.destructor_stop->cancelled(), "destructor requests stop");
        try { LaneEntry second(*p.lane, LaneEntry::TryNow{}); }
        catch (const LaneUnavailable &) { p.held_during_destruction = true; }
        ++p.completed;
        return;
    }
    std::unique_lock<std::mutex> lock(p.mutex);
    ++p.completed;
    p.reached = true;
    p.cv.notify_all();
    require(p.cv.wait_for(lock, std::chrono::seconds(10), [&] { return p.proceed; }),
            "observer released without sleep readiness");
}
static bool busy(InferenceLane &lane) {
    try { LaneEntry second(lane, LaneEntry::TryNow{}); return false; }
    catch (const LaneUnavailable &) { return true; }
}
static void exercise(bool cached, bool precancel) {
    InferenceLane lane("model-free-cpu");
    Progress progress;
    ggml_init_params params{1024 * 1024, nullptr, false};
    auto *ctx = ggml_init(params);
    require(ctx != nullptr, "bounded graph context");
    auto *input = ggml_new_tensor_1d(ctx, GGML_TYPE_F32, 32);
    ggml_set_f32(input, 1.0f);
    auto *first = ggml_map_custom1(ctx, input, observe, 1, &progress);
    std::vector<ggml_tensor *> tail;
    auto *node = first;
    for (int i = 0; i < 8; ++i) {
        node = ggml_sqr(ctx, node);
        ggml_set_f32(node, -123.0f);
        tail.push_back(node);
    }
    auto *graph = ggml_new_graph(ctx);
    ggml_build_forward_expand(graph, node);
    MusicStopHandle retained;
    {
        MusicCpuExecution run(lane);
        retained = run.stop_handle();
        if (cached) run.cache(graph);
        if (precancel) { retained->cancel(); retained->cancel(); progress.proceed = true; }
        ggml_status status = GGML_STATUS_FAILED;
        run.start(graph, cached);
        if (!precancel) {
            std::unique_lock<std::mutex> lock(progress.mutex);
            require(progress.cv.wait_for(lock, std::chrono::seconds(10), [&] { return progress.reached; }),
                    "actual operation made progress");
            retained->cancel(); retained->cancel();
            std::weak_ptr<MusicStopState> callback_lifetime = retained;
            retained.reset();
            require(!callback_lifetime.expired(), "callback state survives external handle release before CPU poll");
            require(busy(lane), "cancel does not release lane while operation stopping");
            progress.proceed = true;
            progress.cv.notify_all();
        }
        status = run.join();
        require(status == GGML_STATUS_ABORTED, "real CPU graph reports aborted");
        require(progress.completed == (precancel ? 0 : 1), "pre-cancel skips graph; in-flight cancel follows actual operation");
        for (auto *t : tail) require(ggml_get_f32_1d(t, 0) == -123.0f, "graph truncated before stock CPU tail nodes");
        require(busy(lane), "joined computation still owned until RAII settlement");
        // A cached plan captures callback data at creation. Drop the external
        // handle and invoke the same plan again: run must retain stable state.
        std::weak_ptr<MusicStopState> lifetime = run.stop_handle();
        retained.reset();
        require(!lifetime.expired(), "plan owner retains callback state");
        run.start(graph, cached);
        require(run.join() == GGML_STATUS_ABORTED, "cached callback state survives external handle release");
    }
    {
        MusicCpuExecution next(lane);
        if (cached) next.cache(graph);
        next.start(graph, cached);
        require(next.join() == GGML_STATUS_SUCCESS, "fresh uncancelled run succeeds");
        require(ggml_get_f32_1d(tail.front(), 0) == 4.0f, "next run executes stock operation");
        require(progress.completed == (precancel ? 1 : 2), "next run has independent stop state");
    }
    ggml_free(ctx);
}
static void destructor_joins() {
    InferenceLane lane("destructor-join");
    Progress p;
    p.lane = &lane;
    auto *ctx = ggml_init({1024 * 1024, nullptr, false});
    require(ctx != nullptr, "destructor graph context");
    auto *input = ggml_new_tensor_1d(ctx, GGML_TYPE_F32, 32);
    ggml_set_f32(input, 1.0f);
    auto *node = ggml_map_custom1(ctx, input, observe, 1, &p);
    auto *graph = ggml_new_graph(ctx);
    ggml_build_forward_expand(graph, node);
    {
        MusicCpuExecution run(lane);
        p.destructor_stop = run.stop_handle();
        run.cache(graph);
        // Ensure destruction cancels a graph already executing, not a worker
        // that has not entered compute. The op waits for that cancellation.
        run.start(graph, true);
        std::unique_lock<std::mutex> lock(p.mutex);
        require(p.cv.wait_for(lock, std::chrono::seconds(10), [&] { return p.reached; }),
                "destructor waits until actual CPU operation entered");
    }
    require(p.destructor_stop->cancelled(), "RAII requests cancellation");
    require(p.completed == 1 && p.held_during_destruction,
            "destructor joins actual operation before releasing lane");
    LaneEntry next(lane, LaneEntry::TryNow{});
    ggml_free(ctx);
}

#ifdef MUSIC_CPU_TEST_FAULTS
static void worker_faults() {
    InferenceLane lane("worker-fault");
    auto *ctx = ggml_init({1024 * 1024, nullptr, false});
    require(ctx != nullptr, "fault graph context");
    auto *input = ggml_new_tensor_1d(ctx, GGML_TYPE_F32, 32);
    ggml_set_f32(input, 2.0f);
    auto *node = ggml_sqr(ctx, input);
    auto *graph = ggml_new_graph(ctx);
    ggml_build_forward_expand(graph, node);
    for (bool cached : {false, true}) {
        for (auto fault : {MusicCpuExecution::Fault::Allocation, MusicCpuExecution::Fault::Generic}) {
            {
                MusicCpuExecution run(lane, fault);
                if (cached) run.cache(graph);
                run.start(graph, cached);
                require(busy(lane), "faulting worker retains lane before join");
                require(run.join() == (fault == MusicCpuExecution::Fault::Allocation ?
                        GGML_STATUS_ALLOC_FAILED : GGML_STATUS_FAILED), "worker exception becomes joined status");
                require(busy(lane), "fault status does not release owner lane");
                // Also exercise destruction of an unjoined, faulting worker.
                run.start(graph, cached);
            }
            require(!busy(lane), "faulting worker destroyed before lane release");
            MusicCpuExecution next(lane);
            if (cached) next.cache(graph);
            next.start(graph, cached);
            require(next.join() == GGML_STATUS_SUCCESS, "fresh owner computes after worker fault");
            require(ggml_get_f32_1d(node, 0) == 4.0f, "fresh owner uses real CPU");
        }
    }
    ggml_free(ctx);
}
#endif
int main() {
#ifdef MUSIC_CPU_TEST_FAULTS
    worker_faults();
#endif
    destructor_joins();
    for (bool cached : {false, true})
        for (bool pre : {false, true}) exercise(cached, pre);
    std::puts("music_cancellation: real CPU ordinary/cached truncation and ownership passed");
}
