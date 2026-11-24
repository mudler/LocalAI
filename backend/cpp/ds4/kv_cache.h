#pragma once
#include <string>
extern "C" {
#include "ds4.h"
}

namespace ds4cpp {

// Disk-backed KV cache for ds4 sessions. Keyed by SHA1(rendered prompt prefix).
// Format (our own, NOT bit-compatible with ds4-server's KVC files - interop
// is a follow-up plan):
//
//   "DS4G" (4 bytes magic) + u32 version=1 + u32 ctx_size +
//   u32 prefix_text_len + prefix_text + u64 payload_bytes + payload
class KvCache {
public:
    KvCache(); // disabled (dir empty)

    // Set the cache directory. Empty disables.
    void SetDir(const std::string &dir);

    // Returns the cache file path for a given rendered text prefix.
    std::string Path(const std::string &rendered_text) const;

    // Look up the longest cached prefix that is also a prefix of
    // `rendered_text`. Loads it into `session` if found. Returns the
    // matched prefix length in bytes (0 if no hit).
    size_t LoadLongestPrefix(ds4_session *session,
                             const std::string &rendered_text,
                             int ctx_size);

    // Save the current session, associated with this rendered text prefix.
    void Save(ds4_session *session, const std::string &rendered_text, int ctx_size);

    bool enabled() const { return !dir_.empty(); }

private:
    std::string dir_;
};

// Compute SHA1 of arbitrary bytes; returns 40-char hex.
std::string Sha1Hex(const void *data, size_t len);

} // namespace ds4cpp
