#pragma once

// Helpers for the SlotCache RPC. They are header-only and free of llama.cpp
// types so the filename rules can be tested on their own.

#include <cstdint>
#include <filesystem>
#include <string>
#include <system_error>

namespace llama_grpc {

// slot_filename_valid accepts a single path component and nothing else. It
// applies the rules of llama.cpp's fs_validate_filename with subdirectories
// refused, and also rejects "." and "..". A name that passes cannot climb out
// of the save directory, name an absolute path or hide a NUL byte.
inline bool slot_filename_valid(const std::string& name) {
    if (name.empty() || name.size() > 255) {
        return false;
    }
    if (name == "." || name == "..") {
        return false;
    }

    size_t i = 0;
    while (i < name.size()) {
        const unsigned char b = static_cast<unsigned char>(name[i]);
        uint32_t cp = 0;
        size_t len = 0;
        if (b < 0x80) {
            cp = b;
            len = 1;
        } else if ((b & 0xE0) == 0xC0) {
            cp = b & 0x1F;
            len = 2;
        } else if ((b & 0xF0) == 0xE0) {
            cp = b & 0x0F;
            len = 3;
        } else if ((b & 0xF8) == 0xF0) {
            cp = b & 0x07;
            len = 4;
        } else {
            return false;
        }
        if (i + len > name.size()) {
            return false;
        }
        for (size_t k = 1; k < len; k++) {
            const unsigned char c = static_cast<unsigned char>(name[i + k]);
            if ((c & 0xC0) != 0x80) {
                return false;
            }
            cp = (cp << 6) | (c & 0x3F);
        }
        // Overlong encodings could smuggle a '/' or a NUL past the checks below.
        if ((len == 2 && cp < 0x80) || (len == 3 && cp < 0x800) || (len == 4 && cp < 0x10000)) {
            return false;
        }
        if (cp <= 0x1F || cp == 0x7F || (cp >= 0x80 && cp <= 0x9F) ||  // control characters, NUL included
            cp == 0xFF0E || cp == 0x2215 || cp == 0x2216 ||             // look-alikes of '.', '/' and '\'
            (cp >= 0xD800 && cp <= 0xDFFF) || cp > 0x10FFFF ||         // surrogates, out of range
            cp == 0xFFFD || cp == 0xFEFF ||                             // replacement character, BOM
            cp == '/' || cp == '\\' || cp == ':' || cp == '*' || cp == '?' ||
            cp == '"' || cp == '<' || cp == '>' || cp == '|') {
            return false;
        }
        i += len;
    }

    // Windows strips a leading or trailing space and a trailing dot.
    return name.front() != ' ' && name.back() != ' ' && name.back() != '.';
}

// resolve_slot_file joins the validated filename onto the save directory and
// checks that the result still lives directly in that directory once symbolic
// links are resolved. On success it stores the full path in out. On failure it
// returns false and stores a short reason in err.
inline bool resolve_slot_file(const std::string& dir, const std::string& name, std::string& out, std::string& err) {
    namespace fs = std::filesystem;
    if (dir.empty()) {
        err = "slot_save_path is not set for this model";
        return false;
    }
    if (!slot_filename_valid(name)) {
        err = "invalid filename: use a single file name without directories";
        return false;
    }

    std::error_code ec;
    const fs::path root = fs::weakly_canonical(fs::path(dir), ec);
    if (ec) {
        err = "cannot resolve the slot save directory";
        return false;
    }
    const fs::path target = fs::weakly_canonical(root / fs::u8path(name), ec);
    if (ec) {
        err = "cannot resolve the slot file path";
        return false;
    }
    // equivalent() needs both paths to exist, so compare the normalised text.
    if (target.parent_path().lexically_normal() != root.lexically_normal() ||
        target.filename().empty()) {
        err = "filename resolves outside the slot save directory";
        return false;
    }
    out = target.string();
    return true;
}

} // namespace llama_grpc
