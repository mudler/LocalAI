#include "family_gate.h"

#include <cctype>

namespace audiocpp_backend {
namespace {

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
