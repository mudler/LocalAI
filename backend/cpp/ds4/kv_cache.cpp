#include "kv_cache.h"

#include <cerrno>
#include <cstdio>
#include <cstring>
#include <dirent.h>
#include <fstream>
#include <sys/stat.h>
#include <vector>

namespace ds4cpp {

namespace {

// Minimal SHA1 (public domain reference). 30 lines; used only here.
struct Sha1 {
    uint32_t h[5];
    uint64_t bits;
    uint8_t block[64];
    size_t used;
    Sha1() { h[0]=0x67452301; h[1]=0xEFCDAB89; h[2]=0x98BADCFE; h[3]=0x10325476; h[4]=0xC3D2E1F0; bits=0; used=0; }
    static uint32_t rol(uint32_t x, int n){ return (x<<n)|(x>>(32-n)); }
    void transform(const uint8_t *b) {
        uint32_t w[80];
        for (int i=0;i<16;i++) w[i] = (uint32_t)b[i*4]<<24 | (uint32_t)b[i*4+1]<<16 | (uint32_t)b[i*4+2]<<8 | b[i*4+3];
        for (int i=16;i<80;i++) w[i] = rol(w[i-3]^w[i-8]^w[i-14]^w[i-16], 1);
        uint32_t a=h[0],bb=h[1],c=h[2],d=h[3],e=h[4];
        for (int i=0;i<80;i++) {
            uint32_t f,k;
            if (i<20)      { f=(bb&c)|((~bb)&d); k=0x5A827999; }
            else if (i<40) { f=bb^c^d;            k=0x6ED9EBA1; }
            else if (i<60) { f=(bb&c)|(bb&d)|(c&d); k=0x8F1BBCDC; }
            else           { f=bb^c^d;            k=0xCA62C1D6; }
            uint32_t t = rol(a,5)+f+e+k+w[i];
            e=d; d=c; c=rol(bb,30); bb=a; a=t;
        }
        h[0]+=a; h[1]+=bb; h[2]+=c; h[3]+=d; h[4]+=e;
    }
    void update(const void *p, size_t n) {
        const uint8_t *bp = (const uint8_t*)p;
        bits += (uint64_t)n*8;
        while (n) {
            size_t take = 64-used;
            if (take>n) take=n;
            std::memcpy(block+used, bp, take);
            used += take; bp += take; n -= take;
            if (used == 64) { transform(block); used = 0; }
        }
    }
    void final(uint8_t out[20]) {
        uint8_t pad[64] = {0x80};
        size_t padlen = (used < 56) ? (56-used) : (120-used);
        uint64_t lb = bits;
        uint8_t len[8];
        for (int i=0;i<8;i++) len[7-i] = (uint8_t)(lb >> (i*8));
        update(pad, padlen);
        update(len, 8);
        for (int i=0;i<5;i++) {
            out[i*4]   = h[i]>>24;
            out[i*4+1] = h[i]>>16;
            out[i*4+2] = h[i]>>8;
            out[i*4+3] = h[i];
        }
    }
};

std::string mkdir_p(const std::string &d) {
    if (d.empty()) return d;
    struct stat st{};
    if (stat(d.c_str(), &st) == 0) return d;
    mkdir(d.c_str(), 0755);
    return d;
}

bool file_exists(const std::string &p) {
    struct stat st{};
    return stat(p.c_str(), &st) == 0;
}

} // namespace

std::string Sha1Hex(const void *data, size_t len) {
    Sha1 s;
    s.update(data, len);
    uint8_t out[20];
    s.final(out);
    char hex[41];
    for (int i = 0; i < 20; ++i) std::snprintf(hex + i*2, 3, "%02x", out[i]);
    hex[40] = 0;
    return std::string(hex);
}

KvCache::KvCache() = default;

void KvCache::SetDir(const std::string &dir) {
    dir_ = dir;
    if (!dir_.empty()) {
        mkdir_p(dir_);
        std::fprintf(stderr, "ds4 KvCache: enabled at %s\n", dir_.c_str());
    } else {
        std::fprintf(stderr, "ds4 KvCache: disabled (no dir set)\n");
    }
}

std::string KvCache::Path(const std::string &rendered_text) const {
    if (dir_.empty()) return "";
    return dir_ + "/" + Sha1Hex(rendered_text.data(), rendered_text.size()) + ".kv";
}

size_t KvCache::LoadLongestPrefix(ds4_session *session,
                                  const std::string &rendered_text,
                                  int ctx_size) {
    if (dir_.empty() || !session) return 0;
    // Strategy: enumerate all .kv files in dir, read their stored prefix
    // header, pick the longest one that is also a prefix of rendered_text.
    DIR *d = opendir(dir_.c_str());
    if (!d) return 0;
    struct dirent *de;
    size_t best_len = 0;
    std::string best_path;
    while ((de = readdir(d)) != nullptr) {
        std::string name = de->d_name;
        if (name.size() < 4 || name.substr(name.size()-3) != ".kv") continue;
        std::string path = dir_ + "/" + name;
        std::ifstream f(path, std::ios::binary);
        if (!f) continue;
        char magic[4]; f.read(magic, 4);
        if (f.gcount() != 4 || std::memcmp(magic, "DS4G", 4) != 0) continue;
        uint32_t version=0, file_ctx=0, prefix_len=0;
        f.read((char*)&version, 4); f.read((char*)&file_ctx, 4); f.read((char*)&prefix_len, 4);
        if (version != 1) continue;
        if ((int)file_ctx != ctx_size) continue;
        if (prefix_len > rendered_text.size()) continue;
        std::vector<char> prefix(prefix_len);
        f.read(prefix.data(), prefix_len);
        if (std::memcmp(prefix.data(), rendered_text.data(), prefix_len) != 0) continue;
        if (prefix_len > best_len) {
            best_len = prefix_len;
            best_path = path;
        }
    }
    closedir(d);
    if (best_len == 0) return 0;

    // Load best_path's payload into session.
    std::ifstream f(best_path, std::ios::binary);
    char magic[4]; f.read(magic, 4);
    uint32_t version, file_ctx, prefix_len;
    f.read((char*)&version, 4); f.read((char*)&file_ctx, 4); f.read((char*)&prefix_len, 4);
    f.seekg(prefix_len, std::ios::cur);
    uint64_t payload_bytes = 0;
    f.read((char*)&payload_bytes, 8);
    // ds4_session_load_payload reads from a FILE*; reopen via fopen.
    FILE *fp = std::fopen(best_path.c_str(), "rb");
    if (!fp) return 0;
    // Seek past header + prefix + payload_bytes field.
    std::fseek(fp, 4 + 4 + 4 + 4 + prefix_len + 8, SEEK_SET);
    char errbuf[256] = {0};
    int rc = ds4_session_load_payload(session, fp, payload_bytes, errbuf, sizeof(errbuf));
    std::fclose(fp);
    if (rc != 0) return 0;
    return best_len;
}

void KvCache::Save(ds4_session *session, const std::string &rendered_text, int ctx_size) {
    if (dir_.empty()) {
        std::fprintf(stderr, "ds4 KvCache::Save: skipped (dir empty)\n");
        return;
    }
    if (!session) {
        std::fprintf(stderr, "ds4 KvCache::Save: skipped (session null)\n");
        return;
    }
    std::string path = Path(rendered_text);
    uint64_t payload_bytes = ds4_session_payload_bytes(session);
    std::fprintf(stderr, "ds4 KvCache::Save: path=%s payload_bytes=%llu prefix_len=%zu\n",
                 path.c_str(), (unsigned long long)payload_bytes, rendered_text.size());
    FILE *fp = std::fopen(path.c_str(), "wb");
    if (!fp) {
        std::fprintf(stderr, "ds4 KvCache::Save: fopen failed: %s\n", std::strerror(errno));
        return;
    }
    char magic[4] = {'D','S','4','G'};
    uint32_t version = 1;
    uint32_t ctx = static_cast<uint32_t>(ctx_size);
    uint32_t prefix_len = static_cast<uint32_t>(rendered_text.size());
    std::fwrite(magic, 4, 1, fp);
    std::fwrite(&version, 4, 1, fp);
    std::fwrite(&ctx, 4, 1, fp);
    std::fwrite(&prefix_len, 4, 1, fp);
    std::fwrite(rendered_text.data(), prefix_len, 1, fp);
    std::fwrite(&payload_bytes, 8, 1, fp);
    char errbuf[256] = {0};
    int rc = ds4_session_save_payload(session, fp, errbuf, sizeof(errbuf));
    std::fclose(fp);
    if (rc != 0) {
        std::fprintf(stderr, "ds4 KvCache::Save: ds4_session_save_payload rc=%d err=%s; removing %s\n",
                     rc, errbuf, path.c_str());
        std::remove(path.c_str());
    } else {
        std::fprintf(stderr, "ds4 KvCache::Save: wrote %s ok\n", path.c_str());
    }
}

} // namespace ds4cpp
