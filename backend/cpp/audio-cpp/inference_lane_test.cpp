// Unit tests for inference_lane. Standard library only. The harness compiles
// this file as a single translation unit, so the implementation is included
// directly rather than linked.
//
// Two kinds of test live here:
//
//   * The pure ones (budget negotiation, the overrun predicate) run with no
//     threads and no sleeping. They carry the arithmetic, so they are the tests
//     that must be exhaustive.
//   * The threaded ones exercise the lane itself. Every one of them is bounded:
//     contenders use a generous wait budget instead of the unbounded mode
//     wherever the point of the test does not require unbounded, and a watchdog
//     in main() puts a ceiling on the whole file. A broken implementation must
//     go red, not hang, because a hung job costs CI far more than a red one.
//
// Wall-clock margins are called out individually. The rule applied throughout:
// a margin is only allowed if a slow or loaded machine pushes the measurement
// deeper into the passing region.

#include "inference_lane.cpp"

#include <atomic>
#include <chrono>
#include <condition_variable>
#include <cstdio>
#include <cstdlib>
#include <mutex>
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

using audiocpp_backend::InferenceLane;
using audiocpp_backend::LaneEntry;
using audiocpp_backend::LaneUnavailable;
using audiocpp_backend::resolve_wait_budget_ms;
using audiocpp_backend::run_exceeds_budget;

// A one-shot, level-triggered signal with a bounded wait. Preferred over sleeps
// for "the other thread got there" so the tests do not encode a guess about
// scheduling.
class Signal {
  public:
    void raise() {
        {
            std::lock_guard<std::mutex> lock(mutex_);
            raised_ = true;
        }
        cv_.notify_all();
    }

    bool await(int timeout_ms) {
        std::unique_lock<std::mutex> lock(mutex_);
        return cv_.wait_for(lock, std::chrono::milliseconds(timeout_ms),
                            [this] { return raised_; });
    }

  private:
    std::mutex mutex_;
    std::condition_variable cv_;
    bool raised_ = false;
};

static std::int64_t elapsed_ms_since(
    const std::chrono::steady_clock::time_point &start) {
    return std::chrono::duration_cast<std::chrono::milliseconds>(
               std::chrono::steady_clock::now() - start)
        .count();
}

static void nap(int ms) {
    std::this_thread::sleep_for(std::chrono::milliseconds(ms));
}

static bool mentions(const std::string &haystack, const std::string &needle) {
    return haystack.find(needle) != std::string::npos;
}

// Wording that separates the two failure modes. Kept here so a message reword
// that erases the distinction breaks these tests loudly.
static const char *const kTimedOutPhrase = "timed out after";
static const char *const kStillRunningPhrase = "has been running for";

// Attempts an entry and reports what happened, so the threaded tests can assert
// on the message rather than only on the exception type.
struct EntryOutcome {
    bool acquired = false;
    std::string message;
    std::int64_t took_ms = 0;
};

static EntryOutcome try_entry(InferenceLane &lane, int budget_ms) {
    EntryOutcome out;
    const auto start = std::chrono::steady_clock::now();
    try {
        LaneEntry entry(lane, budget_ms);
        out.acquired = true;
    } catch (const LaneUnavailable &refused) {
        out.message = refused.what();
    }
    out.took_ms = elapsed_ms_since(start);
    return out;
}

// ---------------------------------------------------------------------------
// B9: budget negotiation. Pure, no threads.
// ---------------------------------------------------------------------------
static void test_budget_negotiation() {
    check(resolve_wait_budget_ms(0, 0) == 0,
          "B9 no ceiling and no request hint is unbounded");
    check(resolve_wait_budget_ms(-1, -1) == 0,
          "B9 negative ceiling and negative hint is unbounded");

    check(resolve_wait_budget_ms(5000, 0) == 5000,
          "B9 absent hint yields the ceiling");
    check(resolve_wait_budget_ms(5000, -250) == 5000,
          "B9 negative hint yields the ceiling");

    check(resolve_wait_budget_ms(5000, 1200) == 1200,
          "B9 a shorter request hint is granted");
    check(resolve_wait_budget_ms(5000, 1) == 1,
          "B9 a much shorter request hint is granted");

    check(resolve_wait_budget_ms(5000, 9000) == 5000,
          "B9 a longer request hint cannot weaken the ceiling");
    check(resolve_wait_budget_ms(5000, 5001) == 5000,
          "B9 a hint one ms over the ceiling is clamped");
    check(resolve_wait_budget_ms(5000, 5000) == 5000,
          "B9 a hint equal to the ceiling is the ceiling");

    check(resolve_wait_budget_ms(0, 1200) == 1200,
          "B9 without a policy limit the request hint applies");
    check(resolve_wait_budget_ms(-5, 1200) == 1200,
          "B9 a negative ceiling is no policy limit");

    check(resolve_wait_budget_ms(1, 0) == 1,
          "B9 a one ms ceiling survives negotiation");
}

