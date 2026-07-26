// Unit tests for run_guard. Standard library only. The harness compiles this
// as a single translation unit, so the implementation is included directly.
//
// Every test that can block waits through AsyncAcquire, which bounds the wait
// and reports a named failure. A guard that never returns must fail this suite,
// not hang it: a hung test wedges CI far longer than a red one.

#include "run_guard.cpp"

#include <atomic>
#include <chrono>
#include <cstdio>
#include <future>
#include <string>
#include <thread>
#include <vector>

static int failures = 0;

static void check(bool ok, const std::string &name) {
    if (!ok) {
        failures++;
        fprintf(stderr, "FAIL: %s\n", name.c_str());
    } else {
        fprintf(stderr, "ok:   %s\n", name.c_str());
    }
}

using namespace audiocpp_backend;

namespace {

struct Outcome {
    bool threw = false;
    std::string message;
};

// Runs one acquire on a worker thread so that a guard which waits forever fails
// a named check instead of hanging the suite. The worker is joined by the
// future's destructor, so every test must first unblock whatever holds the
// guard before this object goes out of scope.
class AsyncAcquire {
public:
    AsyncAcquire(RunGuard &guard, int timeout_ms, const std::string &label)
        : future_(std::async(std::launch::async, [&guard, timeout_ms, label] {
              Outcome out;
              try {
                  auto lock = guard.acquire(timeout_ms, label);
                  (void)lock;
              } catch (const BusyError &err) {
                  out.threw = true;
                  out.message = err.what();
              }
              return out;
          })) {}

    // True when the acquire returned within watchdog_ms.
    bool returned_within(int watchdog_ms) {
        return future_.wait_for(std::chrono::milliseconds(watchdog_ms)) ==
               std::future_status::ready;
    }

    // Only valid once returned_within() reported true, or after the holder has
    // been released; otherwise it blocks.
    Outcome outcome() { return future_.get(); }

private:
    std::future<Outcome> future_;
};

// A run held open on its own thread until release() is called, so the main
// thread can observe a guard that is genuinely busy.
class HeldRun {
public:
    explicit HeldRun(RunGuard &guard) {
        thread_ = std::thread([this, &guard] {
            auto lock = guard.acquire(0, "holder");
            held_.store(true);
            while (!release_.load()) {
                std::this_thread::sleep_for(std::chrono::milliseconds(1));
            }
        });
        while (!held_.load()) {
            std::this_thread::sleep_for(std::chrono::milliseconds(1));
        }
    }

    void release() {
        release_.store(true);
        if (thread_.joinable()) {
            thread_.join();
        }
    }

    ~HeldRun() { release(); }

private:
    std::thread thread_;
    std::atomic<bool> held_{false};
    std::atomic<bool> release_{false};
};

const int kWatchdogMs = 5000;

} // namespace

static void test_steady_clock_advances() {
    const std::int64_t before = steady_now_ms();
    std::this_thread::sleep_for(std::chrono::milliseconds(30));
    const std::int64_t after = steady_now_ms();
    check(after >= before + 10, "steady_now_ms advances with real time");
}

static void test_overrun_predicate() {
    // busy_since_ms == 0 means idle: never an overrun.
    check(!run_has_overrun(0, 100000, 1000), "idle is never an overrun");
    // timeout_ms <= 0 disables the guard.
    check(!run_has_overrun(1000, 100000, 0), "zero timeout disables the check");
    check(!run_has_overrun(1000, 100000, -5), "negative timeout disables the check");
    // Held for 500ms under a 1000ms bound: fine.
    check(!run_has_overrun(1000, 1500, 1000), "within the bound is not an overrun");
    // Exactly at the bound is not yet an overrun.
    check(!run_has_overrun(1000, 2000, 1000), "exactly at the bound is not an overrun");
    // One millisecond past the bound is.
    check(run_has_overrun(1000, 2001, 1000), "past the bound is an overrun");
    check(run_has_overrun(1000, 100000, 1000), "long past the bound is an overrun");
}

