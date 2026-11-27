// Decisions of the disk prompt cache (patches/0007-prompt-cache-disk-localai.patch).
//
// LocalAI-owned and free of llama.cpp types, so prompt_cache_policy_test.cpp
// tests it without a build of the backend. prepare.sh copies it next to
// tools/server/server-slot-sched.cpp, which includes it; the hooks only
// collect sizes, times and names and act on these answers.
#pragma once

#include <algorithm>
#include <array>
#include <cstddef>
#include <cstdint>
#include <cstdlib>
#include <string>
#include <unordered_map>
#include <vector>

namespace prompt_cache_policy {

using salt_t = std::array<uint8_t, 32>;

// A load costs more than the read: the state is parsed, then copied to the
// device. Rough, and on the safe side for small entries.
constexpr double load_overhead_s = 0.05;

// Prompts shorter than this are never written: on a GPU the saving is below
// about 0.2 s and every save costs a write.
constexpr uint64_t default_min_tokens = 1024;

// Prompt speed is measured on prompts at least this long; short prompts are
// dominated by fixed costs and would underestimate it.
constexpr uint64_t meter_min_tokens = 256;

// Hybrid and recurrent models: a checkpoint is a whole recurrent state (tens
// of MiB), so a file keeps only this many.
constexpr size_t max_checkpoints = 3;

// Transfer speed of the cache tier and prompt speed of the model. 0 = not
// known yet. The rule is "transfer time against recompute time", so the same
// gate fits a network tier: there transfer_bps is the measured network read
// speed.
struct speeds {
    double transfer_bps = 0;               // bytes per second, cold read of the cache tier
    double prefill_tps  = 0;               // prompt tokens per second of this model
    double overhead_s   = load_overhead_s; // fixed cost of one load besides the transfer
};

// True when fetching `bytes` from the cache tier is faster than computing
// `n_tokens` prompt tokens again. Unknown speeds do not block: the first start of a model
// has no prompt speed yet, and the gate is meant to stop slow disks, not to
// delay the first hit.
inline bool transfer_beats_recompute(uint64_t bytes, uint64_t n_tokens, const speeds & s) {
    if (s.transfer_bps <= 0 || s.prefill_tps <= 0) {
        return true;
    }
    const double t_read      = (double) bytes / s.transfer_bps + s.overhead_s;
    const double t_recompute = (double) n_tokens / s.prefill_tps;
    return t_read < t_recompute;
}

// Write an entry only if it is long enough and could ever be read back faster
// than it is computed.
inline bool should_write(uint64_t n_tokens, uint64_t bytes, uint64_t min_tokens, const speeds & s) {
    return n_tokens >= min_tokens && transfer_beats_recompute(bytes, n_tokens, s);
}

// Read an entry only if the tokens it adds over what the slot or the RAM cache
// already give (n_have) are cheaper to read than to compute.
inline bool should_read(uint64_t bytes, uint64_t n_reused, uint64_t n_have, const speeds & s) {
    if (n_reused <= n_have) {
        return false;
    }
    return transfer_beats_recompute(bytes, n_reused - n_have, s);
}

// Which checkpoints of a prompt go to disk, oldest first. The first one sits
// near the end of the first user message (system prompt and tools: "same
// agent, new conversation"); the last ones serve "same conversation, next
// turn".
inline std::vector<bool> keep_checkpoints(size_t n, size_t keep = max_checkpoints) {
    std::vector<bool> res(n, false);
    if (keep == 0 || n == 0) {
        return res;
    }
    if (n <= keep) {
        std::fill(res.begin(), res.end(), true);
        return res;
    }
    res[0] = true;
    for (size_t i = n - (keep - 1); i < n; i++) {
        res[i] = true;
    }
    return res;
}

// Moving average of the prompt speed.
struct prefill_meter {
    double tps = 0;

