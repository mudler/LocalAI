#include "slot_cache.h"

#include <cstdio>
#include <cstdlib>
#include <filesystem>
#include <fstream>
#include <string>

static int failures = 0;

static void expect(bool cond, const char* what) {
    if (!cond) {
        std::fprintf(stderr, "FAIL: %s\n", what);
        failures++;
    }
}

int main() {
    namespace fs = std::filesystem;
    using llama_grpc::slot_filename_valid;

    expect(slot_filename_valid("prompt.bin"), "plain name");
    expect(slot_filename_valid("ha-system_prompt.v2.bin"), "name with dash, underscore and dots");
    expect(slot_filename_valid("caf\xC3\xA9.bin"), "valid UTF-8");

    expect(!slot_filename_valid(""), "empty");
    expect(!slot_filename_valid("."), "dot");
    expect(!slot_filename_valid(".."), "dot dot");
    expect(!slot_filename_valid("../x"), "parent traversal");
    expect(!slot_filename_valid("a/b"), "slash");
    expect(!slot_filename_valid("a\\b"), "backslash");
    expect(!slot_filename_valid("/etc/passwd"), "absolute path");
    expect(!slot_filename_valid("C:\\x"), "drive path");
    expect(!slot_filename_valid(std::string("a\0b", 3)), "NUL byte");
    expect(!slot_filename_valid("a\nb"), "control character");
    expect(!slot_filename_valid("a\x7f"), "DEL");
    expect(!slot_filename_valid(" a"), "leading space");
    expect(!slot_filename_valid("a "), "trailing space");
    expect(!slot_filename_valid("a."), "trailing dot");
    expect(!slot_filename_valid("a*b"), "wildcard");
    expect(!slot_filename_valid("\xC0\xAF"), "overlong slash");
    expect(!slot_filename_valid("\xFF"), "invalid UTF-8");
    expect(!slot_filename_valid("\xE2\x88\x95"), "division slash look-alike");
    expect(!slot_filename_valid(std::string(256, 'a')), "too long");

    // Containment, on a scratch directory under the system temp dir.
    std::error_code ec;
    const fs::path base = fs::temp_directory_path() / "localai_slot_cache_test";
    fs::remove_all(base, ec);
    fs::create_directories(base / "save", ec);
    fs::create_directories(base / "other", ec);
    std::ofstream(base / "other" / "secret.bin") << "x";

    std::string out, err;
    const std::string dir = (base / "save").string();

    expect(llama_grpc::resolve_slot_file(dir, "ok.bin", out, err), "resolve a new file");
    expect(out == (fs::weakly_canonical(base / "save") / "ok.bin").string(), "resolved path is inside the directory");

    expect(!llama_grpc::resolve_slot_file("", "ok.bin", out, err), "empty directory refuses");
    expect(!llama_grpc::resolve_slot_file(dir, "../other/secret.bin", out, err), "traversal refused");
    expect(!llama_grpc::resolve_slot_file(dir, (base / "other" / "secret.bin").string(), out, err), "absolute path refused");

    // A symbolic link inside the directory that points outside it must not resolve.
    fs::create_symlink(base / "other" / "secret.bin", base / "save" / "link.bin", ec);
    if (!ec) {
        expect(!llama_grpc::resolve_slot_file(dir, "link.bin", out, err), "symlink escape refused");
    }

    fs::remove_all(base, ec);
    if (failures == 0) {
        std::puts("slot_cache_test: ok");
    }
    return failures == 0 ? 0 : 1;
}
