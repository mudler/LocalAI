// paged-loadgen: a dynamic-load benchmark for paged KV that actually exercises the
// regime where paging wins - variable prompt lengths, variable generation lengths,
// staggered (continuous) arrival, and a shared system prefix. The stock
// examples/paged/paged.cpp adds all requests up front with a fixed n_predict from a
// 20-prompt pool, so it never creates KV-memory pressure or fragmentation and
// therefore never shows a paged advantage (see PAGED_KV_HIGH_CONCURRENCY.md).
//
// Build: drop into PR #22569's examples/paged/ and add to its CMakeLists.txt next to
// llama-paged (it uses the same llama_paged_scheduler_* API). Run on the TARGET GPU
// (e.g. 2xH200) where bandwidth lets decode scale to thousands of sequences and KV
// memory becomes the binding constraint - that is where paged KV pays off and where
// this harness produces a meaningful number. On a low-bandwidth box (GB10) throughput
// plateaus long before memory binds, so the win is not observable there regardless.
//
// Metrics reported:
//   - goodput (decode tokens/s aggregate) under the dynamic load
//   - peak concurrent in-flight sequences actually sustained
//   - paged peak KV bytes used  vs  the contiguous reservation a unified cache needs
//     (n_seq_peak * max_ctx), i.e. the capacity ratio = the headroom paging unlocks
//
// The capacity ratio is the load-bearing number for the buy decision: it is how many
// more concurrent tenants a fixed HBM budget serves with paging than without.

#include "common.h"
#include "llama.h"

#include <cmath>
#include <cstdio>
#include <cstring>
#include <random>
#include <string>
#include <vector>

// ---- workload knobs (env-overridable so the harness is sweepable without rebuilds) ----
static int env_int(const char * k, int dflt) { const char * v = getenv(k); return v ? atoi(v) : dflt; }

struct workload_cfg {
    int    total_requests  = env_int("LG_TOTAL",    2000); // total requests to serve
    int    target_inflight = env_int("LG_INFLIGHT",  256); // continuous-batching concurrency target
    int    prefix_tokens   = env_int("LG_PREFIX",    512); // shared system-prompt prefix (prefix-cache target)
    int    suffix_min      = env_int("LG_SUFMIN",     16); // per-request unique prompt suffix range
    int    suffix_max      = env_int("LG_SUFMAX",    768);
    int    gen_short       = env_int("LG_GENSHORT",   32); // bimodal generation: most short...
    int    gen_long        = env_int("LG_GENLONG",  1024); // ...some long (the over-reservation driver)
    int    gen_long_pct    = env_int("LG_LONGPCT",    15); // % of requests that are long
    int    block_size      = env_int("LG_BLOCK",      16); // must match -kvbls
    unsigned seed          = (unsigned) env_int("LG_SEED", 1234);
};

// Per-request plan drawn from the workload distribution.
struct req_plan { int prompt_len; int gen_len; };