// ---------------------------------------------------------------------------
// B5 and B6: the overrun predicate. Pure, no threads.
// ---------------------------------------------------------------------------
static void test_overrun_predicate() {
    // B5: strictly longer.
    check(run_exceeds_budget(true, 1000, 1100, 100) == false,
          "B5 elapsed exactly equal to the budget is not an overrun");
    check(run_exceeds_budget(true, 1000, 1101, 100) == true,
          "B5 one ms past the budget is an overrun");
    check(run_exceeds_budget(true, 1000, 1099, 100) == false,
          "B5 one ms short of the budget is not an overrun");
    check(run_exceeds_budget(true, 0, 1, 1) == false,
          "B5 a one ms budget at one ms elapsed is not an overrun");
    check(run_exceeds_budget(true, 0, 2, 1) == true,
          "B5 a one ms budget at two ms elapsed is an overrun");

    // B6: an unoccupied lane is never stuck, whatever the leftover timestamp
    // says. This is the pure half of B6; the wiring half is threaded below.
    check(run_exceeds_budget(false, 0, 10000000, 1) == false,
          "B6 an idle lane with an ancient start timestamp is not an overrun");
    check(run_exceeds_budget(false, 5, 5, 5) == false,
          "B6 an idle lane is not an overrun at any elapsed value");

    // An unbounded caller has no budget to exceed, so it never fails fast.
    check(run_exceeds_budget(true, 0, 10000000, 0) == false,
          "B2 an unbounded caller never sees an overrun");
    check(run_exceeds_budget(true, 0, 10000000, -1) == false,
          "B2 a negative budget never sees an overrun");
}

// ---------------------------------------------------------------------------
// B1: mutual exclusion under real contention, and a release admits a waiter.
// ---------------------------------------------------------------------------
static void test_mutual_exclusion() {
    InferenceLane lane("exclusion-model");

    constexpr int kContenders = 4; // "at least three simultaneous contenders"
    constexpr int kHoldMs = 15;
    // Generous on purpose: the point of this test is exclusion, not timeouts.
    // A larger budget only makes a healthy run more likely to pass, while still
    // bounding a broken one at roughly five seconds instead of forever.
    constexpr int kBudgetMs = 5000;

    std::atomic<int> in_flight{0};
    std::atomic<int> peak_in_flight{0};
    std::atomic<int> completed{0};
    std::atomic<int> refused{0};

    Signal go;
    std::vector<std::thread> contenders;
    for (int i = 0; i < kContenders; i++) {
        contenders.emplace_back([&] {
            go.await(5000);
            try {
                LaneEntry entry(lane, kBudgetMs);
                const int now_inside = in_flight.fetch_add(1) + 1;
                int seen = peak_in_flight.load();
                while (now_inside > seen &&
                       !peak_in_flight.compare_exchange_weak(seen, now_inside)) {
                    // retry with the refreshed value
                }
                nap(kHoldMs);
                in_flight.fetch_sub(1);
                completed.fetch_add(1);
            } catch (const LaneUnavailable &) {
                refused.fetch_add(1);
            }
        });
    }
    go.raise();
    for (auto &t : contenders) {
        t.join();
    }

    check(refused.load() == 0, "B1 no contender was refused within its budget");
    check(completed.load() == kContenders,
          "B1 every contender eventually got the lane");
    check(peak_in_flight.load() == 1,
          "B1 never more than one holder inside the lane at once");
}

