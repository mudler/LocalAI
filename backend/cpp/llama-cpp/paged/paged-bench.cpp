// paged-bench: quantify the multi-tenant wins of paged KV allocation that are
// properties of the host-side block model (vLLM-parity), independent of the
// in-model compute path.
//
//   Win 1 (capacity):       on-demand block allocation vs contiguous per-seq
//                           reservation, under a fixed KV block budget.
//   Win 3 (prefix sharing): automatic cross-tenant prefix dedup via block
//                           hashing.
//
// Win 2 (throughput) is intentionally NOT here: it requires the paged read
// path wired into llama-graph.cpp (Gate 0). Measuring it at this layer would
// be dishonest, so it is reported as pending.

#include "paged_kv_manager.h"

#include <cstdio>
#include <vector>
#include <numeric>

using namespace paged;

// A deterministic LCG so sequence lengths vary without Math.random-style nondeterminism.
struct Lcg {
    uint64_t s;
    explicit Lcg(uint64_t seed) : s(seed) {}
    uint32_t next() { s = s * 6364136223846793005ULL + 1442695040888963407ULL; return (uint32_t)(s >> 33); }
    int range(int lo, int hi) { return lo + (int)(next() % (uint32_t)(hi - lo + 1)); }
};

static size_t cdiv(size_t a, size_t b) { return (a + b - 1) / b; }

int main() {
    const int block_size = 16;
    const int n_ctx      = 2048;   // max context a sequence could use
    const int num_blocks = 512;    // fixed KV budget: 512 blocks * 16 = 8192 cells

    printf("paged-bench  (block_size=%d, n_ctx=%d, budget=%d blocks = %d cells)\n\n",
           block_size, n_ctx, num_blocks, num_blocks * block_size);

    // ---------------------------------------------------------------------
    // WIN 1: concurrency capacity. Sequences have realistic, VARYING lengths
    // (most short, a few long) - the regime where reserving n_ctx per seq
    // wastes the most. Count how many fit under the same block budget.
    // ---------------------------------------------------------------------
    {
        Lcg rng(12345);
        const int blocks_per_ctx = (int) cdiv(n_ctx, block_size); // contiguous reserves this per seq

        // Contiguous (stream-style) reservation: every seq reserves n_ctx worth.
        int contiguous_fit = num_blocks / blocks_per_ctx;

        // Paged on-demand: draw real lengths until the pool is exhausted.
        PagedKVManager m(num_blocks, block_size, /*enable_caching=*/false);
        int paged_fit = 0;
        long total_tokens = 0;
        for (int seq = 0; ; ++seq) {
            // 80% short (8-128 tok), 20% long (up to n_ctx)
            int len = (rng.range(0, 99) < 80) ? rng.range(8, 128) : rng.range(128, n_ctx);
            if (!m.allocate(seq, (size_t) len)) break;
            paged_fit++;
            total_tokens += len;
        }

        printf("WIN 1  concurrency capacity @ %d-block budget\n", num_blocks);
        printf("  contiguous (reserve n_ctx/seq): %d sequences\n", contiguous_fit);
        printf("  paged (on-demand blocks):       %d sequences  (avg %ld tok/seq)\n",
               paged_fit, paged_fit ? total_tokens / paged_fit : 0);
        printf("  --> paged fits %.1fx more concurrent sequences\n\n",
               contiguous_fit ? (double) paged_fit / contiguous_fit : 0.0);
    }

    // ---------------------------------------------------------------------
    // WIN 3: cross-tenant prefix sharing. N tenants share a long system
    // prompt / RAG context, then diverge. Compare physical blocks consumed
    // with prefix caching on vs off.
    // ---------------------------------------------------------------------
    {
        const int n_tenants    = 32;
        const int shared_len   = 1024;  // shared system prompt (64 blocks)
        const int distinct_len = 64;    // per-tenant suffix (4 blocks)

        // Shared prefix token ids (identical across tenants -> identical block hashes).
        std::vector<int> shared(shared_len);
        for (int i = 0; i < shared_len; ++i) shared[i] = 1000 + i;

        // --- prefix caching OFF: every tenant pays for the whole prefix ---
        long blocks_off = 0;
        {
            PagedKVManager m(num_blocks * 8, block_size, /*enable_caching=*/false);
            for (int t = 0; t < n_tenants; ++t) {
                m.allocate(t, (size_t) (shared_len + distinct_len));
                blocks_off += m.block_table(t).size();
            }
        }

        // --- prefix caching ON: shared blocks are deduped to one physical copy ---
        long blocks_on = 0;
        {
            PagedKVManager m(num_blocks * 8, block_size, /*enable_caching=*/true);
            // tenant 0 fills + caches the shared prefix
            auto h = m.compute_block_hashes(shared);
            m.allocate(0, (size_t) (shared_len + distinct_len));
            m.cache_blocks(0, h, (size_t) shared_len);
            long physical = m.block_table(0).size();
            // tenants 1..N-1 hit the cached prefix; only their distinct suffix is new
            for (int t = 1; t < n_tenants; ++t) {
                size_t cached_tokens = m.get_computed_blocks(h); // shared blocks reused
                size_t new_tokens = (shared_len - cached_tokens) + distinct_len;
                m.allocate(t, (size_t) (shared_len + distinct_len));
                // physically new blocks = only what wasn't already resident
                physical += (long) cdiv(new_tokens, block_size);
            }
            blocks_on = physical;
        }

        printf("WIN 3  cross-tenant prefix sharing (%d tenants, %d-tok shared prefix)\n",
               n_tenants, shared_len);
        printf("  prefix-cache OFF: %ld physical blocks\n", blocks_off);
        printf("  prefix-cache ON:  %ld physical blocks\n", blocks_on);
        printf("  --> %.1fx less KV memory for the shared workload\n\n",
               blocks_on ? (double) blocks_off / blocks_on : 0.0);
    }

    printf("WIN 2  aggregate throughput under load: PENDING\n");
    printf("  Requires the paged gather-read path wired into llama-graph.cpp\n");
    printf("  (Gate 0) to measure tok/s vs concurrency. Not measurable at the\n");
    printf("  allocation layer; not reported here to avoid overclaiming.\n");
    return 0;
}