static void test_timeout_resolution() {
    // No per-request value: policy applies.
    check(resolve_busy_timeout_ms(5000, 0) == 5000, "unset request uses the ceiling");
    check(resolve_busy_timeout_ms(5000, -1) == 5000, "negative request uses the ceiling");
    // A request may ask for a shorter bound than policy.
    check(resolve_busy_timeout_ms(5000, 1000) == 1000, "shorter request is honoured");
    // A request may never ask for a longer bound than policy.
    check(resolve_busy_timeout_ms(1000, 5000) == 1000, "longer request is capped");
    // Equal on both sides is that value, not something else.
    check(resolve_busy_timeout_ms(1000, 1000) == 1000, "an equal request is that value");
    // Unbounded policy honours whatever the request asks for.
    check(resolve_busy_timeout_ms(0, 2000) == 2000, "unbounded policy honours the request");
    check(resolve_busy_timeout_ms(0, 0) == 0, "unbounded on both sides stays unbounded");
}

static void test_uncontended_acquire() {
    RunGuard guard;
    bool threw = false;
    try {
        auto lock = guard.acquire(1000, "model");
        (void)lock;
    } catch (const BusyError &) {
        threw = true;
    }
    check(!threw, "an uncontended acquire succeeds");
}

static void test_release_clears_the_busy_timestamp() {
    RunGuard guard;
    {
        auto lock = guard.acquire(1000, "model");
        (void)lock;
    }
    // Sleep well past the bound the next caller will use. Releasing must mark
    // the guard idle again: a stale timestamp would make this caller fail fast
    // against a model that has not been running anything for 200ms.
    std::this_thread::sleep_for(std::chrono::milliseconds(200));

    bool threw = false;
    std::string message;
    try {
        auto lock = guard.acquire(20, "model");
        (void)lock;
    } catch (const BusyError &err) {
        threw = true;
        message = err.what();
    }
    check(!threw, "release clears the busy timestamp");
    if (threw) {
        fprintf(stderr, "      unexpected BusyError: %s\n", message.c_str());
    }
}

static void test_exception_in_run_still_releases() {
    RunGuard guard;
    try {
        auto lock = guard.acquire(1000, "model");
        (void)lock;
        throw std::runtime_error("inference blew up");
    } catch (const std::runtime_error &) {
        // swallowed
    }
    // Same reasoning as above: the unwind must clear the timestamp, not just
    // drop the mutex, or the next caller fails fast against an idle model.
    std::this_thread::sleep_for(std::chrono::milliseconds(200));

    bool threw = false;
    try {
        auto lock = guard.acquire(20, "model");
        (void)lock;
    } catch (const BusyError &) {
        threw = true;
    }
    check(!threw, "a throwing run still releases the guard");
}

static void test_contended_acquire_times_out() {
    RunGuard guard;
    HeldRun holder(guard);

    // 300ms is generous on purpose: the waiter must still be inside its bound
    // when it looks, so that this exercises the wait path and not the fail-fast
    // path. Only a 300ms stall between the holder acquiring and the waiter
    // checking would confuse the two.
    AsyncAcquire waiter(guard, 300, "my-model");
    const bool returned = waiter.returned_within(kWatchdogMs);
    check(returned, "a contended acquire returns rather than waiting forever");

    if (returned) {
        const Outcome out = waiter.outcome();
        check(out.threw, "a contended acquire past its timeout throws BusyError");
        check(out.message.find("my-model") != std::string::npos,
              "the error names the model");
        check(out.message.find("timed out after") != std::string::npos,
              "waiting out the bound reports the wait, not a wedge");
    }

    holder.release();

    // After the holder releases, the guard is usable again.
    bool after_threw = false;
    try {
        auto lock = guard.acquire(1000, "my-model");
        (void)lock;
    } catch (const BusyError &) {
        after_threw = true;
    }
    check(!after_threw, "the guard is reusable once released");
}

