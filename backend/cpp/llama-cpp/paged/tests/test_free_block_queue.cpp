#include "../paged_kv_manager.h"
#include <cassert>
#include <cstdio>
#include <vector>

using namespace paged;

static std::vector<KVCacheBlock> make_blocks(int n) {
    std::vector<KVCacheBlock> v;
    v.reserve(n);
    for (int i = 0; i < n; ++i) v.push_back(KVCacheBlock{i});
    return v;
}

int main() {
    // ordered 0..9 at init; popleft yields ascending block_ids
    auto blocks = make_blocks(10);
    std::vector<KVCacheBlock*> ptrs;
    for (auto& b : blocks) ptrs.push_back(&b);
    FreeBlockQueue q(ptrs);
    assert(q.num_free_blocks == 10);

    KVCacheBlock* b0 = q.popleft();
    assert(b0->block_id == 0);
    assert(q.num_free_blocks == 9);

    auto two = q.popleft_n(2);            // {1,2}
    assert(two.size() == 2 && two[0]->block_id == 1 && two[1]->block_id == 2);
    assert(q.num_free_blocks == 7);

    // O(1) middle removal: remove block 5 (currently free), count drops
    q.remove(ptrs[5]);
    assert(q.num_free_blocks == 6);       // free: 3,4,6,7,8,9

    // append puts a block at the tail; it comes back out only after the rest
    q.append(b0);                          // free order now: 3,4,6,7,8,9,0
    assert(q.num_free_blocks == 7);
    auto all = q.get_all_free_blocks();
    assert(all.front()->block_id == 3);
    assert(all.back()->block_id == 0);

    printf("test_free_block_queue: OK\n");
    return 0;
}