// ---------------------------------------------------------------------------
// B2: unbounded mode waits out a run longer than any bound would allow.
// ---------------------------------------------------------------------------
static void test_unbounded_waits_out_the_holder() {
    InferenceLane lane("patient-model");
    constexpr int kHoldMs = 300;

    Signal held;
    Signal patient_done;
    std::int64_t patient_wait_ms = -1;
    bool patient_acquired = false;

    std::thread holder([&] {
        LaneEntry entry(lane, 0);
        held.raise();
        nap(kHoldMs);
    });
    check(held.await(5000), "B2 holder took the lane");

    std::thread patient([&] {
        const auto start = std::chrono::steady_clock::now();
        try {
            LaneEntry entry(lane, 0);
            patient_acquired = true;
        } catch (const LaneUnavailable &) {
            patient_acquired = false;
        }
        patient_wait_ms = elapsed_ms_since(start);
        patient_done.raise();
    });

    // Bound on the unbounded mode: a lane that never wakes its waiters goes red
    // here instead of hanging in join(). The named failure is printed before the
    // join, so even a hard hang leaves a diagnosis behind for the watchdog.
    check(patient_done.await(10000), "B2 unbounded caller returned at all");
    patient.join();
    holder.join();

    check(patient_acquired, "B2 unbounded caller acquired instead of failing");
    // Margin: the holder holds for 300 ms, so the waiter must block for about
    // that long. Asserting only half of it means a loaded machine, which makes
    // the wait longer, drifts further into passing.
    check(patient_wait_ms >= kHoldMs / 2,
          "B2 unbounded caller actually waited for the in-flight run");
}

// ---------------------------------------------------------------------------
// B3: a bounded caller that cannot get in gives up with the timeout wording.
// ---------------------------------------------------------------------------
static void test_bounded_wait_times_out() {
    InferenceLane lane("impatient-model");
    constexpr int kBudgetMs = 120;

    Signal held;
    Signal release;
    std::thread holder([&] {
        LaneEntry entry(lane, 0);
        held.raise();
        release.await(10000);
    });
    check(held.await(5000), "B3 holder took the lane");

    // The waiter arrives immediately, so the holder's elapsed time is far below
    // the budget and the fail-fast path must not trigger here.
    const EntryOutcome outcome = try_entry(lane, kBudgetMs);
    release.raise();
    holder.join();

    check(!outcome.acquired, "B3 bounded caller did not acquire a held lane");
    check(mentions(outcome.message, kTimedOutPhrase),
          "B3 failure names the exhausted wait, not an overrunning run");
    check(!mentions(outcome.message, kStillRunningPhrase),
          "B3 failure is not worded as an overrun");
    check(mentions(outcome.message, "impatient-model"),
          "B3 failure names the model");
    check(mentions(outcome.message, "120"),
          "B3 failure reports the budget it waited out");
    // Margin: wait_for cannot return before its deadline, so the true value is
    // at least 120 ms and load only raises it. Asserting 100 leaves room for
    // clock granularity while still catching an implementation that returns
    // early without waiting.
    check(outcome.took_ms >= 100, "B3 bounded caller waited out its budget");
}

// ---------------------------------------------------------------------------
// B4: a caller whose budget is already exceeded fails at once.
// ---------------------------------------------------------------------------
static void test_fail_fast_against_a_long_run() {
    InferenceLane lane("wedged-model");
    constexpr int kBudgetMs = 200;
    constexpr int kRunAgeMs = 400;

    Signal held;
    Signal release;
    std::thread holder([&] {
        LaneEntry entry(lane, 0);
        held.raise();
        release.await(10000);
    });
    check(held.await(5000), "B4 holder took the lane");

    // Margin: the arriving caller needs the holder's elapsed time to exceed
    // 200 ms. Sleeping 400 ms means a loaded machine oversleeps and pushes the
    // elapsed time further past the budget, never below it.
    nap(kRunAgeMs);
    const EntryOutcome outcome = try_entry(lane, kBudgetMs);
    release.raise();
    holder.join();

    check(!outcome.acquired, "B4 caller did not acquire a long-running lane");
    check(mentions(outcome.message, kStillRunningPhrase),
          "B4 failure states the measured age of the in-flight run");
    check(!mentions(outcome.message, kTimedOutPhrase),
          "B4 failure is not worded as an exhausted wait");
    check(mentions(outcome.message, "wedged-model"),
          "B4 failure names the model");
    // Margin: a fail-fast return takes microseconds. 150 ms of headroom under a
    // 200 ms budget separates "returned at once" from "waited out the budget"
    // by a wide enough gap that scheduler noise cannot close it. The message
    // assertions above are the load-independent proof; this one pins the timing.
    check(outcome.took_ms < 150, "B4 caller failed without waiting out its budget");
}