    void observe(uint64_t n_tokens, double seconds) {
        if (n_tokens < meter_min_tokens || seconds <= 0) {
            return;
        }
        const double v = (double) n_tokens / seconds;
        tps = tps <= 0 ? v : 0.7 * tps + 0.3 * v;
    }
};

// An entry unused for longer than ttl_s is expired. 0 = never.
inline bool expired(int64_t age_s, int64_t ttl_s) {
    return ttl_s > 0 && age_s > ttl_s;
}

// Entries to evict so that a namespace with these entries (sizes, least
// recently used first) can take `incoming` more bytes under `quota` (0 = no
// quota). Returns the count of the oldest entries to remove, or -1 when the
// incoming entry alone exceeds the quota.
inline long quota_evictions(const std::vector<uint64_t> & sizes, uint64_t incoming, uint64_t quota) {
    if (quota == 0) {
        return 0;
    }
    if (incoming > quota) {
        return -1;
    }
    uint64_t total = 0;
    for (auto v : sizes) {
        total += v;
    }
    long n = 0;
    while (total + incoming > quota && n < (long) sizes.size()) {
        total -= sizes[n];
        n++;
    }
    return n;
}

// Entries of the write queue to drop (oldest first) to queue one more of size
// `incoming` under `limit` bytes. -1 when it can never fit.
inline long queue_drops(const std::vector<uint64_t> & queued, uint64_t incoming, uint64_t limit) {
    return quota_evictions(queued, incoming, limit);
}

// Size limit of the write queue: a quarter of the RAM cache, between 256 MiB
// and 2 GiB. The queue holds states that left the RAM cache and wait for the
// disk, so it adds to cache_ram.
inline uint64_t queue_limit(uint64_t cache_ram_bytes) {
    const uint64_t lo = 256ull << 20;
    const uint64_t hi = 2048ull << 20;
    if (cache_ram_bytes == 0) {
        return hi;
    }
    return std::min(hi, std::max(lo, cache_ram_bytes / 4));
}

inline std::string to_hex(const uint8_t * data, size_t n) {
    static const char hex[] = "0123456789abcdef";
    std::string res;
    res.reserve(2 * n);
    for (size_t i = 0; i < n; i++) {
        res += hex[data[i] >> 4];
        res += hex[data[i] & 0xf];
    }
    return res;
}

// Parses the salt that LocalAI sends (64 hex characters). Empty is the shared
// namespace (all zeros). Anything else is refused.
inline bool parse_salt(const std::string & hex, salt_t & out) {
    out.fill(0);
    if (hex.empty()) {
        return true;
    }
    if (hex.size() != 64) {
        return false;
    }
    for (size_t i = 0; i < 32; i++) {
        uint8_t v = 0;
        for (size_t j = 0; j < 2; j++) {
            const char c = hex[2 * i + j];
            v <<= 4;
            if (c >= '0' && c <= '9') {
                v |= c - '0';
            } else if (c >= 'a' && c <= 'f') {
                v |= c - 'a' + 10;
            } else {
                return false;
            }
        }
        out[i] = v;
    }
    return true;
}

// The salt of a request that must not reach disk (zero-retention, incognito).
// Its prompts must never share a prefix with the prompts of the same user that
// may be saved, otherwise a later saved prompt would carry its cells to disk.
inline salt_t private_salt(const salt_t & salt) {
    salt_t res = salt;
    for (auto & b : res) {
        b ^= 0x5a;
    }
    return res;
}

// The namespace part of a file name: the first 16 hex characters of the salt,
// 16 zeros for the shared namespace. LocalAI computes the same value to purge
// or to apply a quota to one namespace.
inline std::string ns_id(const salt_t & salt) {
    return to_hex(salt.data(), 8);
}

// <compat16>-<ns16>-<hash32>.cache
inline std::string file_name(const std::string & compat, const std::string & ns, const std::string & hash32) {
    return compat + "-" + ns + "-" + hash32 + ".cache";
}

// The namespace of a file name, or "" when the name is not a cache file name.
inline std::string file_ns(const std::string & name) {
    // 16 + 1 + 16 + 1 + 32 + 6
    if (name.size() != 72 || name[16] != '-' || name[33] != '-' || name.compare(66, 6, ".cache") != 0) {
        return "";
    }
    for (size_t i = 0; i < 66; i++) {
        if (i == 16 || i == 33) {
            continue;
        }
        const char c = name[i];
        if (!((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'))) {
            return "";
        }
    }
    return name.substr(17, 16);
}

// Free space of the filesystem that holds the cache. LocalAI's janitor uses
// the same numbers (pkg/promptcache), so the backend and the janitor agree on
// when the cache pauses.
enum class disk_level {
    ok,       // writes allowed
    low,      // below the low watermark: no new writes, evict down to it
    critical, // below the critical watermark: the disk tier is off
};

constexpr uint64_t low_watermark_min = 10ull << 30; // 10 GiB

// Free-space thresholds in bytes. 0 keeps the default rule: low = the larger
// of 15% of the filesystem and 10 GiB, critical = 5%.
struct watermarks {
    uint64_t low      = 0;
    uint64_t critical = 0;
};

inline uint64_t low_watermark(uint64_t total, const watermarks & w = {}) {
    if (w.low > 0) {
        return w.low;
    }
    return std::max<uint64_t>(total / 100 * 15, low_watermark_min);
}

inline uint64_t critical_watermark(uint64_t total, const watermarks & w = {}) {
    const uint64_t c = w.critical > 0 ? w.critical : total / 100 * 5;
    return std::min(c, low_watermark(total, w)); // never above low
}

// The level after a new reading. Back to ok only above the low watermark, so
// a disk near a threshold does not flip on every write.
inline disk_level classify(uint64_t free, uint64_t total, disk_level prev, const watermarks & w = {}) {
    if (total == 0) {
        return disk_level::ok; // unknown: the filesystem gave no numbers
    }
    if (free < critical_watermark(total, w)) {
        return disk_level::critical;
    }
    if (free < low_watermark(total, w)) {
        return prev == disk_level::critical ? disk_level::critical : disk_level::low;
    }
    return disk_level::ok;
}

// Bytes the cache must give back to bring the free space up to the low
// watermark. 0 when there is enough free space.
inline uint64_t bytes_to_free(uint64_t free, uint64_t total, const watermarks & w = {}) {
    if (total == 0) {
        return 0;
    }
    const uint64_t low = low_watermark(total, w);
    return free < low ? low - free : 0;
}

// Can a write of `bytes` go ahead without crossing the low watermark?
inline bool room_for(uint64_t bytes, uint64_t free, uint64_t total, const watermarks & w = {}) {
    return total == 0 || (free > bytes && free - bytes >= low_watermark(total, w));
}

// Requests that must share a prefix before it is written.
constexpr uint32_t default_min_reuse = 2;

// Write less: an entry is worth writing only when its prefix was seen in at
// least min_reuse (2) requests (a multi-turn conversation, or a system or tool prefix
// that several requests share). A unique one-shot prompt is never written.
// counts[k] is the number of requests whose prompt held block k of the
// entry's hash chain. Returns the tokens of the longest such prefix.
inline uint64_t reused_tokens(const std::vector<uint32_t> & counts, uint64_t block, uint32_t min_reuse = default_min_reuse) {
    uint64_t k = 0;
    while (k < counts.size() && counts[k] >= std::max<uint32_t>(1, min_reuse)) {
        k++;
    }
    return k * block;
}

inline bool likely_reused(uint64_t reused, uint64_t min_tokens, uint64_t block) {
    return reused >= std::max(min_tokens, block);
}

// Counts the requests that held each prefix block hash. Bounded: two
// generations of at most `cap` hashes each; when the current one is full the
// older one is dropped, so a hash not seen for a while is forgotten.
template <typename Hash, typename HashFn>
struct seen_counter {
    explicit seen_counter(size_t cap = 100000) : cap(cap) {}

    void observe(const std::vector<Hash> & chain) {
        for (const auto & h : chain) {
            if (cur.size() >= cap) {
                old = std::move(cur);
                cur.clear();
            }
            uint32_t prev = 0;
            auto it_old = old.find(h);
            if (it_old != old.end()) {
                prev = it_old->second;
                old.erase(it_old);
            }
            cur[h] += prev + 1;
        }
    }

    uint32_t count(const Hash & h) const {
        uint32_t res = 0;
        auto it = cur.find(h);
        if (it != cur.end()) {
            res += it->second;
        }
        it = old.find(h);
        if (it != old.end()) {
            res += it->second;
        }
        return res;
    }

    std::vector<uint32_t> counts(const std::vector<Hash> & chain) const {
        std::vector<uint32_t> res;
        res.reserve(chain.size());
        for (const auto & h : chain) {
            res.push_back(count(h));
        }
        return res;
    }

private:
    size_t cap;
    std::unordered_map<Hash, uint32_t, HashFn> cur;
    std::unordered_map<Hash, uint32_t, HashFn> old;
};

// The first 8 bytes of a SHA-256 are a good enough bucket hash.
struct sha_bucket {
    size_t operator()(const salt_t & h) const {
        uint64_t v = 0;
        for (size_t i = 0; i < 8; i++) {
            v = (v << 8) | h[i];
        }
        return (size_t) v;
    }
};

// The model options of the disk tier. Unset fields keep the defaults.
struct disk_options {
    int  enabled        = -1; // -1 unset, 0 off, 1 on
    long ttl_hours      = -1; // -1 unset (7 days), 0 never expires
    long min_tokens     = -1; // -1 unset (default_min_tokens)
    long quota_mib      = 0;  // per cache namespace, 0 none
    long max_size_mib   = 0;  // of this model's directory, 0 none
    long low_free_mib   = 0;  // low free-space watermark, 0 default rule
    long crit_free_mib  = 0;  // critical free-space watermark, 0 default rule
    long min_reuse      = -1; // -1 unset (default_min_reuse)
    long max_ckpt       = -1; // -1 unset (max_checkpoints)
    long overhead_ms    = -1; // -1 unset (load_overhead_s)
};

inline watermarks watermarks_of(const disk_options & o) {
    watermarks w;
    w.low      = (uint64_t) std::max(0L, o.low_free_mib) << 20;
    w.critical = (uint64_t) std::max(0L, o.crit_free_mib) << 20;
    return w;
}

constexpr long default_ttl_hours = 7 * 24;

struct disk_decision {
    bool        on = false;
    std::string info; // why it is off, for the log; empty when there is nothing to say
};

// The disk tier is on by default. It needs a directory from the node that
// started the backend (root) and the RAM prompt cache, which it sits under.
inline disk_decision resolve_disk(const disk_options & o, const std::string & root, long cache_ram_mib) {
    disk_decision d;
    if (o.enabled == 0) {
        return d;
    }
    if (root.empty()) {
        if (o.enabled == 1) {
            d.info = "prompt_cache_disk: LocalAI gave no cache directory (no data path), the disk prompt cache stays off";
        }
        return d;
    }
    if (cache_ram_mib == 0) {
        if (o.enabled == 1) {
            d.info = "prompt_cache_disk: needs the RAM prompt cache, cache_ram is 0, the disk prompt cache stays off";
        }
        return d;
    }
    d.on = true;
    return d;
}

inline int64_t ttl_seconds(const disk_options & o) {
    const long h = o.ttl_hours < 0 ? default_ttl_hours : o.ttl_hours;
    return (int64_t) h * 3600;
}

inline uint64_t min_tokens(const disk_options & o) {
    return o.min_tokens < 0 ? default_min_tokens : (uint64_t) o.min_tokens;
}

inline uint32_t min_reuse(const disk_options & o) {
    return o.min_reuse < 1 ? default_min_reuse : (uint32_t) o.min_reuse;
}

inline size_t max_checkpoints_of(const disk_options & o) {
    return o.max_ckpt < 0 ? max_checkpoints : (size_t) o.max_ckpt;
}

inline double overhead_seconds(const disk_options & o) {
    return o.overhead_ms < 0 ? load_overhead_s : o.overhead_ms / 1000.0;
}

// The policy numbers come from the node that starts the backend, as
// environment variables (the contract in .agents/prompt-cache-backends.md),
// so every backend that implements the disk tier uses the same numbers.
// A model option of the same meaning wins over the environment. `get` is
// std::getenv in the backend and a map in the test.
struct env_name {
    const char * name;
    long disk_options::*field;
};

constexpr env_name env_names[] = {
    { "LOCALAI_PROMPT_CACHE_TTL_HOURS",          &disk_options::ttl_hours },
    { "LOCALAI_PROMPT_CACHE_MIN_TOKENS",         &disk_options::min_tokens },
    { "LOCALAI_PROMPT_CACHE_QUOTA_MIB",          &disk_options::quota_mib },
    { "LOCALAI_PROMPT_CACHE_MAX_SIZE_MIB",       &disk_options::max_size_mib },
    { "LOCALAI_PROMPT_CACHE_LOW_FREE_MIB",       &disk_options::low_free_mib },
    { "LOCALAI_PROMPT_CACHE_CRITICAL_FREE_MIB",  &disk_options::crit_free_mib },
    { "LOCALAI_PROMPT_CACHE_MIN_REUSE",          &disk_options::min_reuse },
    { "LOCALAI_PROMPT_CACHE_MAX_CHECKPOINTS",    &disk_options::max_ckpt },
    { "LOCALAI_PROMPT_CACHE_LOAD_OVERHEAD_MS",   &disk_options::overhead_ms },
};

// Fills the fields the environment sets. A missing, empty or malformed value
// (not a whole number of 0 or more) leaves the field as it is.
template <typename Get>
inline void apply_env(disk_options & o, Get get) {
    for (const auto & e : env_names) {
        const char * v = get(e.name);
        if (v == nullptr || *v == '\0') {
            continue;
        }
        char * end = nullptr;
        const long n = std::strtol(v, &end, 10);
        if (end == v || *end != '\0' || n < 0) {
            continue;
        }
        o.*(e.field) = n;
    }
}

} // namespace prompt_cache_policy
