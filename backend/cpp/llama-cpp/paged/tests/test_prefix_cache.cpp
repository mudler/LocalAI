#include "../paged_kv_manager.h"
#include <cassert>
#include <cstdio>
#include <vector>
using namespace paged;

int main() {
    PagedKVManager m(/*num_blocks=*/64, /*block_size=*/16, /*enable_caching=*/true);

    // shared prefix of 32 tokens (2 full blocks) + distinct suffix
    std::vector<int> shared(32);
    for (int i = 0; i < 32; ++i) shared[i] = 100 + i;

    // chained hashing is deterministic and prefix-sensitive
    auto h = m.compute_block_hashes(shared);
    assert(h.size() == 2);
    auto h2 = m.compute_block_hashes(shared);
    assert(h == h2);                          // deterministic
    std::vector<int> other = shared; other[0] = 999;
    assert(m.compute_block_hashes(other)[0] != h[0]); // sensitive to content

    // seq 0: cold, no cache hit yet
    assert(m.get_computed_blocks(h) == 0);
    assert(m.allocate(0, 32));
    m.cache_blocks(0, h, 32);

    // seq 1: warm — the 2 shared blocks are a cache hit (32 tokens)
    assert(m.get_computed_blocks(h) == 32);

    // first-miss stop: a chain that diverges after block 1 hits only 1 block
    auto hmix = h; hmix[1] = 0xDEADBEEF;
    assert(m.get_computed_blocks(hmix) == 16);
    printf("test_prefix_cache: OK\n");
    return 0;
}