// ---------------------------------------------------------------------------
// B6 wiring: an idle lane never looks stuck, however old the last run is.
// ---------------------------------------------------------------------------
static void test_idle_lane_is_never_stuck() {
    InferenceLane lane("idle-model");

    {
        LaneEntry entry(lane, 0);
    }
    // Ages the leftover start timestamp well past the tiny budget used below.
    // A longer sleep only makes a stale-timestamp bug more visible, so load
    // helps this test rather than hurting it.
    nap(80);

    const EntryOutcome first = try_entry(lane, 20);
    check(first.acquired, "B6 tiny budget still acquires an idle lane");

    nap(80);
    const EntryOutcome second = try_entry(lane, 1);
    check(second.acquired, "B6 a one ms budget still acquires an idle lane");
}

// ---------------------------------------------------------------------------
// B7: every ownership exit clears the busy state, including an exception
// thrown from inside the guarded region.
// ---------------------------------------------------------------------------
static void test_release_on_exception() {
    InferenceLane lane("throwing-model");

    struct GuardedRegionFailure {};
    bool propagated = false;
    try {
        LaneEntry entry(lane, 0);
        throw GuardedRegionFailure{};
    } catch (const GuardedRegionFailure &) {
        propagated = true;
    }
    check(propagated, "B7 the guarded region's own exception propagated");

    // If the throw had leaked the busy state, this tiny budget would fail.
    const EntryOutcome after_throw = try_entry(lane, 20);
    check(after_throw.acquired, "B7 lane is free after an exception unwound it");

    // Same check for a holder that unwinds on another thread, which is the shape
    // a gRPC handler failing mid-inference actually has.
    Signal thrown;
    std::thread unlucky([&] {
        try {
            LaneEntry entry(lane, 0);
            throw GuardedRegionFailure{};
        } catch (const GuardedRegionFailure &) {
            thrown.raise();
        }
    });
    check(thrown.await(5000), "B7 worker thread unwound its guarded region");
    unlucky.join();

    const EntryOutcome after_worker = try_entry(lane, 20);
    check(after_worker.acquired,
          "B7 lane is free after a worker thread unwound it");
}

// ---------------------------------------------------------------------------
// B8: a waiter must not publish itself as the holder. If it did, its arrival
// would restart the elapsed-time measurement and hide the real holder.
//
// Timeline, with the holder taking the lane at t0 and never letting go:
//
//   t0        holder acquires, elapsed measurement starts here and only here
//   t0+100    waiter arrives with a 400 ms budget, blocks, and times out
//   t0+500    late caller arrives with a 450 ms budget
//
// A correct lane measures 500 ms of holding at the late arrival, which is more
// than 450, so the late caller fails fast. An implementation that let the
// waiter stamp itself as holder measures only the 400 ms since the waiter
// arrived, which is under 450, so the late caller would queue behind a stuck
// run instead. The two paths are told apart by their wording.
// ---------------------------------------------------------------------------
static void test_waiter_does_not_become_the_holder() {
    InferenceLane lane("stamp-model");
    constexpr int kWaiterArrivesAfterMs = 100;
    constexpr int kWaiterBudgetMs = 400;
    constexpr int kLateBudgetMs = 450;

    Signal held;
    Signal release;
    std::thread holder([&] {
        LaneEntry entry(lane, 0);
        held.raise();
        release.await(10000);
    });
    check(held.await(5000), "B8 holder took the lane");

    // Margin: the waiter must not fail fast on arrival, which needs the
    // holder's elapsed time to stay under 400 ms. Arriving at 100 ms leaves
    // 300 ms of slack, so oversleeping under load does not flip the path.
    nap(kWaiterArrivesAfterMs);

    EntryOutcome waiter_outcome;
    std::thread waiter([&] { waiter_outcome = try_entry(lane, kWaiterBudgetMs); });
    waiter.join();
    check(!waiter_outcome.acquired, "B8 mid-queue waiter did not acquire");
    check(mentions(waiter_outcome.message, kTimedOutPhrase),
          "B8 mid-queue waiter waited out its budget and timed out");

    // Margin: the holder has now been in the lane for at least 500 ms against a
    // 450 ms budget. Load lengthens both sleeps, so the measured age only grows
    // and the fail-fast path only becomes more certain.
    const EntryOutcome late = try_entry(lane, kLateBudgetMs);
    release.raise();
    holder.join();

    check(!late.acquired, "B8 late caller did not acquire");
    check(mentions(late.message, kStillRunningPhrase),
          "B8 elapsed time is still measured from the real holder's acquisition");
    check(late.took_ms < 200,
          "B8 late caller failed fast rather than queueing behind the holder");
}

