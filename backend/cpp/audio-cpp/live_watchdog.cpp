#include "live_watchdog.h"

#include <utility>

namespace audiocpp_backend {

IdleWatchdog::IdleWatchdog(std::chrono::milliseconds window,
                           std::function<void()> on_idle)
    : window_(window), on_idle_(std::move(on_idle)),
      last_(std::chrono::steady_clock::now()) {
    if (window_.count() <= 0) {
        // Disabled: no thread at all, rather than a thread with an infinite
        // deadline. A thread that exists is a thread that has to be joined on
        // every exit path, and there is nothing for this one to do.
        return;
    }
    thread_ = std::thread([this] { run(); });
}

IdleWatchdog::~IdleWatchdog() { disarm(); }

void IdleWatchdog::touch() {
    std::lock_guard<std::mutex> lock(mu_);
    last_ = std::chrono::steady_clock::now();
    // Deliberately does NOT notify. The waiter recomputes its deadline from
    // last_ every time it wakes, so a touch that lands mid-window is picked up
    // when the old deadline expires, and a touch is the hot path: it runs once
    // per frame on the wire.
}

void IdleWatchdog::disarm() {
    {
        std::lock_guard<std::mutex> lock(mu_);
        stop_ = true;
    }
    cv_.notify_all();
    if (thread_.joinable()) {
        thread_.join();
    }
}

bool IdleWatchdog::fired() const {
    std::lock_guard<std::mutex> lock(mu_);
    return fired_;
}

void IdleWatchdog::run() {
    std::unique_lock<std::mutex> lock(mu_);
    while (!stop_) {
        const auto deadline = last_ + window_;
        if (cv_.wait_until(lock, deadline, [this] { return stop_; })) {
            return; // disarmed
        }
        // The deadline passed, but last_ may have moved while this thread was
        // waiting, and a condition variable may also wake spuriously. Re-read
        // it: without this check a touch that landed mid-window would still be
        // followed by a cancellation, i.e. a live client cut off mid-sentence.
        if (std::chrono::steady_clock::now() < last_ + window_) {
            continue;
        }
        fired_ = true;
        auto callback = on_idle_;
        lock.unlock();
        if (callback) {
            callback();
        }
        return; // one shot
    }
}

bool live_frame_carries_audio(bool has_audio, bool pcm_empty) {
    return has_audio && !pcm_empty;
}

} // namespace audiocpp_backend
