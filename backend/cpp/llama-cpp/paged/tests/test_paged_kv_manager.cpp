#include "../paged_kv_manager.h"
#include <cassert>
#include <cstdio>
using namespace paged;

int main() {
    PagedKVManager m(/*num_blocks=*/8, /*block_size=*/16, /*enable_caching=*/false);
    // 20 tokens -> ceil(20/16)=2 blocks
    assert(m.allocate(/*seq=*/0, 20));
    auto bt = m.block_table(0);
    assert(bt.size() == 2);

    // slot arithmetic: pos 0 -> block bt[0]*16 + 0 ; pos 17 -> bt[1]*16 + 1
    assert(m.slot(0, 0)  == (int64_t)bt[0] * 16 + 0);
    assert(m.slot(0, 17) == (int64_t)bt[1] * 16 + 1);

    auto sm = m.slot_mapping(0, {0, 16, 17});
    assert(sm.size() == 3 && sm[1] == (int64_t)bt[1] * 16 + 0);

    // growing the same seq reuses existing blocks, adds only new ones
    assert(m.allocate(0, 40)); // ceil(40/16)=3 -> +1 block
    assert(m.block_table(0).size() == 3);

    // OOM: blocks left = 8 - 1(null) - 3 = 4 blocks; ask for 5 blocks
    assert(m.allocate(1, 5 * 16) == false);

    // free returns blocks to the pool for reuse
    m.free(0);
    assert(m.allocate(1, 5 * 16)); // now fits
    printf("test_paged_kv_manager: OK\n");
    return 0;
}
