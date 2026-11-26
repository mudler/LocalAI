// SPDX-License-Identifier: MIT

#include "serving_policy.h"

#include <cstdio>

using namespace serving_policy;

static int failures = 0;

#define EXPECT(cond, msg)                                       \
    do {                                                        \
        if (!(cond)) {                                          \
            std::fprintf(stderr, "FAIL %s:%d: %s\n", __FILE__,  \
                         __LINE__, msg);                        \
            failures++;                                         \
        }                                                       \
    } while (0)

static void test_prefill_budget() {
    // Off: the stock n_batch limit, whatever the decode load.
    EXPECT(prefill_tokens_this_step(0, 0, 2048) == 2048, "budget off must return n_batch");
    EXPECT(prefill_tokens_this_step(0, 7, 512) == 512, "budget off must ignore the decode tokens");
    EXPECT(prefill_tokens_this_step(-5, 3, 512) == 512, "a negative budget is off");

    // On: decode first, prefill gets the rest of the budget.
    EXPECT(prefill_tokens_this_step(256, 0, 2048) == 256, "no decode: the whole budget");
    EXPECT(prefill_tokens_this_step(256, 4, 2048) == 252, "four streams: budget minus four");
    EXPECT(prefill_tokens_this_step(512, 100, 2048) == 412, "budget minus decode");

    // Floor at a quarter of the budget, so a long prompt always advances.
    EXPECT(prefill_tokens_this_step(256, 250, 2048) == 64, "heavy decode: floor at budget/4");
    EXPECT(prefill_tokens_this_step(256, 1000, 2048) == 64, "decode above budget: floor at budget/4");

    // Never above n_batch, never below 1.
    EXPECT(prefill_tokens_this_step(4096, 0, 2048) == 2048, "budget above n_batch is clamped");
    EXPECT(prefill_tokens_this_step(2, 10, 2048) == 1, "tiny budget still adds one token");
    EXPECT(prefill_tokens_this_step(256, 0, 0) == 1, "degenerate n_batch still adds one token");
}

static void test_effective_reserve() {
    EXPECT(effective_reserve(256, -1) == 256, "unlimited output keeps the reserve");
    EXPECT(effective_reserve(256, 200) == 200, "reserve is capped at n_predict");
    EXPECT(effective_reserve(256, 1000) == 256, "large n_predict keeps the reserve");
    EXPECT(effective_reserve(0, 200) == 0, "reserve off");
    EXPECT(effective_reserve(-1, 200) == 0, "negative reserve is off");
    EXPECT(effective_reserve(256, 0) == 0, "n_predict 0 needs no output cells");
}

static void test_admit() {
    pool_view v;
    v.n_pool = 2048;

    // Alone: always admitted, even when it does not fit.
    EXPECT(admit(v, 100) == admit_result::ADMIT, "alone and fits");
    EXPECT(admit(v, 5000) == admit_result::ADMIT, "alone and too large: the context checks reject it later");
    v.idle_cells = true;
    v.n_used = 1500;
    EXPECT(admit(v, 804) == admit_result::PURGE_IDLE, "alone but idle cells in the way: purge first");
    EXPECT(admit(v, 100) == admit_result::ADMIT, "alone, fits next to the idle cells");
    v.idle_cells = false;
    EXPECT(admit(v, 804) == admit_result::ADMIT, "alone, nothing left to purge: admit");

    // The overcommit trace: 604-token prompts, 200 output, pool 2048.
    v = pool_view{};
    v.n_pool   = 2048;
    v.n_active = 1;
    v.n_used   = 604;
    v.n_pending = slot_claim(0, 200, 0);
    EXPECT(admit(v, 804) == admit_result::ADMIT, "second request fits");
    v.n_active = 2;
    v.n_used   = 1208;
    v.n_pending = 2 * slot_claim(0, 200, 0);
    EXPECT(admit(v, 804) == admit_result::WAIT, "third request must wait");
    v.idle_cells = true;
    EXPECT(admit(v, 804) == admit_result::PURGE_IDLE, "idle cells are purged before waiting");

    // Pending prompt of another slot counts against the pool.
    v = pool_view{};
    v.n_pool    = 2048;
    v.n_active  = 1;
    v.n_used    = 100;
    v.n_pending = 1500;
    EXPECT(admit(v, 500) == admit_result::WAIT, "the rest of another prompt is reserved");

    // First come, first served.
    v = pool_view{};
    v.n_pool        = 2048;
    v.n_active      = 1;
    v.older_waiting = true;
    EXPECT(admit(v, 10) == admit_result::WAIT, "an older waiting request goes first");
    v.n_active = 0;
    EXPECT(admit(v, 10) == admit_result::WAIT, "older waiting request goes first even on an empty pool");
}

