// Paged-pool burst-degradation repro (patch 0024). DEV SCAFFOLDING ONLY.
//
// Reproduces, at the libllama level, the two host-side defects behind the
// "later lower-npl prefill collapses, decode fine, restart cures it" benchmark
// signature:
//
//   * RECLAMATION GAP (Fix-1): a partial tail seq_rm(seq, p0>0, -1) - exactly
//     what llama-server issues on every reused slot - frees the kv-cache CELLS
//     but the paged manager keeps owning the trailing BLOCKS. The manager's
//     free pool silently shrinks. Test A measures the reclaimed-block delta.
//
//   * FRAGMENTATION / NO COMPACTION (Fix-2): a high-fan-out burst that allocates
//     many sequences and frees them in a scrambled order leaves the free queue a
//     scrambled permutation of physical block ids. A later low-npl prefill then
//     pops physically scattered blocks, so its KV scatter-write + in-kernel
//     paged-attention gather lose locality and prefill throughput collapses;
//     decode (single-token append) barely notices. Test B times an npl8 prefill
//     on a FRESH pool vs an npl8 prefill AFTER a scrambling burst+drain.
//
// PASS (post-fix): Test A reclaims ceil((PP-KEEP)/bs) trailing blocks on the
// partial seq_rm (0 pre-fix); Test B's post-burst npl8 prefill_tps is within ~10%
// of the fresh npl8 and num_free returns to the pristine value after the drain.
//
// Run with LLAMA_KV_PAGED=1. Env: BURST_NSLOT(64) NPL(8) PP(512) KEEP(256)
// GEN(4) PAGED_NGL(99). All sequences use distinct content so nothing is shared.

#include "llama.h"
#include "paged-prefix-api.h"

#include <chrono>
#include <clocale>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <vector>

static int env_i(const char * k, int dflt) { const char * v = getenv(k); return v ? atoi(v) : dflt; }

using clk = std::chrono::steady_clock;
static double secs(clk::time_point a, clk::time_point b) {
    return std::chrono::duration<double>(b - a).count();
}

struct Ctx { llama_context * ctx; llama_memory_t mem; llama_batch batch; int n_vocab; };

// Deterministic, content-distinct token for (seq, pos): keeps every sequence's
// blocks unique so no cross-request prefix sharing masks the accounting.
static llama_token tok_of(int seq, int pos, int n_vocab) {
    return (llama_token) (((seq * 1000003 + pos * 131 + 7) % (n_vocab - 200)) + 100);
}

// Prefill n tokens of seq at [pos0, pos0+n) in one ubatch (n <= n_batch).
// Returns wall seconds (sync'd).
static double prefill(Ctx & C, int seq, int pos0, int n) {
    clk::time_point t0 = clk::now();
    C.batch.n_tokens = 0;
    for (int j = 0; j < n; ++j) {
        int i = C.batch.n_tokens;
        C.batch.token[i]    = tok_of(seq, pos0 + j, C.n_vocab);
        C.batch.pos[i]      = pos0 + j;
        C.batch.n_seq_id[i] = 1;
        C.batch.seq_id[i][0]= seq;
        C.batch.logits[i]   = (j + 1 == n) ? 1 : 0;
        C.batch.n_tokens++;
    }
    if (llama_decode(C.ctx, C.batch)) { fprintf(stderr, "prefill decode failed seq=%d\n", seq); return -1; }
    llama_synchronize(C.ctx);
    return secs(t0, clk::now());
}

// One decode step (single token) for seq at pos.
static void decode1(Ctx & C, int seq, int pos) {
    C.batch.n_tokens = 1;
    C.batch.token[0] = tok_of(seq, pos, C.n_vocab);
    C.batch.pos[0]   = pos; C.batch.n_seq_id[0] = 1; C.batch.seq_id[0][0] = seq; C.batch.logits[0] = 1;
    if (llama_decode(C.ctx, C.batch)) fprintf(stderr, "decode1 failed seq=%d\n", seq);
}

