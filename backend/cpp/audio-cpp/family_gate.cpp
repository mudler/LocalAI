#include "family_gate.h"

#include <cctype>

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

} // namespace audiocpp_backend
