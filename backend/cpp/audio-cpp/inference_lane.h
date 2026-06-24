#pragma once

// Serializes inference against the single audio.cpp model a backend process
// owns. Standard library only.
//
// Why a plain mutex is not enough: an audio.cpp session is not reentrant, so
// concurrent gRPC handlers have to take turns. But once a GPU call stops making
// progress there is nothing a host thread can do to take it back, and an
// unbounded queue behind such a run would absorb the handler threads one by one
// until nothing is left to answer with. A caller therefore needs to be able to
// walk away, and needs to be able to tell "the lane is busy with normal work and
// I ran out of patience" apart from "the run in the lane has already outlived
// the patience I brought".

#include <condition_variable>
#include <cstdint>
#include <mutex>
#include <stdexcept>
#include <string>

namespace audiocpp_backend {

// Reading off a monotonic clock, in milliseconds. Monotonic on purpose: a wall
// clock adjustment must never make an in-flight run look younger or older than
// it is, because that reading decides whether callers give up.
std::int64_t monotonic_millis();

// Collapses the per-model configured ceiling and the optional per-request hint
// into the wait budget a caller actually gets. Returns 0 for "wait
// indefinitely".
//
// Either input may be non-positive, which means "not specified":
//   - an unspecified hint yields the ceiling,
//   - an unspecified ceiling means no policy limit, so the hint stands,
//   - unspecified on both sides is unbounded.
// A specified hint may only tighten the ceiling. A client asking for a longer
// wait than the model's policy allows does not get it, because that would let a
// request weaken an operator's choice.
int resolve_wait_budget_ms(int policy_ceiling_ms, int request_hint_ms);

// True when a caller carrying budget_ms should give up on arrival rather than
// queue up. Deliberately a pure function of the lane's observable state so the
// decision can be tested without threads or sleeping.
//
// Only occupancy makes a start timestamp meaningful: a lane nobody holds is
// never overrunning, whatever timestamp the last holder left behind. An
// unbounded caller (non-positive budget) has no budget to exceed. And the
// comparison is strict, so a run whose age exactly equals the budget still has
// its last millisecond.
bool run_exceeds_budget(bool lane_occupied, std::int64_t run_started_ms,
                        std::int64_t now_ms, int budget_ms);

// Thrown when a caller cannot take the lane, in either of the two situations
// resolve_wait_budget_ms allows for. The message distinguishes them; callers
// that need to report a status code can treat them alike.
class LaneUnavailable : public std::runtime_error {
  public:
    explicit LaneUnavailable(const std::string &reason)
        : std::runtime_error(reason) {}
};

class LaneEntry;

// One lane per loaded model. Shared by every handler thread; not copyable.
class InferenceLane {
  public:
    explicit InferenceLane(std::string model_label)
        : model_label_(std::move(model_label)) {}

    InferenceLane(const InferenceLane &) = delete;
    InferenceLane &operator=(const InferenceLane &) = delete;

    const std::string &model_label() const { return model_label_; }

  private:
    // Occupancy is only reachable through LaneEntry, so there is no way to take
    // the lane without also having something that gives it back.
    friend class LaneEntry;

    void occupy(int budget_ms);
    void vacate();

    const std::string model_label_;

    std::mutex state_mutex_;
    std::condition_variable vacated_;
    bool occupied_ = false;
    // Only meaningful while occupied_ is true.
    std::int64_t run_started_ms_ = 0;
};

// Scoped occupancy of a lane. Construct it where the inference happens and it
// is given back on every exit from that scope, including an exception and
// including a caller that returns from the middle of a long stream. Throws
// LaneUnavailable if the lane could not be taken, in which case there is no
// object and nothing to release.
//
// Not reentrant, and it does not detect reentrancy: a second entry constructed
// while the calling thread already holds the same lane waits for a lane only
// that thread can release. With a positive budget that surfaces as
// LaneUnavailable, but in unbounded mode the thread parks with no diagnostic at
// all. Keep entries one per call: a handler that holds one across a stream must
// not let a helper it calls construct another.
class LaneEntry {
  public:
    // budget_ms <= 0 waits indefinitely. Pass the output of
    // resolve_wait_budget_ms.
    LaneEntry(InferenceLane &lane, int budget_ms);
    ~LaneEntry();

    LaneEntry(const LaneEntry &) = delete;
    LaneEntry &operator=(const LaneEntry &) = delete;

    // Deliberately immovable rather than carefully movable. A moved-from entry
    // would have to stop releasing the lane while the lane still records it as
    // occupied, and that hazard is not worth the convenience: the lane can only
    // be recovered by whoever took it.
    //
    // To hold a lane for longer than one scope, construct the entry in place
    // instead of moving one in. Two shapes work:
    //   std::optional<LaneEntry> held;   // member or local
    //   held.emplace(lane, budget_ms);   // takes the lane, held.reset() gives it back
    //   auto held = std::make_unique<LaneEntry>(lane, budget_ms);  // also returnable
    // Both outlive the acquiring scope and still release exactly once, when they
    // are reset or destroyed. Prefer the optional for a member whose lifetime is
    // the handler's; use the unique_ptr when the entry has to be returned, since
    // an optional of an immovable type is itself immovable and cannot be. A
    // factory may instead write `return LaneEntry(lane, budget_ms);`, which C++17
    // guarantees to elide, whereas `LaneEntry entry(...); return entry;` does not
    // compile, because that form is a move.
    LaneEntry(LaneEntry &&) = delete;
    LaneEntry &operator=(LaneEntry &&) = delete;

  private:
    InferenceLane &lane_;
};

} // namespace audiocpp_backend