static void test_claim() {
    EXPECT(slot_claim(700, 256, 0) == 956, "prompt left plus the whole reserve");
    EXPECT(slot_claim(0, 256, 100) == 156, "the generated part of the reserve is in use already");
    EXPECT(slot_claim(0, 256, 300) == 0, "past the reserve: nothing claimed");
    EXPECT(slot_claim(-4, 0, 0) == 0, "never negative");
}

static void test_demand() {
    EXPECT(slot_demand(true, 0, 0) == 1, "plain decode needs one cell");
    EXPECT(slot_demand(true, 3, 0) == 4, "decode with drafts");
    EXPECT(slot_demand(true, -1, 0) == 1, "negative draft is ignored");
    EXPECT(slot_demand(false, 0, 700) == 700, "prefill needs the rest of the prompt");
    EXPECT(slot_demand(false, 0, -3) == 0, "nothing left");
    EXPECT(!pool_short(2048, 2000, 48), "exactly full is not short");
    EXPECT(pool_short(2048, 2000, 49), "one over is short");
}

static void test_pick_victim() {
    std::vector<victim_candidate> c;
    EXPECT(pick_victim(c) == -1, "no candidates");
    c.push_back({0, 10, true});
    EXPECT(pick_victim(c) == -1, "the only running slot is never preempted");
    c.push_back({1, 12, true});
    c.push_back({2, 11, true});
    EXPECT(pick_victim(c) == 1, "youngest resumable");

    // A younger non-resumable slot is not chosen while a resumable one exists.
    c.push_back({3, 20, false});
    EXPECT(pick_victim(c) == 1, "resumable preferred over younger non-resumable");

    // Last resort: only non-resumable slots.
    std::vector<victim_candidate> nr = {{0, 5, false}, {1, 9, false}, {2, 7, false}};
    EXPECT(pick_victim(nr) == 1, "youngest non-resumable as last resort");

    // One resumable, one not: the resumable one, even if older.
    std::vector<victim_candidate> mix = {{0, 1, true}, {1, 2, false}};
    EXPECT(pick_victim(mix) == 0, "old resumable before young non-resumable");
}

static void test_checks() {
    EXPECT(check_paged_options(true, false, true).empty(), "defaults are fine");
    EXPECT(check_paged_options(false, false, true).find("kv_unified") != std::string::npos, "kv_unified:false is an error");
    EXPECT(check_paged_options(true, true, true).find("context_shift") != std::string::npos, "context_shift is an error");
    EXPECT(check_paged_options(true, false, false).find("cont_batching") != std::string::npos, "cont_batching:false is an error");
    EXPECT(paged_default_parallel == 8, "paged_kv defaults to 8 slots");
}

int main() {
    test_prefill_budget();
    test_effective_reserve();
    test_admit();
    test_claim();
    test_demand();
    test_pick_victim();
    test_checks();
    if (failures > 0) {
        std::fprintf(stderr, "%d serving policy check(s) failed\n", failures);
        return 1;
    }
    std::printf("serving policy: all checks passed\n");
    return 0;
}
