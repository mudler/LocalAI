// SPDX-License-Identifier: MIT

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
#include <string>
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
    PURGE_IDLE, // drop the cached cells of one idle or waiting slot, then ask again
    WAIT,       // try again in a later step
};

// What the server knows about the KV pool when it asks for admission.
struct pool_view {
    int64_t n_pool        = 0;     // cells in the unified KV cache
    int64_t n_used        = 0;     // cells held by every other slot (estimate, may count shared cells twice)
    int64_t n_pending     = 0;     // cells the other running slots still claim, see slot_claim()
    int32_t n_active      = 0;     // other slots that are admitted and still running
    bool    idle_cells    = false; // an idle or waiting slot still holds cached cells it can drop
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

// How many times one request may be preempted while another slot can be
// preempted instead. Without a cap the youngest request can be preempted
// again each time it resumes, and never finish while the pool stays full.
constexpr int paged_max_preemptions = 2;

struct victim_candidate {
    int     id_slot     = -1;
    int64_t age         = 0;     // larger is younger (the task id, given on arrival)
    bool    resumable   = false; // can be rebuilt by recomputing its tokens
    int     n_preempted = 0;     // times this request was preempted already
};

// Picks the slot to preempt when the pool is short.
//
// Returns an index into c, or -1 when nothing may be preempted. In order:
//   1. the youngest resumable slot preempted fewer than paged_max_preemptions times,
//   2. the youngest resumable slot (the cap gives way only to avoid failing a request),
//   3. the youngest slot that cannot be resumed (speculative decoding, a multimodal
//      prompt, a score fork, recurrent or hybrid memory): it fails with a retryable error.
// The only running slot is never preempted, so one request always makes progress.
inline int pick_victim(const std::vector<victim_candidate> & c) {
    if (c.size() < 2) {
        return -1;
    }
    int best = -1;
    for (int pass = 0; pass < 3 && best < 0; pass++) {
        for (int i = 0; i < (int) c.size(); i++) {
            const bool ok = pass == 0 ? c[i].resumable && c[i].n_preempted < paged_max_preemptions
                          : pass == 1 ? c[i].resumable
                                      : !c[i].resumable;
            if (ok && (best < 0 || c[i].age > c[best].age)) {
                best = i;
            }
        }
    }
    return best;
}

// Cells of one slot for the pool count, and where they came from: a slot that
// copied another slot's prompt (parallel completions, a shared prompt prefix)
// shares its first `shared` cells with slot `src` instead of owning them.
struct slot_cells {
    int64_t cells  = 0;
    int     src    = -1; // index of the slot it copied from, -1 if none
    int64_t shared = 0;  // cells copied from src
};

// Cells in use, each counted once. A shared cell is counted for the source
// slot only, and only while both slots still hold it.
inline int64_t unique_cells(const std::vector<slot_cells> & s) {
    int64_t n = 0;
    for (const auto & x : s) {
        n += std::max((int64_t) 0, x.cells);
    }
    for (int i = 0; i < (int) s.size(); i++) {
        const int src = s[i].src;
        if (src < 0 || src >= (int) s.size() || src == i) {
            continue;
        }
        n -= std::max((int64_t) 0, std::min({ s[i].shared, s[i].cells, s[src].cells }));
    }
    return n;
}

// Slot count when paged_kv is on and neither the model options nor the
// environment set parallel: with one slot the pool has a single user and
// the mode does nothing.
constexpr int paged_default_parallel = 8;

// Checks the options that paged serving cannot run with. Returns an empty
// string when they are fine, otherwise the error for LoadModel.
inline std::string check_paged_options(bool kv_unified, bool ctx_shift, bool cont_batching) {
    if (!kv_unified) {
        return "paged_kv needs the unified KV cache; remove kv_unified:false or paged_kv";
    }
    if (ctx_shift) {
        return "paged_kv cannot be used with context_shift; remove context_shift or paged_kv";
    }
    if (!cont_batching) {
        return "paged_kv needs continuous batching; remove cont_batching:false or paged_kv";
    }
    return "";
}

} // namespace serving_policy
