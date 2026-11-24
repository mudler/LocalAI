#include "slot_cache.h"

#include <cstdio>
#include <cstdlib>
#include <filesystem>
#include <fstream>
#include <set>
#include <string>

static int failures = 0;

static void expect(bool cond, const char* what) {
    if (!cond) {
        std::fprintf(stderr, "FAIL: %s\n", what);
        failures++;
    }
}

using namespace llama_grpc;
namespace fs = std::filesystem;

static SlotEntry make_entry(const std::string& id, const std::string& identity, int64_t created, uint64_t bytes) {
    SlotEntry e;
    e.id = id;
    e.model_identity = identity;
    e.slot = 1;
    e.n_tokens = 10;
    e.n_bytes = bytes;
    e.created_unix = created;
    return e;
}

static std::string write_file(const std::string& path, size_t n) {
    std::ofstream(path, std::ios::binary) << std::string(n, 'x');
    return path;
}

int main() {
    // ID validation.
    expect(slot_id_valid("0123456789abcdef0123456789abcdef"), "valid id");
    expect(!slot_id_valid("0123456789ABCDEF0123456789abcdef"), "uppercase rejected");
    expect(!slot_id_valid("0123456789abcdef"), "too short");
    expect(!slot_id_valid("0123456789abcdef0123456789abcdef0"), "too long");
    expect(!slot_id_valid(""), "empty");
    expect(!slot_id_valid("0123456789abcdef0123456789abcdeg"), "non-hex");
    expect(!slot_id_valid("..............................ab"), "dots");
    expect(!slot_id_valid("../../../../etc/passwd/../../xx"), "traversal");
    expect(!slot_id_valid("0123456789abcdef/123456789abcdef"), "slash");
    expect(!slot_id_valid(std::string("0123456789abcdef0123456789abcd\0f", 32)), "NUL byte");

    // SHA-256 known answers.
    expect(slot_sha256_hex("abc") == "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", "sha256 abc");
    expect(slot_sha256_hex("") == "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", "sha256 empty");
    expect(slot_sha256_hex("abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq") ==
               "248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1", "sha256 two blocks");
    expect(slot_identity_dir_name("abc") == "ba7816bf8f01cfea414140de5dae2223", "dir name is 32 hex of sha256");

    // ID generation: format and uniqueness.
    {
        std::set<std::string> seen;
        bool fmt = true;
        for (int i = 0; i < 20000; i++) {
            std::string id;
            if (!slot_random_id(id) || !slot_id_valid(id)) {
                fmt = false;
                break;
            }
            seen.insert(id);
        }
        expect(fmt, "generated ids have the valid format");
        expect(seen.size() == 20000, "20000 generated ids are unique");
    }

    std::error_code ec;
    const fs::path base = fs::temp_directory_path() / "localai_slot_cache_test";
    fs::remove_all(base, ec);
    const std::string root = (base / "save").string();
    std::string warn, err;

    // Root creation: missing root gets mode 0700.
    expect(slot_ensure_private_dir(root, warn, err), "create root");
    expect((fs::status(root).permissions() & (fs::perms::group_all | fs::perms::others_all)) == fs::perms::none,
           "new root is 0700");
    expect(warn.empty(), "no warning for 0700 root");
    // A looser existing root is kept and reported.
    const std::string loose = (base / "loose").string();
    fs::create_directories(loose);
    ::chmod(loose.c_str(), 0755);
    expect(slot_ensure_private_dir(loose, warn, err), "existing root accepted");
    expect(!warn.empty(), "warning for loose root");
    expect((fs::status(loose).permissions() & fs::perms::group_read) != fs::perms::none, "loose root mode unchanged");

    const std::string identity = "model-a";
    const std::string other_identity = "model-b";
    std::string dir;
    expect(slot_identity_dir(root, identity, false, dir, err) == SlotStatus::NotFound, "no dir before first save");
    expect(slot_identity_dir(root, identity, true, dir, err) == SlotStatus::Ok, "identity dir created");
    expect(fs::path(dir).filename().string() == slot_identity_dir_name(identity), "dir named by identity hash");
    expect((fs::status(dir).permissions() & (fs::perms::group_all | fs::perms::others_all)) == fs::perms::none,
           "identity dir is 0700");

    // Reserve never overwrites: the generator repeats a used ID first.
    {
        std::string id1, path1, id2, path2;
        expect(slot_reserve_id(dir, id1, path1, err) == SlotStatus::Ok, "reserve first id");
        write_file(path1, 5);
        int calls = 0;
        auto gen = [&](std::string& out) {
            out = (calls++ < 2) ? id1 : "ffffffffffffffffffffffffffffffff";
            return true;
        };
        expect(slot_reserve_id(dir, id2, path2, err, gen) == SlotStatus::Ok, "reserve after collision");
        expect(id2 == "ffffffffffffffffffffffffffffffff" && calls == 3, "collision retried with a new id");
        expect(fs::file_size(path1) == 5, "existing file untouched by collision");
        fs::remove(path1);
        fs::remove(path2);
    }

    // Missing manifest is empty; round trip.
    std::vector<SlotEntry> entries;
    expect(slot_manifest_load(dir, entries, err) == SlotStatus::Ok && entries.empty(), "missing manifest is empty");
    const std::string id_a = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
    const std::string id_b = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb";
    std::vector<std::string> pruned;
    write_file(dir + "/" + id_a, 100);
    expect(slot_commit_save(dir, make_entry(id_a, identity, 1000, 100), pruned, err) == SlotStatus::Ok, "commit a");
    write_file(dir + "/" + id_b, 200);
    expect(slot_commit_save(dir, make_entry(id_b, identity, 2000, 200), pruned, err) == SlotStatus::Ok, "commit b");
    expect(slot_manifest_load(dir, entries, err) == SlotStatus::Ok, "load manifest");
    expect(entries.size() == 2 && entries[0].id == id_a && entries[1].id == id_b, "manifest round trip order");
    expect(entries[1].model_identity == identity && entries[1].slot == 1 && entries[1].n_tokens == 10 &&
               entries[1].n_bytes == 200 && entries[1].created_unix == 2000, "manifest round trip fields");
    {
        bool tmp_left = false;
        for (const auto& de : fs::directory_iterator(dir)) {
            if (de.path().filename().string().find(".tmp.") != std::string::npos) tmp_left = true;
        }
        expect(!tmp_left, "no temp manifest left behind");
    }

    // Pruning: oldest by created time goes first, with its file.
    {
        const std::string d2 = (base / "prune").string();
        fs::create_directories(d2);
        std::vector<std::string> ids;
        for (int i = 0; i < 4; i++) {
            std::string id = std::string(31, 'c') + char('0' + i);
            ids.push_back(id);
            write_file(d2 + "/" + id, 1);
        }
        // Commit out of time order so "oldest" is not "first in the list".
        const int64_t times[4] = {300, 100, 400, 200};
        std::vector<std::string> p;
        for (int i = 0; i < 3; i++) {
            expect(slot_commit_save(d2, make_entry(ids[i], identity, times[i], 1), p, err, 3) == SlotStatus::Ok, "fill");
            expect(p.empty(), "no prune under the limit");
        }
        expect(slot_commit_save(d2, make_entry(ids[3], identity, times[3], 1), p, err, 3) == SlotStatus::Ok, "over limit");
        expect(p.size() == 1 && p[0] == ids[1], "oldest entry pruned");
        expect(!fs::exists(d2 + "/" + ids[1]), "pruned file deleted");
        expect(fs::exists(d2 + "/" + ids[0]), "newer file kept");
        slot_manifest_load(d2, entries, err);
        expect(entries.size() == 3, "manifest holds the limit");
        expect(SLOT_CACHE_MAX_ENTRIES == 32, "limit is 32");
    }

    // Restore: success.
    SlotEntry got;
    std::string path;
    expect(slot_lookup_restore(root, identity, id_b, got, path, err) == SlotStatus::Ok, "restore known id");
    expect(path == dir + "/" + id_b && got.n_bytes == 200, "path built from the entry id");
    // Restore: refusals.
    expect(slot_lookup_restore(root, identity, "dddddddddddddddddddddddddddddddd", got, path, err) == SlotStatus::NotFound,
           "unknown id refused");
    expect(slot_lookup_restore(root, identity, "../" + id_b, got, path, err) == SlotStatus::Invalid, "bad id refused");
    expect(slot_lookup_restore(root, other_identity, id_b, got, path, err) == SlotStatus::NotFound,
           "other model has no such id");

    // Identity mismatch inside a manifest (edited by hand) is refused.
    {
        std::vector<SlotEntry> forged = {make_entry(id_a, "someone-else", 1000, 100)};
        slot_manifest_write(dir, forged, err);
        expect(slot_lookup_restore(root, identity, id_a, got, path, err) == SlotStatus::NotFound,
               "identity mismatch refused");
        std::vector<SlotEntry> restored = {make_entry(id_a, identity, 1000, 100), make_entry(id_b, identity, 2000, 200)};
        slot_manifest_write(dir, restored, err);
    }

    // Size mismatch is refused.
    write_file(dir + "/" + id_a, 99);
    expect(slot_lookup_restore(root, identity, id_a, got, path, err) == SlotStatus::Invalid, "size mismatch refused");
    write_file(dir + "/" + id_a, 100);
    expect(slot_lookup_restore(root, identity, id_a, got, path, err) == SlotStatus::Ok, "size match accepted");

    // A symlink with the right target size is refused.
    {
        write_file((base / "target.bin").string(), 200);
        fs::remove(dir + "/" + id_b);
        fs::create_symlink(base / "target.bin", dir + "/" + id_b, ec);
        if (!ec) {
            expect(slot_lookup_restore(root, identity, id_b, got, path, err) == SlotStatus::Invalid, "symlink refused");
        } else {
            expect(false, "could not create the test symlink");
        }
        fs::remove(dir + "/" + id_b);
        fs::create_directory(dir + "/" + id_b);
        expect(slot_lookup_restore(root, identity, id_b, got, path, err) == SlotStatus::Invalid, "directory refused");
        fs::remove(dir + "/" + id_b);
    }

    // A corrupt manifest is an error for save and restore, and is not reset.
    {
        std::ofstream(dir + "/manifest.json") << "{not json";
        expect(slot_lookup_restore(root, identity, id_a, got, path, err) == SlotStatus::Corrupt, "corrupt manifest: restore");
        expect(slot_commit_save(dir, make_entry(id_b, identity, 3000, 1), pruned, err) == SlotStatus::Corrupt,
               "corrupt manifest: save");
        std::ifstream in(dir + "/manifest.json");
        std::string text((std::istreambuf_iterator<char>(in)), std::istreambuf_iterator<char>());
        expect(text == "{not json", "corrupt manifest left as it was");
        std::ofstream(dir + "/manifest.json") << R"({"entries":[{"id":"../x","model_identity":"m","slot":0,"n_tokens":0,"n_bytes":0,"created_unix":0}]})";
        expect(slot_manifest_load(dir, entries, err) == SlotStatus::Corrupt, "entry with a bad id is corrupt");
    }

    fs::remove_all(base, ec);
    if (failures == 0) {
        std::puts("slot_cache_test: ok");
    }
    return failures == 0 ? 0 : 1;
}
