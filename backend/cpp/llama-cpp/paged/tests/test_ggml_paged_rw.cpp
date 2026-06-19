// Phase 1 integration test: prove the paged KV write+read MECHANISM at the
// ggml-op level, driven by PagedKVManager.
//
//   write:  ggml_set_rows(pool, k_src, slot_mapping)   // scatter by slot
//   read:   ggml_get_rows(pool, gather_idx)            // gather seq's slots
//
// The decisive property: a sequence's physical blocks are NON-CONTIGUOUS and
// OUT-OF-ORDER (forced via allocate/free/reallocate), yet gather(write(x)) == x,
// and a second sequence written into disjoint blocks does not contaminate it.
// This is exactly how a paged read path feeds contiguous scratch to attention.

#include "../paged_kv_manager.h"

#include "ggml.h"
#include "ggml-cpu.h"
#include "ggml-alloc.h"
#include "ggml-backend.h"

#include <cassert>
#include <cstdio>
#include <cmath>
#include <vector>

using namespace paged;

int main() {
    const int n_embd      = 8;
    const int block_size  = 16;
    const int num_blocks  = 8;                       // block 0 reserved as null
    const int total_slots = block_size * num_blocks; // 128

    // --- Force a non-contiguous, out-of-order block layout for seqC ----------
    PagedKVManager m(num_blocks, block_size, /*enable_caching=*/false);
    assert(m.allocate(/*seqA=*/0, 2 * block_size)); // blocks {1,2}
    assert(m.allocate(/*seqB=*/1, 2 * block_size)); // blocks {3,4}
    m.free(0);                                       // returns {1,2} to free list
    assert(m.allocate(/*seqC=*/2, 3 * block_size));  // reuses freed blocks, reordered

    auto btC = m.block_table(2);
    auto btB = m.block_table(1);
    printf("seqC block_table = [");
    for (size_t i = 0; i < btC.size(); ++i) printf("%s%d", i ? "," : "", btC[i]);
    printf("]\n");
    assert(btC.size() == 3);
    // sanity: seqC and seqB occupy disjoint physical blocks
    for (int cb : btC) for (int bb : btB) assert(cb != bb);

    const int n_tokens = 3 * block_size; // 48 tokens for seqC

    // slot_mapping for seqC positions 0..n_tokens-1
    std::vector<int> positions(n_tokens);
    for (int i = 0; i < n_tokens; ++i) positions[i] = i;
    std::vector<int64_t> slots64 = m.slot_mapping(2, positions); // I64 for set_rows
    std::vector<int32_t> slots32(slots64.begin(), slots64.end()); // I32 for get_rows

    // seqB occupies different blocks; write a sentinel there to prove isolation.
    std::vector<int> posB(2 * block_size);
    for (size_t i = 0; i < posB.size(); ++i) posB[i] = (int) i;
    std::vector<int64_t> slotsB64 = m.slot_mapping(1, posB);

    // --- ggml backend + persistent (statically allocated) tensors ------------
    ggml_backend_t backend = ggml_backend_cpu_init();
    assert(backend);

    struct ggml_init_params dp = { /*mem_size=*/ ggml_tensor_overhead() * 16,
                                   /*mem_buffer=*/ NULL, /*no_alloc=*/ true };
    struct ggml_context * ctx_data = ggml_init(dp);

    // The shared paged KV pool: one flat block pool, exactly like a paged layer.
    struct ggml_tensor * pool    = ggml_new_tensor_2d(ctx_data, GGML_TYPE_F32, n_embd, total_slots);
    struct ggml_tensor * k_src   = ggml_new_tensor_2d(ctx_data, GGML_TYPE_F32, n_embd, n_tokens);
    struct ggml_tensor * w_idx   = ggml_new_tensor_1d(ctx_data, GGML_TYPE_I64, n_tokens);
    struct ggml_tensor * g_idx   = ggml_new_tensor_1d(ctx_data, GGML_TYPE_I32, n_tokens);
    struct ggml_tensor * kB_src  = ggml_new_tensor_2d(ctx_data, GGML_TYPE_F32, n_embd, (int) posB.size());
    struct ggml_tensor * wB_idx  = ggml_new_tensor_1d(ctx_data, GGML_TYPE_I64, (int) posB.size());

    ggml_backend_buffer_t buf = ggml_backend_alloc_ctx_tensors(ctx_data, backend);
    assert(buf);

    // pool starts zeroed
    std::vector<float> zeros(n_embd * total_slots, 0.0f);
    ggml_backend_tensor_set(pool, zeros.data(), 0, ggml_nbytes(pool));

    // token t carries the value (float) t in every embedding lane -> easy to verify
    std::vector<float> ksrc(n_embd * n_tokens);
    for (int t = 0; t < n_tokens; ++t)
        for (int e = 0; e < n_embd; ++e) ksrc[t * n_embd + e] = (float) t;
    ggml_backend_tensor_set(k_src, ksrc.data(), 0, ggml_nbytes(k_src));
    ggml_backend_tensor_set(w_idx, slots64.data(), 0, ggml_nbytes(w_idx));
    ggml_backend_tensor_set(g_idx, slots32.data(), 0, ggml_nbytes(g_idx));

    // seqB sentinel = 999 everywhere
    std::vector<float> kBsrc(n_embd * posB.size(), 999.0f);
    ggml_backend_tensor_set(kB_src, kBsrc.data(), 0, ggml_nbytes(kB_src));
    ggml_backend_tensor_set(wB_idx, slotsB64.data(), 0, ggml_nbytes(wB_idx));

    // --- compute graph: write seqB, write seqC, then gather seqC -------------
    struct ggml_init_params cp = { /*mem_size=*/ ggml_tensor_overhead() * 32 + ggml_graph_overhead(),
                                   /*mem_buffer=*/ NULL, /*no_alloc=*/ true };
    struct ggml_context * ctx = ggml_init(cp);

    struct ggml_tensor * wroteB = ggml_set_rows(ctx, pool,   kB_src, wB_idx); // view(pool)
    struct ggml_tensor * wroteC = ggml_set_rows(ctx, wroteB, k_src,  w_idx);  // chain so order is fixed
    struct ggml_tensor * gathered = ggml_get_rows(ctx, wroteC, g_idx);
    ggml_set_output(gathered);

    struct ggml_cgraph * gf = ggml_new_graph(ctx);
    ggml_build_forward_expand(gf, gathered);

    ggml_gallocr_t galloc = ggml_gallocr_new(ggml_backend_cpu_buffer_type());
    assert(ggml_gallocr_alloc_graph(galloc, gf));

    assert(ggml_backend_graph_compute(backend, gf) == GGML_STATUS_SUCCESS);

    // --- verify gather(write(x)) == x for the non-contiguous sequence --------
    std::vector<float> out(n_embd * n_tokens);
    ggml_backend_tensor_get(gathered, out.data(), 0, ggml_nbytes(gathered));

    int mism = 0;
    for (int t = 0; t < n_tokens; ++t)
        for (int e = 0; e < n_embd; ++e)
            if (std::fabs(out[t * n_embd + e] - (float) t) > 1e-6f) mism++;
    assert(mism == 0 && "gathered paged KV must equal source (round-trip)");

    // --- verify isolation: read seqC slots directly from pool, unaffected by seqB
    std::vector<float> pool_host(n_embd * total_slots);
    ggml_backend_tensor_get(pool, pool_host.data(), 0, ggml_nbytes(pool));
    for (int t = 0; t < n_tokens; ++t) {
        int slot = (int) slots64[t];
        for (int e = 0; e < n_embd; ++e)
            assert(std::fabs(pool_host[slot * n_embd + e] - (float) t) < 1e-6f);
    }

    ggml_gallocr_free(galloc);
    ggml_free(ctx);
    ggml_free(ctx_data);
    ggml_backend_buffer_free(buf);
    ggml_backend_free(backend);

    printf("test_ggml_paged_rw: OK (non-contiguous paged write/gather round-trip)\n");
    return 0;
}
