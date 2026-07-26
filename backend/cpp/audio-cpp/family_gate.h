#pragma once

// Decides which audio.cpp family a model path belongs to, and refuses paths
// this backend must not claim. Standard library only.
//
// This is the guard against issue #9287. A model config with no explicit
// backend makes LocalAI probe every installed backend and bind to the first
// Load that succeeds, so accepting an arbitrary GGUF here would capture
// unrelated LLMs. audio.cpp GGUFs carry an audiocpp.model_spec.family metadata
// key; llama.cpp GGUFs do not.

#include <string>

namespace audiocpp_backend {

// True when the path ends in ".gguf", case insensitively, and has a stem.
bool path_looks_like_gguf(const std::string &path);

struct FamilyDecision {
    bool ok = false;
    std::string family;
    // Set when ok is false. Suitable verbatim as an INVALID_ARGUMENT message.
    std::string error;
};

// Precedence:
//   1. an explicit `family:` option, so a user can override wrong metadata;
//   2. for a GGUF, the family embedded in audiocpp.model_spec.family;
//   3. otherwise refuse.
// A directory path never consults embedded metadata: there is no single GGUF
// to read it from.
FamilyDecision decide_family(bool path_is_gguf, const std::string &embedded_family,
                             const std::string &configured_family);

} // namespace audiocpp_backend
