#pragma once

// Serializes runs on one loaded model and bounds how long a caller waits.
//
// A single audio.cpp model runs one request at a time; its sessions are not
// reentrant. If an inference wedges the GPU, a CUDA call that never returns
// cannot be cancelled from userspace, so later requests would block forever
// and every gRPC worker thread would pile up behind it. With a positive
// timeout a caller instead gives up and throws BusyError, which grpc-server.cpp
// maps to UNAVAILABLE so the client can retry or fail over. A timeout of 0
// restores unbounded std::mutex behaviour.
//
// This mirrors the behaviour upstream documents in app/server/busy_guard.h.
// That header is deliberately not copied: it is outside the public include
// path, and it is Apache-2.0 while this repository is MIT.

#include <atomic>
#include <cstdint>
#include <mutex>
#include <stdexcept>
#include <string>

namespace audiocpp_backend {

class BusyError : public std::runtime_error {
public:
    using std::runtime_error::runtime_error;
};

std::int64_t steady_now_ms();

// True when a caller should fail fast instead of queueing: the current holder
// acquired the guard at busy_since_ms and has already run longer than
// timeout_ms. busy_since_ms == 0 means idle; timeout_ms <= 0 disables the guard.
bool run_has_overrun(std::int64_t busy_since_ms, std::int64_t now_ms,
                     int timeout_ms);

// Resolves the bound for one run. `ceiling` is the model's configured policy;
// `requested` is a per-request override where a value <= 0 means unset. A
// request may ask for a shorter bound than policy but never a longer one, so a
// client cannot weaken the guard. A ceiling of 0 means unbounded and therefore
// compares as infinity.
int resolve_busy_timeout_ms(int ceiling, int requested);

class RunGuard {
public:
    // Holds the guard for the duration of a run and marks it idle again on
    // release, including when the run throws, so the fail-fast check never
    // sees a stale timestamp. Move only.
    class Lock {
    public:
        Lock() = default;
        Lock(RunGuard &guard, std::unique_lock<std::timed_mutex> lock);
        Lock(Lock &&) = default;
        Lock &operator=(Lock &&) = default;
        Lock(const Lock &) = delete;
        Lock &operator=(const Lock &) = delete;
        ~Lock();

    private:
        RunGuard *guard_ = nullptr;
        std::unique_lock<std::timed_mutex> lock_;
    };

    // `label` identifies the model in the error message. Throws BusyError when
    // timeout_ms is positive and either the current holder has already overrun
    // (fail fast, no wait) or the wait itself times out.
    Lock acquire(int timeout_ms, const std::string &label);

private:
    friend class Lock;
    std::timed_mutex mutex_;
    std::atomic<std::int64_t> busy_since_ms_{0};
};

} // namespace audiocpp_backend
