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

static void test_adaptive_budget() {
    EXPECT(adaptive_prefill_budget(0, 2048) == 2048, "no decoder: the whole batch");
    EXPECT(adaptive_prefill_budget(1, 2048) == 2048, "one decoder: the whole batch");
    EXPECT(adaptive_prefill_budget(2, 2048) == 1024, "two decoders");
    EXPECT(adaptive_prefill_budget(4, 2048) == 512, "four decoders");
    EXPECT(adaptive_prefill_budget(8, 2048) == 256, "eight decoders");
    EXPECT(adaptive_prefill_budget(32, 2048) == 128, "floor at 128");
    EXPECT(adaptive_prefill_budget(2, 512) == 512, "never above n_batch");
    EXPECT(adaptive_prefill_budget(64, 64) == 64, "a tiny batch caps the floor");
    for (int32_t n_batch : {64, 512, 2048, 8192}) {
        int32_t prev = adaptive_prefill_budget(0, n_batch);
        for (int32_t d = 1; d <= 128; d++) {
            const int32_t b = adaptive_prefill_budget(d, n_batch);
            EXPECT(b <= prev, "the budget never grows with more decoders");
            EXPECT(b >= 1 && b <= n_batch, "the budget stays in [1, n_batch]");
            prev = b;
        }
    }
    EXPECT(step_prefill_budget(256, 8, 2048) == 256, "an explicit budget is fixed");
    EXPECT(step_prefill_budget(0, 8, 2048) == 0, "0 is off");
    EXPECT(step_prefill_budget(prefill_budget_adaptive, 8, 2048) == 256, "adaptive follows the decoders");
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

static void test_resolve_paged() {
    auto c = resolve_paged(std::nullopt, true, false, true);
    EXPECT(c.on && c.info.empty() && c.error.empty(), "on by default");
    c = resolve_paged(false, true, false, true);
    EXPECT(!c.on && c.info.empty() && c.error.empty(), "paged_kv:false turns it off quietly");
    c = resolve_paged(true, true, false, true);
    EXPECT(c.on, "explicit paged_kv:true");

    // Defaults step aside for a model that cannot run paged, with one info line.
    c = resolve_paged(std::nullopt, false, false, true);
    EXPECT(!c.on && c.error.empty() && c.info.find("kv_unified:false") != std::string::npos, "kv_unified:false turns the default off");
    c = resolve_paged(std::nullopt, true, true, true);
    EXPECT(!c.on && c.error.empty() && c.info.find("context_shift") != std::string::npos, "context_shift turns the default off");
    c = resolve_paged(std::nullopt, true, false, false);
    EXPECT(!c.on && c.error.empty() && c.info.find("cont_batching:false") != std::string::npos, "cont_batching:false turns the default off");

    // An explicit paged_kv:true with a conflicting option fails the load.
    c = resolve_paged(true, false, false, true);
    EXPECT(!c.on && c.error.find("kv_unified:false") != std::string::npos, "explicit paged_kv with kv_unified:false is an error");
    c = resolve_paged(true, true, true, true);
    EXPECT(!c.on && c.error.find("context_shift") != std::string::npos, "explicit paged_kv with context_shift is an error");
    c = resolve_paged(true, true, false, false);
    EXPECT(!c.on && c.error.find("cont_batching:false") != std::string::npos, "explicit paged_kv with cont_batching:false is an error");
    // paged_kv:false with a conflict: nothing to say.
    c = resolve_paged(false, false, true, false);
    EXPECT(!c.on && c.info.empty() && c.error.empty(), "off and conflicting: quiet");

    EXPECT(paged_default_parallel == 8, "paged serving defaults to 8 slots");
    EXPECT(default_prefill_budget == 256, "default prefill budget");
}

static void test_unique_cells() {
    EXPECT(unique_cells({}) == 0, "empty");
    EXPECT(unique_cells({{100}, {50}}) == 150, "no sharing");
    // A child copied 80 cells of its parent's prompt, then added 20 of its own.
    EXPECT(unique_cells({{80}, {100, 0, 80}}) == 100, "shared prefix counted once");
    // Two children of the same parent.
    EXPECT(unique_cells({{80}, {100, 0, 80}, {90, 0, 80}}) == 110, "two children");
    // The parent was cleared (preempted, released): the child owns the cells now.
    EXPECT(unique_cells({{0}, {100, 0, 80}}) == 100, "source gone");
    // The parent kept only part of the prefix.
    EXPECT(unique_cells({{30}, {100, 0, 80}}) == 100, "source shrank");
    EXPECT(unique_cells({{50, 0, 50}}) == 50, "a slot never shares with itself");
    EXPECT(unique_cells({{50, 7, 50}}) == 50, "out of range source is ignored");
}

static void test_preemption_cap() {
    // The youngest request has been preempted twice: the next youngest goes instead.
    std::vector<victim_candidate> c = {{0, 1, true, 0}, {1, 2, true, 0}, {2, 3, true, paged_max_preemptions}};
    EXPECT(pick_victim(c) == 1, "a request at the cap is skipped");
    // All at the cap: still preempt rather than fail a request.
    c = {{0, 1, true, 2}, {1, 2, true, 2}};
    EXPECT(pick_victim(c) == 1, "cap gives way when every slot reached it");
    // A capped resumable slot still goes before a non-resumable one.
    c = {{0, 1, true, 2}, {1, 9, false, 0}};
    EXPECT(pick_victim(c) == 0, "capped resumable before non-resumable");
}

static void test_victim_cells() {
    // the fewest cells go first: they are the cheapest to recompute
    std::vector<victim_candidate> c = {{0, 1, true, 0, 300}, {1, 2, true, 0, 900}, {2, 3, true, 0, 500}};
    EXPECT(pick_victim(c) == 0, "fewest cells, even when older");
    c = {{0, 1, true, 0, 400}, {1, 5, true, 0, 400}, {2, 3, true, 0, 400}};
    EXPECT(pick_victim(c) == 1, "equal cells: the youngest");
    c = {{0, 1, true, paged_max_preemptions, 10}, {1, 2, true, 0, 900}};
    EXPECT(pick_victim(c) == 1, "the cap still comes before the cell count");
    c = {{0, 1, false, 0, 10}, {1, 2, true, 0, 900}};
    EXPECT(pick_victim(c) == 1, "resumable before non-resumable, whatever the cells");
}

static void test_resume_need() {
    EXPECT(resume_need(700, 64) == 700 + 256, "small reserve: at least 256 extra");
    EXPECT(resume_need(700, 256) == 700 + 512, "twice the reserve");
    EXPECT(resume_need(700, 0) == 700 + 256, "no reserve: still 256");
    EXPECT(resume_need(700, 64) > slot_claim(700, 64, 0), "more room than a first admission");
}

static void test_stash_fits() {
    const uint64_t mib = 1024 * 1024;
    EXPECT(stash_fits(0, 100 * mib, 8192), "fits an empty stash");
    EXPECT(stash_fits(8000 * mib, 192 * mib, 8192), "fits up to the cap");
    EXPECT(!stash_fits(8000 * mib, 193 * mib, 8192), "not over the cap");
    EXPECT(!stash_fits(0, 1, 0), "cache_ram 0 turns the stash off");
    EXPECT(stash_fits(1ull << 40, 1ull << 40, -1), "cache_ram -1 has no limit");
}

static void test_budget_long() {
    EXPECT(budget_applies(5000, default_prefill_long), "a long prompt is held back by the budget");
    EXPECT(!budget_applies(400, default_prefill_long), "a short prompt of a burst is read whole");
    EXPECT(!budget_applies(1024, 1024), "up to the threshold is short");
    EXPECT(budget_applies(1025, 1024), "above it is long");
    EXPECT(budget_applies(10, 0), "prefill_long 0 holds back every prompt");
}

static void test_readmission() {
    // A preempted request (old task id) waits: newer requests stay behind it.
    pool_view v;
    v.n_pool        = 2048;
    v.n_active      = 2;
    v.n_used        = 400;
    v.older_waiting = true;
    EXPECT(admit(v, 10) == admit_result::WAIT, "new arrivals wait behind a preempted request");
    // The preempted request itself: it already generated past its reserve, so it
    // needs only the cells of its tokens.
    EXPECT(slot_claim(700, 256, 300) == 700, "a running request claims no reserve it has used up");
    EXPECT(slot_claim(700, 256, 100) == 856, "and the rest of its reserve otherwise");
}

static void test_attach() {
    EXPECT(!should_attach(0, paged_min_attach - 1), "too short to attach");
    EXPECT(should_attach(0, paged_min_attach), "long enough");
    EXPECT(!should_attach(600, 600), "no gain over the own cache");
    EXPECT(should_attach(300, 600), "longer than the own cache");
}

static void test_spec_pause() {
    EXPECT(!spec_paused(false, 100, 0), "0 never pauses");
    EXPECT(!spec_paused(false, 4, 4), "at the limit: still drafting");
    EXPECT(spec_paused(false, 5, 4), "above the limit: paused");
    EXPECT(spec_paused(true, 4, 4), "hysteresis: stays paused at the limit");
    EXPECT(!spec_paused(true, 3, 4), "resumes one below the limit");
    EXPECT(spec_paused(true, 13, 16) && spec_paused(true, 13, 16), "16: stays paused down to 13");
    EXPECT(!spec_paused(true, 12, 16), "16: resumes at 12");
    // no flapping: a count that moves by one around the limit does not flip it
    bool p = false;
    int flips = 0;
    for (int k = 0; k < 20; k++) {
        const bool q = spec_paused(p, (k % 2) ? 8 : 9, 8);
        flips += q != p;
        p = q;
    }
    EXPECT(flips == 1, "one flip for a count hovering at the limit");
}

static void test_order() {
    order_params p;
    wait_key a, b;
    a.id = 1; b.id = 2;
    EXPECT(goes_before(a, b, p) && !goes_before(b, a, p), "same class and size: arrival");
    b.klass = class_interactive;
    EXPECT(goes_before(b, a, p), "interactive before default");
    a.klass = class_batch; b.klass = 0;
    EXPECT(goes_before(b, a, p), "default before batch");
    a.t_wait_us = 25 * 1000000;
    EXPECT(effective_class(a, p) == 0, "a batch request ages into default after aging_us");
    EXPECT(goes_before(a, b, p), "then it goes by arrival");
    // fewest uncached first, within a class
    wait_key c, d;
    c.id = 1; c.n_uncached = 3000;
    d.id = 2; d.n_uncached = 100;
    EXPECT(goes_before(d, c, p), "a cached or short prompt overtakes a long one");
    p.by_cache = false;
    EXPECT(goes_before(c, d, p), "without cache order: arrival");
    p.by_cache = true;
    c.n_overtaken = p.max_overtake;
    EXPECT(goes_before(c, d, p), "overtaken too often: served by arrival");
    c.n_overtaken = 0; c.t_wait_us = p.max_wait_us;
    EXPECT(goes_before(c, d, p), "waited too long: served by arrival");
    c.t_wait_us = 0; c.preempted = true;
    EXPECT(goes_before(c, d, p), "preempted requests are not pushed back again");
    EXPECT(effective_class({class_interactive, 0, 999999999}, p) == class_interactive, "class is capped");
}

static void test_prefill_cap() {
    EXPECT(request_prefill_cap(512, 1, 2048) == 2048, "alone: the whole step");
    EXPECT(request_prefill_cap(512, 3, 2048) == 512, "shared: capped");
    EXPECT(request_prefill_cap(512, 3, 256) == 256, "never above the step");
    EXPECT(request_prefill_cap(0, 3, 2048) == 2048, "0 is off");
}

static void test_idle_trim() {
    EXPECT(idle_trim(800, 100, true) == paged_min_trim, "trim at least paged_min_trim, from the tail");
    EXPECT(idle_trim(800, 300, true) == 300, "trim only what is needed above that");
    EXPECT(idle_trim(100, 1, true) == 100, "a short slot is cleared");
    EXPECT(idle_trim(800, 1000, true) == 800, "at most the whole slot");
    EXPECT(idle_trim(800, 100, false) == 800, "no partial removal: the whole slot");
    EXPECT(idle_trim(0, 100, true) == 0, "nothing to trim");
}

int main() {
    test_prefill_budget();
    test_adaptive_budget();
    test_effective_reserve();
    test_admit();
    test_claim();
    test_demand();
    test_pick_victim();
    test_resolve_paged();
    test_unique_cells();
    test_preemption_cap();
    test_readmission();
    test_victim_cells();
    test_stash_fits();
    test_budget_long();
    test_resume_need();
    test_attach();
    test_spec_pause();
    test_order();
    test_prefill_cap();
    test_idle_trim();
    if (failures > 0) {
        std::fprintf(stderr, "%d serving policy check(s) failed\n", failures);
        return 1;
    }
    std::printf("serving policy: all checks passed\n");
    return 0;
}
