#include "inference_lane.h"

#include <algorithm>
#include <chrono>

namespace audiocpp_backend {

std::int64_t monotonic_millis() {
    return std::chrono::duration_cast<std::chrono::milliseconds>(
               std::chrono::steady_clock::now().time_since_epoch())
        .count();
}

int resolve_wait_budget_ms(int policy_ceiling_ms, int request_hint_ms) {
    // Both sides normalise to 0 for "not specified", which is also the value
    // that means unbounded on the way out, so the unspecified cases fall out of
    // the arithmetic instead of needing their own branches.
    const int ceiling = policy_ceiling_ms > 0 ? policy_ceiling_ms : 0;
    const int hint = request_hint_ms > 0 ? request_hint_ms : 0;

    if (ceiling == 0) {
        return hint;
    }
    if (hint == 0) {
        return ceiling;
    }
    // The hint can tighten the ceiling but never loosen it.
    return std::min(ceiling, hint);
}

bool run_exceeds_budget(bool lane_occupied, std::int64_t run_started_ms,
                        std::int64_t now_ms, int budget_ms) {
    if (!lane_occupied || budget_ms <= 0) {
        return false;
    }
    return now_ms - run_started_ms > static_cast<std::int64_t>(budget_ms);
}

namespace {

std::string busy_prefix(const std::string &model_label) {
    return "inference lane for model '" + model_label + "' is busy: ";
}

// States the measurement and nothing else. A short-budget caller meeting a
// legitimately long run lands here too, so this text must not declare the run
// broken; the numbers let a reader decide that for themselves.
std::string overrun_message(const std::string &model_label,
                            std::int64_t run_age_ms, int budget_ms) {
    return busy_prefix(model_label) + "the in-flight run has been running for " +
           std::to_string(run_age_ms) + " ms, longer than this request's " +
           std::to_string(budget_ms) + " ms wait budget";
}

std::string wait_exhausted_message(const std::string &model_label,
                                   int budget_ms) {
    return busy_prefix(model_label) + "timed out after " +
           std::to_string(budget_ms) +
           " ms waiting for the in-flight run to finish";
}

} // namespace

void InferenceLane::occupy(int budget_ms) {
    std::unique_lock<std::mutex> lock(state_mutex_);

    // Checked once, on arrival: if the run already in the lane has outlived what
    // this caller brought, no amount of waiting can help it, and queueing here
    // is exactly how a wedged run swallows every handler thread.
    const std::int64_t arrived_ms = monotonic_millis();
    if (run_exceeds_budget(occupied_, run_started_ms_, arrived_ms, budget_ms)) {
        throw LaneUnavailable(overrun_message(
            model_label_, arrived_ms - run_started_ms_, budget_ms));
    }

    if (budget_ms > 0) {
        if (!vacated_.wait_for(lock, std::chrono::milliseconds(budget_ms),
                               [this] { return !occupied_; })) {
            throw LaneUnavailable(
                wait_exhausted_message(model_label_, budget_ms));
        }
    } else {
        vacated_.wait(lock, [this] { return !occupied_; });
    }

    // Only now, holding both the mutex and the lane. Stamping any earlier would
    // restart the age of the run for every caller behind this one and make a
    // genuinely wedged holder look freshly started forever.
    occupied_ = true;
    run_started_ms_ = monotonic_millis();
}

void InferenceLane::vacate() {
    {
        std::lock_guard<std::mutex> lock(state_mutex_);
        occupied_ = false;
    }
    // notify_all, not notify_one: a waiter that times out concurrently with a
    // notification can consume it, and losing the only wakeup would park the
    // remaining waiters for the rest of the run's lifetime. Waking all of them
    // still admits exactly one, since the rest re-test occupancy under the mutex
    // and go back to waiting, and ordering among waiters is not a requirement.
    vacated_.notify_all();
}

LaneEntry::LaneEntry(InferenceLane &lane, int budget_ms) : lane_(lane) {
    // If this throws, the object never existed, so ~LaneEntry does not run and
    // cannot hand back a lane this caller never held.
    lane_.occupy(budget_ms);
}

LaneEntry::~LaneEntry() { lane_.vacate(); }

} // namespace audiocpp_backend