static void test_fails_fast_when_the_holder_has_overrun() {
    RunGuard guard;
    HeldRun holder(guard);

    // Let the holder run past every bound the callers below use, so each of
    // them must fail fast on the holder's own start time instead of queueing.
    std::this_thread::sleep_for(std::chrono::milliseconds(250));

    AsyncAcquire first(guard, 40, "wedged-model");
    const bool first_returned = first.returned_within(kWatchdogMs);
    check(first_returned, "a caller meeting a wedged run returns");
    if (first_returned) {
        const Outcome out = first.outcome();
        check(out.threw, "a caller meeting a wedged run throws BusyError");
        check(out.message.find("has run") != std::string::npos,
              "the error reports the wedged run, not a plain wait timeout");
        check(out.message.find("wedged-model") != std::string::npos,
              "the fail-fast error names the model");
    }

    // A second caller must see the same thing. A waiter that stamps its own
    // arrival time over the holder's would hide the wedge from everyone after
    // it, which is exactly the pile-up this guard exists to prevent.
    AsyncAcquire second(guard, 40, "wedged-model");
    const bool second_returned = second.returned_within(kWatchdogMs);
    check(second_returned, "a second caller meeting a wedged run returns");
    if (second_returned) {
        const Outcome out = second.outcome();
        check(out.threw, "a second caller meeting a wedged run throws BusyError");
        check(out.message.find("has run") != std::string::npos,
              "a rejected waiter does not reset the wedge clock");
    }

    holder.release();
}

// A caller that queues behind a healthy run must not stamp its own arrival over
// the holder's start time. If it did, the wedge clock would restart on every
// arrival and a run that never finishes would look young forever to everyone
// who arrives after: the pile-up this guard exists to prevent, back again.
static void test_a_queued_waiter_does_not_reset_the_wedge_clock() {
    RunGuard guard;
    HeldRun holder(guard);

    // 300ms in, a caller with a 5s bound is nowhere near its own limit, so it
    // queues rather than failing fast. That is the caller that could clobber
    // the clock.
    std::this_thread::sleep_for(std::chrono::milliseconds(300));
    AsyncAcquire queued(guard, 5000, "patient-model");
    std::this_thread::sleep_for(std::chrono::milliseconds(100));

    // 400ms in, this caller's own 250ms bound is already exceeded by the
    // holder, so it must fail fast. It only can if the clock still reads the
    // holder's start: 400ms of holding, not the 100ms since the queued caller
    // arrived.
    AsyncAcquire impatient(guard, 250, "wedged-model");
    const bool returned = impatient.returned_within(kWatchdogMs);
    check(returned, "a caller arriving behind a queued waiter returns");
    if (returned) {
        const Outcome out = impatient.outcome();
        check(out.threw, "a caller arriving behind a queued waiter throws BusyError");
        check(out.message.find("has run") != std::string::npos,
              "a queued waiter does not reset the wedge clock");
    }

    holder.release();

    const bool queued_returned = queued.returned_within(kWatchdogMs);
    check(queued_returned, "the queued waiter returns once the holder releases");
    if (queued_returned) {
        check(!queued.outcome().threw, "the queued waiter gets its turn");
    }
}

static void test_runs_are_serialized() {
    RunGuard guard;
    std::atomic<int> concurrent{0};
    std::atomic<int> peak{0};
    std::atomic<bool> unexpected_busy{false};

    std::vector<std::thread> threads;
    for (int t = 0; t < 4; ++t) {
        threads.emplace_back([&] {
            for (int i = 0; i < 40; ++i) {
                try {
                    auto lock = guard.acquire(0, "model");
                    const int inside = ++concurrent;
                    int seen = peak.load();
                    while (inside > seen &&
                           !peak.compare_exchange_weak(seen, inside)) {
                    }
                    std::this_thread::sleep_for(std::chrono::microseconds(20));
                    --concurrent;
                } catch (const BusyError &) {
                    unexpected_busy.store(true);
                    return;
                }
            }
        });
    }
    for (auto &thread : threads) {
        thread.join();
    }

    check(!unexpected_busy.load(), "an unbounded acquire never gives up");
    check(peak.load() == 1, "only one run holds the guard at a time");
}

int main() {
    test_steady_clock_advances();
    test_overrun_predicate();
    test_timeout_resolution();
    test_uncontended_acquire();
    test_release_clears_the_busy_timestamp();
    test_exception_in_run_still_releases();
    test_contended_acquire_times_out();
    test_fails_fast_when_the_holder_has_overrun();
    test_a_queued_waiter_does_not_reset_the_wedge_clock();
    test_runs_are_serialized();
    if (failures) {
        fprintf(stderr, "%d check(s) failed\n", failures);
        return 1;
    }
    fprintf(stderr, "all run_guard checks passed\n");
    return 0;
}