int main(int argc, char ** argv) {
    std::setlocale(LC_NUMERIC, "C");
    const char * model_path = nullptr;
    for (int i = 1; i < argc; ++i) if (!strcmp(argv[i], "-m") && i + 1 < argc) model_path = argv[++i];
    if (!model_path) { fprintf(stderr, "usage: %s -m model.gguf\n", argv[0]); return 2; }

    const int NSLOT = env_i("BURST_NSLOT", 64);
    const int NPL   = env_i("NPL", 8);
    const int PP    = env_i("PP", 512);
    const int KEEP  = env_i("KEEP", 256);
    const int GEN   = env_i("GEN", 4);
    const int ngl   = env_i("PAGED_NGL", 99);
    const bool paged = getenv("LLAMA_KV_PAGED") != nullptr;

    ggml_backend_load_all();
    llama_model_params mp = llama_model_default_params();
    mp.n_gpu_layers = ngl;
    llama_model * model = llama_model_load_from_file(model_path, mp);
    if (!model) { fprintf(stderr, "model load failed\n"); return 1; }
    const llama_vocab * vocab = llama_model_get_vocab(model);
    const int n_vocab = llama_vocab_n_tokens(vocab);

    // Pool sized for the burst plus headroom so the burst fits but a later npl
    // run draws from whatever the burst's churn left behind.
    const long cells = (long) (NSLOT + NPL + 4) * (PP + GEN + 16);
    llama_context_params cp = llama_context_default_params();
    cp.n_ctx     = (uint32_t) cells;
    cp.n_batch   = (uint32_t) (PP + 16);
    cp.n_ubatch  = (uint32_t) (PP + 16);
    cp.n_seq_max = NSLOT + NPL + 2;
    cp.kv_unified = true;     // one unified stream-0 pool -> num_free(ctx) is the whole pool
    cp.no_perf   = true;
    llama_context * ctx = llama_init_from_model(model, cp);
    if (!ctx) { fprintf(stderr, "ctx init failed (cells=%ld)\n", cells); return 1; }

    Ctx C; C.ctx = ctx; C.mem = llama_get_memory(ctx); C.n_vocab = n_vocab;
    C.batch = llama_batch_init(cp.n_batch, 0, 1);

    printf("== paged-burst-bench == paged=%d NSLOT=%d NPL=%d PP=%d KEEP=%d GEN=%d n_ctx=%ld\n",
           paged, NSLOT, NPL, PP, KEEP, GEN, cells);

    llama_memory_clear(C.mem, true);
    const long F_start = paged_prefix_api::num_free_global();

    // ---- Test A: Fix-1 reclamation gap on a partial tail seq_rm --------------
    {
        prefill(C, 0, 0, PP);
        const long f_after_prefill = paged_prefix_api::num_free_global();
        llama_memory_seq_rm(C.mem, 0, KEEP, -1);          // partial tail removal
        const long f_after_rm = paged_prefix_api::num_free_global();
        llama_memory_seq_rm(C.mem, 0, -1, -1);            // full free -> pristine
        const long f_after_full = paged_prefix_api::num_free_global();
        const long bs = 16;
        const long expect = (PP + bs - 1)/bs - (KEEP + bs - 1)/bs; // trailing blocks
        printf("[TEST-A Fix-1] start=%ld afterPrefill=%ld afterPartialRm=%ld reclaimed=%ld "
               "(expect %ld post-fix, 0 pre-fix)  afterFullFree=%ld\n",
               F_start, f_after_prefill, f_after_rm, f_after_rm - f_after_prefill, expect, f_after_full);
    }

    // ---- Test B: fragmentation -> npl prefill collapse -----------------------
    // Fresh npl prefill baseline on a pristine pool.
    llama_memory_clear(C.mem, true);
    double tps_fresh;
    {
        clk::time_point t0 = clk::now();
        long ntok = 0;
        for (int s = 0; s < NPL; ++s) { double d = prefill(C, s, 0, PP); if (d < 0) return 1; ntok += PP; }
        tps_fresh = ntok / secs(t0, clk::now());
        for (int s = 0; s < NPL; ++s) llama_memory_seq_rm(C.mem, s, -1, -1);
    }
    const long F_pristine = paged_prefix_api::num_free_global();

    // High-fan-out burst: allocate NSLOT sequences, each prefilled + a few decode
    // steps (mixed alloc), then drain them in a scrambled order (odd ids first,
    // then even, each truncated before the full free) so the free queue becomes a
    // scrambled permutation - the fragmentation the bug never compacts.
    for (int s = 0; s < NSLOT; ++s) {
        if (prefill(C, NPL + s, 0, PP) < 0) return 1;
        for (int g = 0; g < GEN; ++g) decode1(C, NPL + s, PP + g);
    }
    const long F_during_burst = paged_prefix_api::num_free_global();
    // Drain: partial tail seq_rm (the reused-slot pattern) then full free, in a
    // scrambled slot order to scramble the physical free order.
    for (int parity = 1; parity >= 0; --parity)
        for (int s = 0; s < NSLOT; ++s) if ((s & 1) == parity) {
            llama_memory_seq_rm(C.mem, NPL + s, KEEP, -1);   // partial (Fix-1 path)
            llama_memory_seq_rm(C.mem, NPL + s, -1, -1);     // full free
        }
    const long F_after_drain = paged_prefix_api::num_free_global();

    // Post-burst npl prefill: pops from the (pre-fix scrambled / post-fix
    // defragged) free queue.
    double tps_post;
    {
        clk::time_point t0 = clk::now();
        long ntok = 0;
        for (int s = 0; s < NPL; ++s) { double d = prefill(C, s, 0, PP); if (d < 0) return 1; ntok += PP; }
        tps_post = ntok / secs(t0, clk::now());
        for (int s = 0; s < NPL; ++s) llama_memory_seq_rm(C.mem, s, -1, -1);
    }

    const double ratio = tps_fresh > 0 ? tps_post / tps_fresh : 0;
    printf("[TEST-B frag] num_free: start=%ld pristine=%ld duringBurst=%ld afterDrain=%ld "
           "(afterDrain==pristine? %s)\n",
           F_start, F_pristine, F_during_burst, F_after_drain,
           F_after_drain == F_pristine ? "YES" : "NO");
    printf("[TEST-B frag] prefill_tps fresh=%.1f post-burst=%.1f  ratio=%.3f "
           "(PASS if >=0.90)\n", tps_fresh, tps_post, ratio);

    // ---- Test C: idle-slot retention leak -> reclaim (the Fix-3 scenario) -----
    // Burst NSLOT sequences and leave them IDLE (stock llama-server keeps an idle
    // slot's KV; the blocks are stranded). F_idle shows the depleted pool a later
    // low-npl run would see. Then full-seq_rm each (exactly what Fix-3's
    // prompt_clear() issues at slot.release): F_reclaimed must return to pristine.
    llama_memory_clear(C.mem, true);
    // Touch the pool once so the manager exists, then read the full-pool size
    // (num_free is 0 while no manager is registered).
    if (prefill(C, 0, 0, 16) < 0) return 1;
    llama_memory_seq_rm(C.mem, 0, -1, -1);
    const long F_pre_c = paged_prefix_api::num_free_global();
    for (int s = 0; s < NSLOT; ++s) { if (prefill(C, NPL + s, 0, PP) < 0) return 1; }
    const long F_idle = paged_prefix_api::num_free_global();
    for (int s = 0; s < NSLOT; ++s) llama_memory_seq_rm(C.mem, NPL + s, -1, -1); // Fix-3 release
    const long F_reclaimed = paged_prefix_api::num_free_global();
    printf("[TEST-C idle] pristine=%ld idle_after_burst=%ld (leaked=%ld) reclaimed=%ld "
           "(returns_to_fresh? %s)\n",
           F_pre_c, F_idle, F_pre_c - F_idle, F_reclaimed,
           F_reclaimed == F_pre_c ? "YES" : "NO");

    printf("RESULT paged=%d frag_fix2_ratio=%.3f drain_numfree_returns=%s idle_reclaim_returns=%s\n",
           paged, ratio,
           F_after_drain == F_pristine ? "YES" : "NO",
           F_reclaimed == F_pre_c ? "YES" : "NO");

    llama_batch_free(C.batch);
    llama_free(ctx);
    llama_model_free(model);
    return 0;
}
