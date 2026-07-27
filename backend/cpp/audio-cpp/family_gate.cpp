#include "family_gate.h"

#include <cctype>
#include <cstddef>

namespace audiocpp_backend {
namespace {

// PRECONDITION: `suffix` must already be lowercase. Both sides are folded, so
// this reads as symmetric, but only `value` can carry case in practice and a
// caller passing ".GGUF" would still work today for that reason alone. Do not
// rely on it: the fold on the suffix side is the only thing standing between
// this and a helper that answers false for every input, and it is not covered
// by any test, because with a lowercase suffix no input can distinguish it.
bool ends_with_ci(const std::string &value, const std::string &suffix) {
    if (value.size() <= suffix.size()) {
        return false; // a bare ".gguf" is an extension, not a model file
    }
    const size_t offset = value.size() - suffix.size();
    for (size_t i = 0; i < suffix.size(); ++i) {
        const auto lhs = static_cast<unsigned char>(value[offset + i]);
        const auto rhs = static_cast<unsigned char>(suffix[i]);
        if (std::tolower(lhs) != std::tolower(rhs)) {
            return false;
        }
    }
    return true;
}

} // namespace

bool path_looks_like_gguf(const std::string &path) {
    return ends_with_ci(path, ".gguf");
}

FamilyDecision decide_family(bool path_is_gguf, const std::string &embedded_family,
                             const std::string &configured_family) {
    FamilyDecision decision;

    if (!configured_family.empty()) {
        decision.ok = true;
        decision.family = configured_family;
        return decision;
    }

    if (path_is_gguf) {
        if (!embedded_family.empty()) {
            decision.ok = true;
            decision.family = embedded_family;
            return decision;
        }
        decision.error =
            "audio-cpp: this GGUF carries no 'audiocpp.model_spec.family' "
            "metadata key, so it is not an audio.cpp model. Convert it with "
            "audiocpp_gguf, or name the family explicitly with the model option "
            "'family:<name>'";
        return decision;
    }

    decision.error =
        "audio-cpp: a model path that is not a standalone audio.cpp GGUF needs "
        "an explicit 'family:<name>' model option, because the audio.cpp family "
        "cannot be inferred from a safetensors or package directory";
    return decision;
}

namespace {

// Families that ABORT THE PROCESS on a weight dtype they cannot handle, and the
// dtypes they can. See the header for why this is a list of crashes rather than
// a list of preferences.
//
// supertonic: upstream's docs/gguf.md:90 records its 16-bit GGUF column as
// "---", i.e. NOT TESTED, and its q8_0 as "No (unsupported weight dtype)". Only
// the `orig` package is marked Pass, and its 698 weight tensors are f32 while
// its 72 index and shape constants are i64. Attributed rather than assumed: the
// abort is identical through the unary TTS RPC and through TTSStream, so it is
// the packaging and not the streaming path.
//
// TO REMOVE AN ENTRY: bump AUDIO_CPP_VERSION past a fix, load a package in the
// refused dtype, and synthesise. If audio comes out, delete the entry. No test
// can do that for you, which is exactly why it is written here: the test beside
// this file pins WHAT the table says, not whether upstream has moved on. Do not
// widen an entry without running that, because what it prevents is a process
// death rather than a wrong answer.
struct DtypeAllowList {
    // NULL TERMINATED, and the terminator occupies one of these slots: both
    // loops below stop at the first nullptr and have no other bound, so an entry
    // that named three dtypes would leave them reading past the end of the
    // array. That is undefined behaviour rather than a wrong answer, and it is
    // one keystroke away from any edit that widens an entry, so the terminator
    // is asserted at compile time below rather than trusted.
    static constexpr std::size_t kSlots = 3;

    const char *family;
    const char *allowed[kSlots];
};

constexpr DtypeAllowList kDtypeAllowLists[] = {
    {"supertonic", {"f32", "i64", nullptr}},
};

constexpr bool allow_lists_are_terminated() {
    for (const auto &entry : kDtypeAllowLists) {
        if (entry.allowed[DtypeAllowList::kSlots - 1] != nullptr) {
            return false;
        }
    }
    return true;
}

static_assert(allow_lists_are_terminated(),
              "every DtypeAllowList must leave its last slot null: the lookups "
              "below stop at the first nullptr and would otherwise read past "
              "the end of the array");

const DtypeAllowList *find_allow_list(const std::string &family) {
    for (const auto &entry : kDtypeAllowLists) {
        if (family == entry.family) {
            return &entry;
        }
    }
    return nullptr;
}

} // namespace

bool family_has_weight_dtype_allow_list(const std::string &family) {
    return find_allow_list(family) != nullptr;
}

bool weight_dtype_is_supported(const std::string &family, const std::string &dtype) {
    const DtypeAllowList *list = find_allow_list(family);
    if (list == nullptr) {
        return true;
    }
    for (const char *const *name = list->allowed; *name != nullptr; ++name) {
        if (dtype == *name) {
            return true;
        }
    }
    return false;
}

std::string supported_weight_dtypes(const std::string &family) {
    const DtypeAllowList *list = find_allow_list(family);
    if (list == nullptr) {
        return {};
    }
    std::string out;
    for (const char *const *name = list->allowed; *name != nullptr; ++name) {
        if (!out.empty()) {
            out += ", ";
        }
        out += *name;
    }
    return out;
}

} // namespace audiocpp_backend
