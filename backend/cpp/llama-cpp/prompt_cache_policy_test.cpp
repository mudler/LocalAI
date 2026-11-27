// SPDX-License-Identifier: MIT

#include "prompt_cache_policy.h"

#include <cstdio>
#include <map>
#include <set>
#include <string>

using namespace prompt_cache_policy;

static int failures = 0;

#define EXPECT(cond, msg)                                       \
    do {                                                        \
        if (!(cond)) {                                          \
            std::fprintf(stderr, "FAIL %s:%d: %s\n", __FILE__,  \
                         __LINE__, msg);                        \
            failures++;                                         \
        }                                                       \
    } while (0)

static constexpr uint64_t MiB = 1ull << 20;
static constexpr uint64_t GiB = 1ull << 30;

static salt_t salt_of(uint8_t v) {
    salt_t s;
    s.fill(v);
    return s;
}

// The key: the salt keeps namespaces apart, on disk too.
static void test_salt() {
    salt_t s;
    EXPECT(parse_salt("", s) && s == salt_t{}, "empty is the shared namespace: all zeros");
    EXPECT(ns_id(s) == "0000000000000000", "the shared namespace id is 16 zeros");

    const std::string hex = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff";
    EXPECT(parse_salt(hex, s), "64 lowercase hex characters parse");
    EXPECT(s[0] == 0x00 && s[1] == 0x11 && s[15] == 0xff && s[31] == 0xff, "bytes in order");
    EXPECT(ns_id(s) == "0011223344556677", "the namespace id is the first 16 hex characters");

    EXPECT(!parse_salt(hex.substr(1), s), "63 characters are refused");
    EXPECT(!parse_salt(std::string(64, 'G'), s), "non-hex is refused");
    EXPECT(!parse_salt("00112233445566778899AABBCCDDEEFF00112233445566778899aabbccddeeff", s), "uppercase is refused");

    // a zero-retention request never shares a prefix with its user's saved prompts
    const salt_t user = salt_of(0x11);
    EXPECT(private_salt(user) != user, "the private salt differs from the user's");
    EXPECT(private_salt(salt_t{}) != salt_t{}, "the private salt of the shared namespace differs from it");
    EXPECT(private_salt(user) != private_salt(salt_of(0x12)), "private salts of two users differ");
    EXPECT(ns_id(private_salt(user)) != ns_id(user), "and so do their namespace ids");
}

static void test_file_names() {
    const std::string compat = "0123456789abcdef";
    const std::string ns     = "fedcba9876543210";
    const std::string hash   = "00112233445566778899aabbccddeeff";
    const std::string name   = file_name(compat, ns, hash);
    EXPECT(name == "0123456789abcdef-fedcba9876543210-00112233445566778899aabbccddeeff.cache", "name layout");
    EXPECT(file_ns(name) == ns, "the namespace comes back from the name");
    EXPECT(file_ns(name + ".tmp").empty(), "a partial file is not a cache file");
    EXPECT(file_ns("0123456789abcdef-00112233445566778899aabbccddeeff.cache").empty(), "the upstream name without namespace is not ours");
    EXPECT(file_ns("0123456789abcdef-fedcba987654321X-00112233445566778899aabbccddeeff.cache").empty(), "non-hex is refused");
    EXPECT(file_ns("../../../etc/passwd").empty(), "a path is refused");
}

