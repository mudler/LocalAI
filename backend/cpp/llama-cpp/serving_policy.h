#pragma once

// Scheduling decisions of the paged serving mode (model options paged_kv,
// prefill_budget, kv_output_reserve, paged_preempt).
//
// The hooks in patches/0002-paged-serving.patch call these functions from the
// llama.cpp server loop. All the arithmetic lives here, free of llama.cpp
// types, so it can be tested on its own (serving_policy_test.cpp) and so a
// llama.cpp bump only has to re-anchor the hooks, not re-derive the policy.
// See backend/cpp/llama-cpp/PATCHES.md.
//
// prepare.sh copies this file into llama.cpp/tools/server/ before the patch
// is compiled. Standard library only.

#include <algorithm>
#include <cstdint>
#include <vector>

namespace serving_policy {

// Number of prompt tokens the server may add to the batch in this step.
//
// budget   the prefill_budget option (0 = off).
// n_decode decode tokens already in the batch (they are added first).
// n_batch  the logical batch size, the hard limit of one step.
//
// With the budget off, the result is n_batch: the stock limit, so the fill
// loop does not change. With the budget on, decode gets its tokens first and
// prefill gets what is left of the budget, but never less than a quarter of
// it, so a long prompt still advances when many streams decode.
inline int32_t prefill_tokens_this_step(int32_t budget, int32_t n_decode, int32_t n_batch) {
    if (budget <= 0) {
        return n_batch;
    }
    const int32_t n = std::max(budget - n_decode, budget / 4);
    return std::clamp(n, (int32_t) 1, std::max((int32_t) 1, n_batch));
}

// Free cells a request must find beyond its prompt before it starts.
// n_predict is the request's output limit, -1 when it has none: a request
// that asks for fewer tokens than the reserve only needs room for those.
inline int64_t effective_reserve(int64_t reserve, int64_t n_predict) {
    if (reserve <= 0) {
        return 0;
    }
    if (n_predict >= 0) {
        return std::min(reserve, n_predict);
    }
    return reserve;
}

enum class admit_result {
    ADMIT,      // start (or resume) the prompt now
    PURGE_IDLE, // drop the cached cells of one idle slot, then ask again
    WAIT,       // try again in a later step
};

// What the server knows about the KV pool when it asks for admission.
struct pool_view {
    int64_t n_pool        = 0;     // cells in the unified KV cache
    int64_t n_used        = 0;     // cells held by every other slot (estimate, may count shared cells twice)
    int64_t n_pending     = 0;     // cells the other running slots still claim, see slot_claim()
    int32_t n_active      = 0;     // other slots that are admitted and still running
    bool    idle_cells    = false; // an idle slot still holds cached cells
    bool    older_waiting = false; // an older request waits for admission (first come, first served)
};

// Decides if a request that needs n_need cells (prompt plus reserve) can start.
// A request alone on the server is always admitted: when it does not fit, the
// existing context-size checks reject it with their own error.
inline admit_result admit(const pool_view & v, int64_t n_need) {
    if (v.n_active == 0 && !v.older_waiting) {
        return v.idle_cells && v.n_pool - v.n_used < n_need ? admit_result::PURGE_IDLE : admit_result::ADMIT;
    }
    if (v.older_waiting) {
        return admit_result::WAIT;
    }
    if (v.n_pool - v.n_used - v.n_pending >= n_need) {
        return admit_result::ADMIT;
    }
    return v.idle_cells ? admit_result::PURGE_IDLE : admit_result::WAIT;
}

// Cells a running slot still claims for admission purposes: the rest of its
// prompt plus the part of its output reserve it has not generated yet. Holding
// the reserve of running slots is what lets every admitted request finish
// without preemption when the reserve covers its output.
inline int64_t slot_claim(int64_t n_prompt_left, int64_t n_reserve, int64_t n_generated) {
    return std::max((int64_t) 0, n_prompt_left) + std::max((int64_t) 0, n_reserve - n_generated);
}

// Cells one slot needs in the next step.
// generating: one cell for the sampled token plus one per draft token.
// prefilling: the rest of its prompt (or of its resume buffer).
inline int64_t slot_demand(bool generating, int64_t n_draft_max, int64_t n_prompt_left) {
    if (generating) {
        return 1 + std::max((int64_t) 0, n_draft_max);
    }
    return std::max((int64_t) 0, n_prompt_left);
}

// True when the running slots need more cells than the pool has.
inline bool pool_short(int64_t n_pool, int64_t n_used, int64_t n_demand) {
    return n_used + n_demand > n_pool;
}

struct victim_candidate {
    int     id_slot   = -1;
    int64_t age       = 0;     // larger is younger (the task id)
    bool    resumable = false; // can be rebuilt by recomputing its tokens
};

// Picks the slot to preempt when the pool is short.
//
// Returns an index into c, or -1 when nothing may be preempted. The youngest
// resumable slot is chosen first. A slot that cannot be resumed (speculative
// decoding, a multimodal prompt, a score fork, recurrent or hybrid memory) is
// chosen only when no resumable slot exists: it fails with a retryable error.
// The only running slot is never preempted, so one request always makes progress.
inline int pick_victim(const std::vector<victim_candidate> & c) {
    if (c.size() < 2) {
        return -1;
    }
    int best = -1;
    for (int pass = 0; pass < 2 && best < 0; pass++) {
        const bool want_resumable = pass == 0;
        for (int i = 0; i < (int) c.size(); i++) {
            if (c[i].resumable != want_resumable) {
                continue;
            }
            if (best < 0 || c[i].age > c[best].age) {
                best = i;
            }
        }
    }
    return best;
}

} // namespace serving_policy
