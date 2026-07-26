#include "run_guard.h"

#include <chrono>

namespace audiocpp_backend {

std::int64_t steady_now_ms() {
    return std::chrono::duration_cast<std::chrono::milliseconds>(
               std::chrono::steady_clock::now().time_since_epoch())
        .count();
}

bool run_has_overrun(std::int64_t busy_since_ms, std::int64_t now_ms,
                     int timeout_ms) {
    if (timeout_ms <= 0 || busy_since_ms == 0) {
        return false;
    }
    return (now_ms - busy_since_ms) > timeout_ms;
}

int resolve_busy_timeout_ms(int ceiling, int requested) {
    if (requested <= 0) {
        return ceiling;
    }
    if (ceiling <= 0) {
        return requested;
    }
    return requested < ceiling ? requested : ceiling;
}

RunGuard::Lock::Lock(RunGuard &guard, std::unique_lock<std::timed_mutex> lock)
    : guard_(&guard), lock_(std::move(lock)) {}

RunGuard::Lock::~Lock() {
    if (guard_ != nullptr && lock_.owns_lock()) {
        guard_->busy_since_ms_.store(0, std::memory_order_release);
    }
}

RunGuard::Lock RunGuard::acquire(int timeout_ms, const std::string &label) {
    std::unique_lock<std::timed_mutex> lock(mutex_, std::defer_lock);
    if (timeout_ms <= 0) {
        lock.lock(); // guard disabled: unbounded wait
    } else {
        // If the current holder has already run past the bound, treat it as
        // wedged and fail fast rather than queue behind it. This is what stops
        // requests piling up on a stuck GPU. Nothing is stored here: only the
        // thread that actually takes the lock stamps the clock, so a rejected
        // caller cannot hide the wedge from the callers after it.
        const auto since = busy_since_ms_.load(std::memory_order_acquire);
        const auto now_ms = steady_now_ms();
        if (run_has_overrun(since, now_ms, timeout_ms)) {
            throw BusyError("audio-cpp: model '" + label +
                            "' is busy: the current inference has run " +
                            std::to_string(now_ms - since) +
                            " ms (busy_timeout_ms=" + std::to_string(timeout_ms) +
                            "); it has likely wedged and cannot be cancelled");
        }
        if (!lock.try_lock_for(std::chrono::milliseconds(timeout_ms))) {
            throw BusyError("audio-cpp: model '" + label +
                            "' is busy: timed out after " +
                            std::to_string(timeout_ms) +
                            " ms waiting for the inference lock");
        }
    }
    busy_since_ms_.store(steady_now_ms(), std::memory_order_release);
    return Lock(*this, std::move(lock));
}

} // namespace audiocpp_backend