static void test_gating() {
    speeds unknown;
    EXPECT(transfer_beats_recompute(10 * GiB, 1, unknown), "unknown speeds never block");

    // 8B f16 KV, 8k tokens = 1 GiB; GPU prefill 6000 t/s = 1.37 s
    speeds nvme{ 3e9, 6000 };
    speeds sata{ 0.5e9, 6000 };
    speeds nas { 0.11e9, 6000 };
    EXPECT(transfer_beats_recompute(GiB, 8192, nvme), "NVMe beats a fast GPU");
    EXPECT(!transfer_beats_recompute(GiB, 8192, sata), "SATA loses to a fast GPU");
    EXPECT(!transfer_beats_recompute(GiB, 8192, nas), "1 GbE NAS loses to a fast GPU");

    // the same on CPU: 600 t/s = 13.7 s
    speeds cpu_nas{ 0.11e9, 600 };
    EXPECT(transfer_beats_recompute(GiB, 8192, cpu_nas), "on CPU even a slow NAS wins");

    // the fixed overhead decides for tiny entries
    EXPECT(!transfer_beats_recompute(MiB, 10, nvme), "a tiny prefix is cheaper to compute");

    // writes: at least min_tokens, and only when a read could ever win
    EXPECT(!should_write(1000, 100 * MiB, 1024, nvme), "under 1k tokens: no write");
    EXPECT(should_write(8192, GiB, 1024, nvme), "long and fast: write");
    EXPECT(!should_write(8192, GiB, 1024, nas), "slow disk: no write");
    EXPECT(should_write(8192, GiB, 1024, unknown), "unknown speeds: write");

    // reads: only the tokens the file adds over the slot and RAM count
    EXPECT(!should_read(GiB, 8192, 8192, nvme), "nothing new: no read");
    EXPECT(!should_read(GiB, 8192, 8000, nvme), "192 new tokens do not pay for 1 GiB");
    EXPECT(should_read(GiB, 8192, 0, nvme), "the whole prefix pays for it");
}

static void test_checkpoints() {
    auto k = keep_checkpoints(0);
    EXPECT(k.empty(), "no checkpoints");
    k = keep_checkpoints(2);
    EXPECT(k.size() == 2 && k[0] && k[1], "fewer than the limit: keep all");
    k = keep_checkpoints(3);
    EXPECT(k[0] && k[1] && k[2], "exactly the limit: keep all");
    k = keep_checkpoints(32);
    size_t n = 0;
    for (bool b : k) {
        n += b;
    }
    EXPECT(n == 3, "32 checkpoints: keep 3");
    EXPECT(k[0] && k[30] && k[31] && !k[1] && !k[29], "keep the first (system prompt) and the last two");
    k = keep_checkpoints(5, 0);
    EXPECT(!k[0] && !k[4], "keep 0: none");
}

static void test_meter() {
    prefill_meter m;
    m.observe(100, 0.1);
    EXPECT(m.tps == 0, "short prompts are not measured");
    m.observe(1000, 0);
    EXPECT(m.tps == 0, "no time, no measure");
    m.observe(1000, 1.0);
    EXPECT(m.tps == 1000, "the first reading is taken as is");
    m.observe(2000, 1.0);
    EXPECT(m.tps > 1000 && m.tps < 2000, "later readings are averaged");
}

static void test_retention() {
    EXPECT(!expired(1000000, 0), "ttl 0 never expires");
    EXPECT(!expired(3600, 7200), "younger than the ttl");
    EXPECT(expired(7201, 7200), "older than the ttl");

    // quota of one namespace, sizes oldest first
    EXPECT(quota_evictions({ 10, 20, 30 }, 5, 0) == 0, "no quota");
    EXPECT(quota_evictions({ 10, 20, 30 }, 5, 100) == 0, "fits");
    EXPECT(quota_evictions({ 10, 20, 30 }, 50, 100) == 1, "evict the oldest only");
    EXPECT(quota_evictions({ 10, 20, 30 }, 80, 100) == 3, "evict all three");
    EXPECT(quota_evictions({ 10, 20, 30 }, 101, 100) == -1, "larger than the quota: skip");

    EXPECT(queue_drops({ 100, 100 }, 50, 300) == 0, "the queue has room");
    EXPECT(queue_drops({ 100, 100 }, 150, 300) == 1, "drop the oldest queued");
    EXPECT(queue_drops({}, 400, 300) == -1, "never fits");

    EXPECT(queue_limit(0) == 2048 * MiB, "no RAM limit: 2 GiB");
    EXPECT(queue_limit(512 * MiB) == 256 * MiB, "small RAM cache: at least 256 MiB");
    EXPECT(queue_limit(8192 * MiB) == 2048 * MiB, "8 GiB RAM cache: a quarter is 2 GiB");
    EXPECT(queue_limit(4096 * MiB) == 1024 * MiB, "a quarter in between");
}

