#include "../paged_kv_manager.h"
#include <cassert>
#include <cstdio>
using namespace paged;

int main() {
    BlockPool pool(/*num_blocks=*/8, /*enable_caching=*/true);
    // block 0 is reserved as null_block (vLLM pops one at init)
    assert(pool.null_block != nullptr && pool.null_block->block_id == 0);
    assert(pool.get_num_free_blocks() == 7);

    // get_new_blocks sets ref_cnt=1 and removes from free list
    auto b = pool.get_new_blocks(2);
    assert(b.size() == 2 && b[0]->ref_cnt == 1 && b[1]->ref_cnt == 1);
    assert(pool.get_num_free_blocks() == 5);

    // cache two full blocks with chained hashes, then look them up
    std::vector<uint64_t> hashes = {1111, 2222};
    pool.cache_full_blocks(b, /*num_cached=*/0, /*num_full=*/2, hashes);
    assert(b[0]->has_hash && b[0]->block_hash == 1111);
    assert(pool.get_cached_block(1111) == b[0]);
    assert(pool.get_cached_block(2222) == b[1]);
    assert(pool.get_cached_block(9999) == nullptr);

    // free: hashed blocks go to tail (kept warm), so they remain queryable.
    pool.free_blocks(b);
    assert(b[0]->ref_cnt == 0);
    assert(pool.get_num_free_blocks() == 7);
    assert(pool.get_cached_block(1111) == b[0]); // still cached/warm

    // touch a warm cached block: pulls it out of free list, ++ref_cnt
    pool.touch({b[0]});
    assert(b[0]->ref_cnt == 1);
    assert(pool.get_num_free_blocks() == 6);

    // exhausting the pool then allocating evicts a warm cached hash
    auto rest = pool.get_new_blocks(pool.get_num_free_blocks());
    (void) rest;
    assert(pool.get_cached_block(2222) == nullptr); // evicted on reuse
    printf("test_block_pool: OK\n");
    return 0;
}