// ---------------------------------------------------------------------------
// B8, other direction: the age of a run is measured from the moment its holder
// acquired, not from the moment that holder arrived. A caller that queued for a
// while and then got in is starting a fresh run, and its time in the queue must
// not be billed to it: if it were, every handover would hand the new holder a
// head start towards looking overrun, and short-budget callers would be turned
// away from a run that has barely begun.
//
//   t0        first holder acquires and holds for 300 ms
//   t0        second caller arrives and queues
//   t0+300    second caller acquires, so its own run age is ~0 here
//   t0+300    a third caller arrives with a 300 ms budget
//
// Correct: the third caller sees a run that just started, so it queues and then
// times out. Billing the queue time to the second caller would show a 300 ms old
// run instead, and the third caller would be turned away as an overrun.
// ---------------------------------------------------------------------------
static void test_run_age_starts_at_acquisition() {
    InferenceLane lane("handover-model");
    constexpr int kFirstHoldMs = 300;
    constexpr int kThirdBudgetMs = 200;

    Signal first_held;
    Signal handed_over;
    Signal release_second;

    std::thread first([&] {
        LaneEntry entry(lane, 0);
        first_held.raise();
        nap(kFirstHoldMs);
    });
    // Ordering matters only for the diagnosis, not for the assertion: waiting
    // for the first holder guarantees the second caller really does queue, which
    // is what gives it queue time to be wrongly billed for.
    check(first_held.await(5000), "B8 first holder took the lane");

    std::thread second([&] {
        LaneEntry entry(lane, 0);
        handed_over.raise();
        release_second.await(10000);
    });
    check(handed_over.await(10000), "B8 queued caller was handed the lane");

    // Margin: the new holder's run is a few ms old against a 200 ms budget, so
    // this caller must queue. Load can only add a few ms of handover latency,
    // well inside that slack, while it lengthens the queue time that the buggy
    // version would bill, making the bug more visible rather than less.
    const EntryOutcome third = try_entry(lane, kThirdBudgetMs);
    release_second.raise();
    second.join();
    first.join();

    check(!third.acquired, "B8 lane was still held by the queued caller");
    check(mentions(third.message, kTimedOutPhrase),
          "B8 a fresh holder's run age excludes the time it spent queueing");
}

// ---------------------------------------------------------------------------
// B10: both failure modes carry a usable message, and the fail-fast one reports
// a measurement rather than diagnosing a cause.
// ---------------------------------------------------------------------------
static void test_failure_messages_are_diagnosable() {
    InferenceLane lane("diagnosable-model");

    Signal held;
    Signal release;
    std::thread holder([&] {
        LaneEntry entry(lane, 0);
        held.raise();
        release.await(10000);
    });
    check(held.await(5000), "B10 holder took the lane");

    nap(120);
    const EntryOutcome fast = try_entry(lane, 30);
    const EntryOutcome slow = try_entry(lane, 60);
    release.raise();
    holder.join();

    check(fast.message != slow.message,
          "B10 the two failure modes do not share one message");
    check(!fast.message.empty() && !slow.message.empty(),
          "B10 both failures carry text");
    check(mentions(fast.message, "diagnosable-model") &&
              mentions(slow.message, "diagnosable-model"),
          "B10 both failures name the model");
    // The fail-fast wording must not accuse the run of being stuck: a short
    // budget meeting a legitimately long run reaches this path too.
    for (const char *verdict : {"stuck", "wedged", "hung", "deadlock"}) {
        check(!mentions(fast.message, verdict),
              std::string("B10 fail-fast message avoids diagnosing '") +
                  verdict + "'");
    }
}

int main() {
    // Last resort only. Every threaded test above is individually bounded, so
    // this should never fire; it exists so that an implementation which parks a
    // thread forever still ends the job instead of occupying a CI runner.
    std::thread watchdog([] {
        std::this_thread::sleep_for(std::chrono::seconds(30));
        fprintf(stderr, "FAIL: watchdog fired, an inference_lane test hung\n");
        fflush(stderr);
        std::_Exit(1);
    });
    watchdog.detach();

    test_budget_negotiation();
    test_overrun_predicate();
    test_mutual_exclusion();
    test_unbounded_waits_out_the_holder();
    test_bounded_wait_times_out();
    test_fail_fast_against_a_long_run();
    test_idle_lane_is_never_stuck();
    test_release_on_exception();
    test_waiter_does_not_become_the_holder();
    test_run_age_starts_at_acquisition();
    test_failure_messages_are_diagnosable();

    if (failures == 0) {
        fprintf(stderr, "\nAll inference_lane tests passed.\n");
        return 0;
    }
    fprintf(stderr, "\n%d inference_lane test(s) failed.\n", failures);
    return 1;
}