// The watermarks use a fake filesystem: free and total bytes as numbers.
static void test_watermarks() {
    const uint64_t big = 1000 * GiB; // low = 150 GiB, critical = 50 GiB
    EXPECT(low_watermark(big) == big / 100 * 15, "large disk: 15%");
    EXPECT(low_watermark(20 * GiB) == 10 * GiB, "small disk: at least 10 GiB");
    EXPECT(critical_watermark(big) == 50 * GiB, "critical: 5%");

    using L = disk_level;
    EXPECT(classify(500 * GiB, big, L::ok) == L::ok, "plenty of space");
    EXPECT(classify(100 * GiB, big, L::ok) == L::low, "below 15%: low");
    EXPECT(classify(40 * GiB, big, L::low) == L::critical, "below 5%: critical");
    EXPECT(classify(100 * GiB, big, L::critical) == L::critical, "from critical, stay off until above low");
    EXPECT(classify(160 * GiB, big, L::critical) == L::ok, "above low again: back on");
    EXPECT(classify(100 * GiB, big, L::low) == L::low, "low stays low");
    EXPECT(classify(0, 0, L::critical) == L::ok, "no numbers: no watermark");

    EXPECT(bytes_to_free(160 * GiB, big) == 0, "nothing to give back");
    EXPECT(bytes_to_free(100 * GiB, big) == 50 * GiB, "give back down to the low watermark");

    EXPECT(room_for(GiB, 200 * GiB, big), "a write that stays above low");
    EXPECT(!room_for(GiB, 150 * GiB, big), "a write that would cross low");
    EXPECT(!room_for(300 * GiB, 200 * GiB, big), "a write larger than the free space");
    EXPECT(room_for(GiB, 0, 0), "no numbers: allowed");

    // settable thresholds: a small disk or a test
    watermarks w;
    w.low      = 2 * GiB;
    w.critical = GiB;
    EXPECT(classify(3 * GiB, big, L::ok, w) == L::ok, "above the set low watermark");
    EXPECT(classify(1536 * MiB, big, L::ok, w) == L::low, "below the set low watermark");
    EXPECT(classify(512 * MiB, big, L::low, w) == L::critical, "below the set critical watermark");
    EXPECT(bytes_to_free(GiB + GiB / 2, big, w) == GiB / 2, "give back down to the set low watermark");
    watermarks inverted;
    inverted.low      = GiB;
    inverted.critical = 4 * GiB;
    EXPECT(critical_watermark(big, inverted) == GiB, "critical never above low");
    disk_options o;
    o.low_free_mib  = 1024;
    o.crit_free_mib = 512;
    EXPECT(watermarks_of(o).low == GiB && watermarks_of(o).critical == GiB / 2, "options in MiB");
}

// Write less: only prefixes seen in two requests.
static void test_reuse() {
    EXPECT(reused_tokens({}, 64) == 0, "no blocks");
    EXPECT(reused_tokens({ 1, 1, 1 }, 64) == 0, "seen once: not reused");
    EXPECT(reused_tokens({ 3, 2, 2, 1 }, 64) == 192, "the first three blocks were seen twice");
    EXPECT(reused_tokens({ 2, 1, 2 }, 64) == 64, "a prefix ends at the first block seen once");

    EXPECT(!likely_reused(960, 1024, 64), "under min tokens");
    EXPECT(likely_reused(1024, 1024, 64), "at min tokens");
    EXPECT(!likely_reused(0, 0, 64), "at least one block");

    seen_counter<salt_t, sha_bucket> seen(4);
    const std::vector<salt_t> sys  = { salt_of(1), salt_of(2) };
    const std::vector<salt_t> turn = { salt_of(1), salt_of(2), salt_of(3) };
    seen.observe(sys);
    EXPECT(reused_tokens(seen.counts(turn), 64) == 0, "one request: nothing is reused");
    seen.observe(turn);
    EXPECT(reused_tokens(seen.counts(turn), 64) == 128, "the shared system prompt is reused, the tail is not");

    // bounded: old hashes are forgotten, recent ones kept
    seen_counter<salt_t, sha_bucket> small(2);
    small.observe({ salt_of(10) });
    small.observe({ salt_of(10) });
    small.observe({ salt_of(11), salt_of(12) });
    small.observe({ salt_of(13), salt_of(14) });
    EXPECT(small.count(salt_of(10)) == 0, "the oldest generation is dropped");
    EXPECT(small.count(salt_of(13)) == 1, "the newest is kept");
    small.observe({ salt_of(13) });
    EXPECT(small.count(salt_of(13)) == 2, "a count survives a generation move");
}

