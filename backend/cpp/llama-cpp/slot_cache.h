#pragma once

// Helpers for the SlotCache RPC. They are header-only and free of llama.cpp
// and gRPC types so they can be tested on their own.
//
// The client never chooses a file name or a path. A save gets a random ID from
// the backend and writes <slot_save_path>/<dir>/<id>, where <dir> comes from
// the model identity. A manifest.json in <dir> lists every ID the backend
// issued. A restore accepts an ID only if the manifest lists it.

#include <nlohmann/json.hpp>

#include <cerrno>
#include <cstdint>
#include <cstdio>
#include <cstring>
#include <ctime>
#include <filesystem>
#include <functional>
#include <mutex>
#include <set>
#include <stdexcept>
#include <string>
#include <vector>

#ifndef _WIN32
#include <fcntl.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>
#endif

namespace llama_grpc {

// Saved files kept per model directory. The oldest entry is deleted when a
// save would go over this number.
constexpr size_t SLOT_CACHE_MAX_ENTRIES = 32;

// Largest file one save may leave on disk. llama.cpp does not report the size
// before it writes, so the check runs after the save and the file is deleted
// when it is too large. 8 GiB keeps one model directory at or below 256 GiB
// (32 files) and is far above a typical saved prompt (hundreds of MiB). The
// disk can briefly hold one file over the limit while the check runs.
constexpr uint64_t SLOT_CACHE_MAX_FILE_BYTES = 8ULL * 1024 * 1024 * 1024;

constexpr const char* SLOT_CACHE_MANIFEST = "manifest.json";

enum class SlotStatus {
    Ok,
    NotFound,  // unknown ID, or an entry that belongs to another model
    Invalid,   // bad ID, or the file on disk is not what the manifest says
    Corrupt,   // the manifest cannot be read as written by this code
    Internal,  // disk, permission or randomness failure
};

struct SlotEntry {
    std::string id;
    std::string model_identity;
    int32_t slot = 0;
    uint64_t n_tokens = 0;
    uint64_t n_bytes = 0;
    int64_t created_unix = 0;
};

// slot_id_valid accepts exactly 32 lowercase hex characters. Nothing else can
// contain a separator, a dot or a NUL, so a valid ID is always one plain path
// component.
inline bool slot_id_valid(const std::string& id) {
    if (id.size() != 32) {
        return false;
    }
    for (const char c : id) {
        if (!((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'))) {
            return false;
        }
    }
    return true;
}

inline std::string slot_hex(const unsigned char* data, size_t n) {
    static const char* digits = "0123456789abcdef";
    std::string out;
    out.reserve(n * 2);
    for (size_t i = 0; i < n; i++) {
        out.push_back(digits[data[i] >> 4]);
        out.push_back(digits[data[i] & 0x0F]);
    }
    return out;
}

// slot_random_id draws 128 bits from the operating system and renders them as
// 32 lowercase hex characters. /dev/urandom is the kernel CSPRNG;
// std::random_device is not guaranteed to be one, so it is not used. Returns
// false when no secure source is available (always on Windows, where this
// feature is not supported).
inline bool slot_random_id(std::string& out) {
#ifdef _WIN32
    (void)out;
    return false;
#else
    unsigned char buf[16];
    const int fd = ::open("/dev/urandom", O_RDONLY | O_CLOEXEC);
    if (fd < 0) {
        return false;
    }
    size_t got = 0;
    while (got < sizeof(buf)) {
        const ssize_t r = ::read(fd, buf + got, sizeof(buf) - got);
        if (r <= 0) {
            ::close(fd);
            return false;
        }
        got += static_cast<size_t>(r);
    }
    ::close(fd);
    out = slot_hex(buf, sizeof(buf));
    return true;
#endif
}

// SHA-256 (FIPS 180-4), used only to turn a model identity into a directory
// name. Kept here so the header stays free of other dependencies.
inline std::string slot_sha256_hex(const std::string& msg) {
    static const uint32_t K[64] = {
        0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
        0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
        0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
        0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
        0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
        0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
        0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
        0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2};
    uint32_t h[8] = {0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19};

    std::string data = msg;
    const uint64_t bits = static_cast<uint64_t>(msg.size()) * 8;
    data.push_back(static_cast<char>(0x80));
    while (data.size() % 64 != 56) {
        data.push_back('\0');
    }
    for (int i = 7; i >= 0; i--) {
        data.push_back(static_cast<char>((bits >> (i * 8)) & 0xFF));
    }

    auto rotr = [](uint32_t x, int n) { return (x >> n) | (x << (32 - n)); };
    for (size_t off = 0; off < data.size(); off += 64) {
        uint32_t w[64];
        for (int i = 0; i < 16; i++) {
            const unsigned char* p = reinterpret_cast<const unsigned char*>(data.data()) + off + i * 4;
            w[i] = (uint32_t(p[0]) << 24) | (uint32_t(p[1]) << 16) | (uint32_t(p[2]) << 8) | uint32_t(p[3]);
        }
        for (int i = 16; i < 64; i++) {
            const uint32_t s0 = rotr(w[i - 15], 7) ^ rotr(w[i - 15], 18) ^ (w[i - 15] >> 3);
            const uint32_t s1 = rotr(w[i - 2], 17) ^ rotr(w[i - 2], 19) ^ (w[i - 2] >> 10);
            w[i] = w[i - 16] + s0 + w[i - 7] + s1;
        }
        uint32_t a = h[0], b = h[1], c = h[2], d = h[3], e = h[4], f = h[5], g = h[6], hh = h[7];
        for (int i = 0; i < 64; i++) {
            const uint32_t S1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25);
            const uint32_t ch = (e & f) ^ (~e & g);
            const uint32_t t1 = hh + S1 + ch + K[i] + w[i];
            const uint32_t S0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22);
            const uint32_t mj = (a & b) ^ (a & c) ^ (b & c);
            const uint32_t t2 = S0 + mj;
            hh = g; g = f; f = e; e = d + t1; d = c; c = b; b = a; a = t1 + t2;
        }
        h[0] += a; h[1] += b; h[2] += c; h[3] += d; h[4] += e; h[5] += f; h[6] += g; h[7] += hh;
    }

    unsigned char digest[32];
    for (int i = 0; i < 8; i++) {
        digest[i * 4] = static_cast<unsigned char>(h[i] >> 24);
        digest[i * 4 + 1] = static_cast<unsigned char>(h[i] >> 16);
        digest[i * 4 + 2] = static_cast<unsigned char>(h[i] >> 8);
        digest[i * 4 + 3] = static_cast<unsigned char>(h[i]);
    }
    return slot_hex(digest, sizeof(digest));
}

// slot_identity_dir_name is the first 32 hex characters of the SHA-256 of the
// model identity.
inline std::string slot_identity_dir_name(const std::string& identity) {
    return slot_sha256_hex(identity).substr(0, 32);
}

// One lock for every manifest read-modify-write in this process. Two backend
// processes that share one slot_save_path and one model are not coordinated.
inline std::mutex& slot_cache_mutex() {
    static std::mutex m;
    return m;
}

#ifndef _WIN32

// slot_ensure_private_dir makes sure path is a directory. A missing directory
// is created with mode 0700 (parents get the default mode). An existing one is
// left as it is; if group or others can access it, warn is set so the caller
// can log it.
inline bool slot_ensure_private_dir(const std::string& path, std::string& warn, std::string& err) {
    namespace fs = std::filesystem;
    warn.clear();
    std::error_code ec;
    const fs::path p = fs::u8path(path);
    struct stat st;
    if (::lstat(p.c_str(), &st) != 0) {
        if (errno != ENOENT) {
            err = "cannot inspect " + path + ": " + std::strerror(errno);
            return false;
        }
        if (p.has_parent_path()) {
            fs::create_directories(p.parent_path(), ec);
            if (ec) {
                err = "cannot create the parent of " + path + ": " + ec.message();
                return false;
            }
        }
        if (::mkdir(p.c_str(), 0700) != 0 && errno != EEXIST) {
            err = "cannot create " + path + ": " + std::strerror(errno);
            return false;
        }
        if (::lstat(p.c_str(), &st) != 0) {
            err = "cannot inspect " + path + ": " + std::strerror(errno);
            return false;
        }
    }
    // stat, not lstat, for the final check: a configured slot_save_path may be
    // a link to a directory. The identity directories below it are checked
    // with lstat.
    struct stat target;
    if (::stat(p.c_str(), &target) != 0 || !S_ISDIR(target.st_mode)) {
        err = path + " is not a directory";
        return false;
    }
    if ((target.st_mode & 077) != 0) {
        warn = path + " is accessible by group or others (mode " + [&] {
                   char buf[8];
                   std::snprintf(buf, sizeof(buf), "%04o", static_cast<unsigned>(target.st_mode & 07777));
                   return std::string(buf);
               }() + "); mode 0700 is advised because saved prompts can contain private text";
    }
    return true;
}

// slot_identity_dir returns <root>/<hash of identity>. With create it makes the
// directory (mode 0700) when it is missing. The directory must be a real
// directory, not a link, and owned by the running user.
inline SlotStatus slot_identity_dir(const std::string& root, const std::string& identity, bool create,
                                    std::string& dir, std::string& err) {
    namespace fs = std::filesystem;
    if (root.empty()) {
        err = "slot_save_path is not set for this model";
        return SlotStatus::Internal;
    }
    dir = (fs::u8path(root) / slot_identity_dir_name(identity)).string();
    struct stat st;
    if (::lstat(dir.c_str(), &st) != 0) {
        if (errno != ENOENT) {
            err = std::string("cannot inspect the slot directory: ") + std::strerror(errno);
            return SlotStatus::Internal;
        }
        if (!create) {
            return SlotStatus::NotFound;
        }
        if (::mkdir(dir.c_str(), 0700) != 0 && errno != EEXIST) {
            err = std::string("cannot create the slot directory: ") + std::strerror(errno);
            return SlotStatus::Internal;
        }
        if (::lstat(dir.c_str(), &st) != 0) {
            err = std::string("cannot inspect the slot directory: ") + std::strerror(errno);
            return SlotStatus::Internal;
        }
    }
    if (!S_ISDIR(st.st_mode)) {
        err = "the slot directory is not a plain directory";
        return SlotStatus::Internal;
    }
    if (st.st_uid != ::geteuid() || (st.st_mode & 077) != 0) {
        err = "the slot directory must be owned by the backend user and have mode 0700";
        return SlotStatus::Internal;
    }
    return SlotStatus::Ok;
}

inline nlohmann::json slot_entry_to_json(const SlotEntry& e) {
    return nlohmann::json{{"id", e.id},
                          {"model_identity", e.model_identity},
                          {"slot", e.slot},
                          {"n_tokens", e.n_tokens},
                          {"n_bytes", e.n_bytes},
                          {"created_unix", e.created_unix}};
}

// slot_manifest_load reads <dir>/manifest.json. A missing file is an empty
// list. A file that does not parse, or has an entry that is not well formed,
// returns Corrupt and is never reset: the caller must stop.
inline SlotStatus slot_manifest_load(const std::string& dir, std::vector<SlotEntry>& entries, std::string& err) {
    namespace fs = std::filesystem;
    entries.clear();
    const std::string path = (fs::u8path(dir) / SLOT_CACHE_MANIFEST).string();
    struct stat st;
    if (::lstat(path.c_str(), &st) != 0) {
        if (errno == ENOENT) {
            return SlotStatus::Ok;
        }
        err = std::string("cannot read the slot manifest: ") + std::strerror(errno);
        return SlotStatus::Internal;
    }
    if (!S_ISREG(st.st_mode)) {
        err = "the slot manifest is not a regular file";
        return SlotStatus::Corrupt;
    }
    const int fd = ::open(path.c_str(), O_RDONLY | O_CLOEXEC | O_NOFOLLOW);
    if (fd < 0) {
        err = std::string("cannot read the slot manifest: ") + std::strerror(errno);
        return SlotStatus::Internal;
    }
    std::string text;
    char buf[4096];
    for (;;) {
        const ssize_t r = ::read(fd, buf, sizeof(buf));
        if (r < 0) {
            ::close(fd);
            err = std::string("cannot read the slot manifest: ") + std::strerror(errno);
            return SlotStatus::Internal;
        }
        if (r == 0) {
            break;
        }
        text.append(buf, static_cast<size_t>(r));
        if (text.size() > (16u << 20)) {
            ::close(fd);
            err = "the slot manifest is corrupt: too large";
            return SlotStatus::Corrupt;
        }
    }
    ::close(fd);

    try {
        const nlohmann::json doc = nlohmann::json::parse(text);
        if (!doc.is_object() || !doc.contains("entries") || !doc["entries"].is_array()) {
            throw std::runtime_error("no entries array");
        }
        for (const auto& j : doc["entries"]) {
            SlotEntry e;
            e.id = j.at("id").get<std::string>();
            e.model_identity = j.at("model_identity").get<std::string>();
            e.slot = j.at("slot").get<int32_t>();
            e.n_tokens = j.at("n_tokens").get<uint64_t>();
            e.n_bytes = j.at("n_bytes").get<uint64_t>();
            e.created_unix = j.at("created_unix").get<int64_t>();
            if (!slot_id_valid(e.id)) {
                throw std::runtime_error("bad id");
            }
            entries.push_back(std::move(e));
        }
    } catch (const std::exception& ex) {
        entries.clear();
        err = std::string("the slot manifest is corrupt: ") + ex.what();
        return SlotStatus::Corrupt;
    }
    return SlotStatus::Ok;
}

// slot_manifest_write replaces the manifest atomically: write a temp file in
// the same directory, fsync it, rename it over the manifest, fsync the
// directory.
inline SlotStatus slot_manifest_write(const std::string& dir, const std::vector<SlotEntry>& entries, std::string& err) {
    namespace fs = std::filesystem;
    nlohmann::json doc;
    doc["version"] = 1;
    doc["entries"] = nlohmann::json::array();
    for (const auto& e : entries) {
        doc["entries"].push_back(slot_entry_to_json(e));
    }
    const std::string text = doc.dump(2);

    std::string suffix;
    if (!slot_random_id(suffix)) {
        err = "no secure random source available";
        return SlotStatus::Internal;
    }
    const std::string final_path = (fs::u8path(dir) / SLOT_CACHE_MANIFEST).string();
    const std::string tmp_path = final_path + ".tmp." + suffix;
    const int fd = ::open(tmp_path.c_str(), O_WRONLY | O_CREAT | O_EXCL | O_CLOEXEC | O_NOFOLLOW, 0600);
    if (fd < 0) {
        err = std::string("cannot write the slot manifest: ") + std::strerror(errno);
        return SlotStatus::Internal;
    }
    size_t done = 0;
    bool ok = true;
    while (done < text.size()) {
        const ssize_t w = ::write(fd, text.data() + done, text.size() - done);
        if (w <= 0) {
            ok = false;
            break;
        }
        done += static_cast<size_t>(w);
    }
    if (ok && ::fsync(fd) != 0) {
        ok = false;
    }
    ::close(fd);
    if (!ok || ::rename(tmp_path.c_str(), final_path.c_str()) != 0) {
        err = std::string("cannot write the slot manifest: ") + std::strerror(errno);
        ::unlink(tmp_path.c_str());
        return SlotStatus::Internal;
    }
    const int dfd = ::open(dir.c_str(), O_RDONLY | O_DIRECTORY | O_CLOEXEC);
    if (dfd >= 0) {
        ::fsync(dfd);
        ::close(dfd);
    }
    return SlotStatus::Ok;
}

// slot_reserve_id picks an ID that is not in use and creates an empty file
// with that name (O_EXCL, mode 0600), so a save never overwrites anything.
// The generator can be replaced in tests.
inline SlotStatus slot_reserve_id(const std::string& dir, std::string& id, std::string& path, std::string& err,
                                  const std::function<bool(std::string&)>& gen = slot_random_id) {
    namespace fs = std::filesystem;
    for (int attempt = 0; attempt < 16; attempt++) {
        std::string cand;
        if (!gen(cand) || !slot_id_valid(cand)) {
            err = "no secure random source available";
            return SlotStatus::Internal;
        }
        const std::string p = (fs::u8path(dir) / cand).string();
        const int fd = ::open(p.c_str(), O_WRONLY | O_CREAT | O_EXCL | O_CLOEXEC | O_NOFOLLOW, 0600);
        if (fd >= 0) {
            ::close(fd);
            id = cand;
            path = p;
            return SlotStatus::Ok;
        }
        if (errno != EEXIST) {
            err = std::string("cannot create the slot file: ") + std::strerror(errno);
            return SlotStatus::Internal;
        }
    }
    err = "could not find an unused slot ID";
    return SlotStatus::Internal;
}

// slot_commit_save appends the entry to the manifest and, when the list is
// over SLOT_CACHE_MAX_ENTRIES, removes the oldest entries and their files. The
// IDs removed are returned in pruned. A corrupt manifest stops the save.
inline SlotStatus slot_commit_save(const std::string& dir, const SlotEntry& entry, std::vector<std::string>& pruned,
                                   std::string& err, size_t max_entries = SLOT_CACHE_MAX_ENTRIES) {
    namespace fs = std::filesystem;
    pruned.clear();
    std::lock_guard<std::mutex> lock(slot_cache_mutex());
    std::vector<SlotEntry> entries;
    SlotStatus s = slot_manifest_load(dir, entries, err);
    if (s != SlotStatus::Ok) {
        return s;
    }
    entries.push_back(entry);
    while (entries.size() > max_entries) {
        size_t oldest = 0;
        for (size_t i = 1; i < entries.size(); i++) {
            if (entries[i].created_unix < entries[oldest].created_unix) {
                oldest = i;
            }
        }
        // Never prune the entry that was just added.
        if (entries[oldest].id == entry.id) {
            break;
        }
        pruned.push_back(entries[oldest].id);
        entries.erase(entries.begin() + static_cast<std::ptrdiff_t>(oldest));
    }
    s = slot_manifest_write(dir, entries, err);
    if (s != SlotStatus::Ok) {
        pruned.clear();
        return s;
    }
    for (const auto& id : pruned) {
        std::error_code ec;
        fs::remove(fs::u8path(dir) / id, ec);
    }
    return SlotStatus::Ok;
}

// slot_lookup_restore decides whether a restore may go ahead and returns the
// path to hand to llama.cpp. The path is built from the manifest entry's ID,
// never from request text.
//
// The checks are: the ID has the right shape; the manifest lists it; the entry
// belongs to this model; the path is a regular file (lstat, so a link or a
// directory is refused); and its size is the size recorded at save time, so a
// replaced or truncated file is refused.
//
// This is a check of a path, and llama.cpp then opens that path itself. A
// small window remains between the lstat and that open. The 0700 mode of the
// directory is the main defence: it stops another local user from swapping the
// file in that window. This code does not close the window against the same
// user or against root.
inline SlotStatus slot_lookup_restore(const std::string& root, const std::string& identity, const std::string& id,
                                      SlotEntry& entry, std::string& path, std::string& err) {
    namespace fs = std::filesystem;
    if (!slot_id_valid(id)) {
        err = "invalid id: expected 32 lowercase hex characters";
        return SlotStatus::Invalid;
    }
    std::string dir;
    SlotStatus s = slot_identity_dir(root, identity, false, dir, err);
    if (s == SlotStatus::NotFound) {
        err = "no saved state with this id for this model";
        return s;
    }
    if (s != SlotStatus::Ok) {
        return s;
    }

    std::vector<SlotEntry> entries;
    {
        std::lock_guard<std::mutex> lock(slot_cache_mutex());
        s = slot_manifest_load(dir, entries, err);
    }
    if (s != SlotStatus::Ok) {
        return s;
    }
    const SlotEntry* found = nullptr;
    for (const auto& e : entries) {
        if (e.id == id) {
            found = &e;
            break;
        }
    }
    if (found == nullptr || found->model_identity != identity) {
        err = "no saved state with this id for this model";
        return SlotStatus::NotFound;
    }

    path = (fs::u8path(dir) / found->id).string();
    struct stat st;
    if (::lstat(path.c_str(), &st) != 0) {
        err = "no saved state with this id for this model";
        return SlotStatus::NotFound;
    }
    if (!S_ISREG(st.st_mode)) {
        err = "the saved file is not a regular file";
        return SlotStatus::Invalid;
    }
    if (static_cast<uint64_t>(st.st_size) != found->n_bytes) {
        err = "the saved file does not match its record: it was changed or truncated";
        return SlotStatus::Invalid;
    }
    entry = *found;
    return SlotStatus::Ok;
}

#endif // !_WIN32

} // namespace llama_grpc
