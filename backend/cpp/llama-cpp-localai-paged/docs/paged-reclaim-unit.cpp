// Host-side unit test for the paged-pool burst-reclaim fix (patch 0024).
// Compiles paged-kv-manager.cpp directly; no ggml / llama / GPU dependency.
//
//   Fix-1  PagedKVManager::truncate(seq, n_keep) reclaims the trailing blocks
//          beyond ceil(n_keep/bs) (ref-counted), so a partial tail seq_rm no
//          longer strands blocks whose cells were cleared.
//   Fix-2  defrag_free_pool() relinks the free queue into ascending block-id
//          order once the pool is fully idle, undoing a burst's scrambled frees
//          so a later prefill pops physically contiguous blocks again.

#include "paged-kv-manager.h"
#include <cstdio>

using paged::PagedKVManager;

int main() {
    int rc = 0;

    // ---- Fix-1: truncate reclaims the trailing block suffix -----------------
    {
        PagedKVManager m(/*num_blocks=*/64, /*block_size=*/16, /*caching=*/true);
        const size_t f0 = m.num_free_blocks();   // 63 (block 0 reserved as null)
        m.allocate(0, 512);                       // ceil(512/16)=32 blocks
        const size_t f1 = m.num_free_blocks();    // 31
        m.truncate(0, 256);                       // keep ceil(256/16)=16, free 16
        const size_t f2 = m.num_free_blocks();    // 47
        printf("[unit Fix-1] free=%zu alloc512=%zu truncate256=%zu reclaimed=%zu (expect 16)\n",
               f0, f1, f2, f2 - f1);
        if (f2 - f1 != 16) rc = 1;
        m.truncate(0, 16);                        // keep 1 block, free 15 more
        const size_t f3 = m.num_free_blocks();    // 62
        printf("[unit Fix-1] truncate16=%zu (expect %zu)\n", f3, f0 - 1);
        if (f3 != f0 - 1) rc = 1;
        m.free(0);
        if (m.num_free_blocks() != f0) { printf("[unit Fix-1] free mismatch\n"); rc = 1; }
    }

    // ---- Fix-2: defrag restores ascending popleft order ---------------------
    {
        PagedKVManager m(/*num_blocks=*/64, /*block_size=*/16, /*caching=*/false);
        for (int s = 0; s < 8; ++s) m.allocate(s, 16);          // pop blocks 1..8
        const int scrambled[8] = {3, 7, 1, 5, 0, 6, 2, 4};      // free out of order
        for (int i = 0; i < 8; ++i) m.free(scrambled[i]);
        m.defrag_free_pool();                                    // all idle -> compact
        m.allocate(100, 16 * 3);                                 // pop 3 blocks
        const auto bt = m.block_table(100);
        bool asc = true;
        printf("[unit Fix-2] post-defrag block_table:");
        for (size_t i = 0; i < bt.size(); ++i) {
            printf(" %d", bt[i]);
            if (i && bt[i] < bt[i - 1]) asc = false;
        }
        printf("  ascending=%s (expect YES)\n", asc ? "YES" : "NO");
        if (!asc) rc = 1;
    }

    printf("UNIT %s\n", rc == 0 ? "PASS" : "FAIL");
    return rc;
}