static void test_resolve() {
    disk_options o;
    EXPECT(resolve_disk(o, "/data/prompt-cache", 8192).on, "on by default");
    EXPECT(resolve_disk(o, "", 8192).info.empty(), "no root and not asked for: off, quietly");
    EXPECT(!resolve_disk(o, "", 8192).on, "no root: off");
    o.enabled = 1;
    EXPECT(!resolve_disk(o, "", 8192).info.empty(), "asked for without a root: off, with a reason");
    EXPECT(!resolve_disk(o, "/data", 0).on, "no RAM cache: off");
    o.enabled = 0;
    EXPECT(!resolve_disk(o, "/data", 8192).on, "prompt_cache_disk:false wins");

    disk_options d;
    EXPECT(ttl_seconds(d) == 7 * 24 * 3600, "default ttl: 7 days");
    d.ttl_hours = 0;
    EXPECT(ttl_seconds(d) == 0, "ttl 0: never");
    EXPECT(min_tokens(d) == 1024, "default min tokens");
    d.min_tokens = 64;
    EXPECT(min_tokens(d) == 64, "min tokens option");
}

// The numbers a node gives every backend through the environment.
static void test_env() {
    std::map<std::string, std::string> env = {
        { "LOCALAI_PROMPT_CACHE_TTL_HOURS", "24" },
        { "LOCALAI_PROMPT_CACHE_MIN_TOKENS", "512" },
        { "LOCALAI_PROMPT_CACHE_MIN_REUSE", "3" },
        { "LOCALAI_PROMPT_CACHE_MAX_CHECKPOINTS", "1" },
        { "LOCALAI_PROMPT_CACHE_LOAD_OVERHEAD_MS", "120" },
        { "LOCALAI_PROMPT_CACHE_QUOTA_MIB", "x" },
        { "LOCALAI_PROMPT_CACHE_LOW_FREE_MIB", "-5" },
        { "LOCALAI_PROMPT_CACHE_CRITICAL_FREE_MIB", "" },
    };
    const auto get = [&](const char * n) -> const char * {
        auto it = env.find(n);
        return it == env.end() ? nullptr : it->second.c_str();
    };
    disk_options o;
    apply_env(o, get);
    EXPECT(ttl_seconds(o) == 24 * 3600, "ttl from the env");
    EXPECT(min_tokens(o) == 512, "min tokens from the env");
    EXPECT(min_reuse(o) == 3, "reuse count from the env");
    EXPECT(max_checkpoints_of(o) == 1, "checkpoints from the env");
    EXPECT(overhead_seconds(o) > 0.119 && overhead_seconds(o) < 0.121, "load overhead from the env");
    EXPECT(o.quota_mib == 0, "a malformed value is ignored");
    EXPECT(o.low_free_mib == 0, "a negative value is ignored");
    EXPECT(o.crit_free_mib == 0, "an empty value is ignored");

    disk_options d;
    apply_env(d, [](const char *) -> const char * { return nullptr; });
    EXPECT(ttl_seconds(d) == 7 * 24 * 3600 && min_tokens(d) == 1024 && min_reuse(d) == 2 &&
           max_checkpoints_of(d) == 3 && overhead_seconds(d) == load_overhead_s, "no env: the defaults");

    // a model option set after the env wins
    apply_env(d, get);
    d.min_tokens = 2048;
    EXPECT(min_tokens(d) == 2048, "a model option wins over the env");

    // the reuse count and the overhead change the decisions
    EXPECT(reused_tokens({ 2, 2, 1 }, 64, 3) == 0, "three requests needed, two seen");
    EXPECT(reused_tokens({ 3, 3, 2 }, 64, 3) == 128, "three requests seen for two blocks");
    speeds s{ 1e9, 1000 };
    EXPECT(transfer_beats_recompute(1000000, 100, s), "default overhead: a small entry pays");
    s.overhead_s = 1.0;
    EXPECT(!transfer_beats_recompute(1000000, 100, s), "a large overhead: it does not");
}

int main() {
    test_env();
    test_salt();
    test_file_names();
    test_gating();
    test_checkpoints();
    test_meter();
    test_retention();
    test_watermarks();
    test_reuse();
    test_resolve();
    if (failures > 0) {
        std::fprintf(stderr, "%d prompt cache policy check(s) failed\n", failures);
        return 1;
    }
    std::printf("prompt cache policy: all checks passed\n");
    return 0;
}