int main(int argc, char ** argv) {
    common_params params;
    params.n_predict = -1; // per-request, controlled by the plan below
    if (!common_params_parse(argc, argv, params, LLAMA_EXAMPLE_PAGED)) {
        fprintf(stderr, "usage: %s -m <model> -kvp --fit off -ngpub N -ncpub M -ngl 99\n", argv[0]);
        return 1;
    }
    params.kv_paged = true;

    common_init_result init = common_init_from_params(params);
    llama_model *   model = init.model.get();
    llama_context * ctx   = init.context.get();
    if (!model || !ctx) { fprintf(stderr, "load failed\n"); return 1; }
    const llama_vocab * vocab = llama_model_get_vocab(model);

    workload_cfg cfg;
    std::mt19937 rng(cfg.seed);
    std::uniform_int_distribution<int> suf(cfg.suffix_min, cfg.suffix_max);
    std::uniform_int_distribution<int> pct(1, 100);

    // KV bytes/token = 2(K,V) * n_layers * n_head_kv * head_dim * sizeof(f16). Confirmed
    // against llama-kv-cache-paged.cpp (block_bytes formula). Used for the capacity ratio.
    const int n_layers   = llama_model_n_layer(model);
    const int n_head_kv  = llama_model_n_head_kv(model);
    const int head_dim   = llama_model_n_embd(model) / llama_model_n_head(model);
    const size_t kv_bytes_per_token = (size_t)2 * n_layers * n_head_kv * head_dim * sizeof(uint16_t);

    // A long shared system prefix that every request reuses (the prefix-cache target).
    std::vector<llama_token> prefix = common_tokenize(ctx, std::string(cfg.prefix_tokens, 'x'), true);

    // Pre-draw all request plans so paged peak usage and the contiguous reservation are
    // computed from the SAME workload.
    std::vector<req_plan> plans(cfg.total_requests);
    int max_ctx = 0;
    for (auto & p : plans) {
        p.prompt_len = cfg.prefix_tokens + suf(rng);
        p.gen_len    = (pct(rng) <= cfg.gen_long_pct) ? cfg.gen_long : cfg.gen_short;
        max_ctx      = std::max(max_ctx, p.prompt_len + p.gen_len);
    }

    llama_paged_scheduler * sched = llama_paged_scheduler_init(ctx);
    if (!sched) { fprintf(stderr, "scheduler init failed\n"); return 1; }

    // ---- continuous-arrival loop: keep ~target_inflight requests live at all times ----
    int    next_req = 0, done = 0, inflight = 0, peak_inflight = 0;
    long   total_decoded = 0;
    size_t peak_kv_bytes_paged = 0;   // sum over live seqs of ceil(used/block)*block*kv_bytes
    size_t live_used_tokens = 0;      // running sum of actual KV tokens held by live seqs

    auto admit = [&](int rid) {
        const req_plan & p = plans[rid];
        std::vector<llama_token> toks = prefix; // shared prefix...
        std::vector<llama_token> suff = common_tokenize(ctx, std::string(p.prompt_len - cfg.prefix_tokens, 'y'), false);
        toks.insert(toks.end(), suff.begin(), suff.end()); // ...+ unique suffix
        if (llama_paged_scheduler_add_request(sched, toks.data(), toks.size(), rid)) {
            inflight++; peak_inflight = std::max(peak_inflight, inflight);
            live_used_tokens += p.prompt_len;
        }
    };

    const int64_t t0 = ggml_time_us();
    for (int i = 0; i < cfg.target_inflight && next_req < cfg.total_requests; ++i) admit(next_req++);

    llama_batch batch = {};
    std::vector<llama_token> sampled; std::vector<int8_t> stop_flags;

    while (done < cfg.total_requests) {
        if (!llama_paged_scheduler_prepare_batch(sched, &batch)) break;
        const llama_paged_batch_info * info = llama_paged_scheduler_get_batch_info(sched);
        sampled.assign(info->n_seq, 0); stop_flags.assign(info->n_seq, 0);

        // (decode is done inside the scheduler/update path in PR #22569; greedy here)
        for (int i = 0; i < info->n_seq; ++i) {
            const int rid = info->seq_ids[i];
            llama_paged_seq_state st{};
            llama_paged_scheduler_get_seq_state(sched, rid, &st);
            // greedy argmax from the i-th row of logits
            const float * lg = llama_get_logits_ith(ctx, i);
            int best = 0; float bv = lg[0];
            for (int t = 1; t < llama_vocab_n_tokens(vocab); ++t) if (lg[t] > bv) { bv = lg[t]; best = t; }
            sampled[i] = best;
            const bool stop = llama_vocab_is_eog(vocab, best) || st.n_decoded + 1 >= plans[rid].gen_len;
            stop_flags[i] = stop ? 1 : 0;
            if (!stop) { total_decoded++; live_used_tokens++; }
            if (stop) {
                done++; inflight--;
                live_used_tokens -= (plans[rid].prompt_len + st.n_decoded);
                if (next_req < cfg.total_requests) admit(next_req++); // continuous arrival
            }
        }
        // paged peak KV: blocks are allocated per live seq = ceil(used/block); approximate
        // current paged footprint from live_used_tokens rounded up per the block size.
        const size_t paged_now = (size_t)std::ceil((double)live_used_tokens / cfg.block_size)
                                 * cfg.block_size * kv_bytes_per_token;
        peak_kv_bytes_paged = std::max(peak_kv_bytes_paged, paged_now);

        llama_paged_scheduler_update(sched, &batch, sampled.data(), stop_flags.data());
    }
    const double secs = (ggml_time_us() - t0) / 1e6;

    // Contiguous unified-KV reservation needed to serve the SAME peak concurrency without
    // mid-generation eviction: every live slot must be backed for the worst-case context.
    const size_t contig_reserve = (size_t)peak_inflight * max_ctx * kv_bytes_per_token;

    printf("\n==== paged-loadgen ====\n");
    printf("requests served      : %d  (target inflight %d, peak inflight %d)\n", done, cfg.target_inflight, peak_inflight);
    printf("goodput (decode)     : %.1f tok/s   (%ld tokens / %.2f s)\n", total_decoded / secs, total_decoded, secs);
    printf("kv bytes / token     : %zu (n_layer=%d n_head_kv=%d head_dim=%d f16)\n", kv_bytes_per_token, n_layers, n_head_kv, head_dim);
    printf("paged peak KV        : %.2f GiB (allocated on demand)\n", peak_kv_bytes_paged / 1073741824.0);
    printf("contiguous reserve   : %.2f GiB (peak_inflight * max_ctx %d)\n", contig_reserve / 1073741824.0, max_ctx);
    printf("CAPACITY RATIO       : %.2fx  <- tenants-per-HBM paging unlocks\n",
           peak_kv_bytes_paged ? (double)contig_reserve / peak_kv_bytes_paged : 0.0);
    printf("  (plus cross-request prefix sharing of the %d-token shared prefix, not counted above)\n", cfg.prefix_tokens);

    llama_paged_scheduler_free(sched);
    return 0;
}
