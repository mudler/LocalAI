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

// True when `family` can run weights stored as `dtype`, where dtype is the
// string a TensorMetadata carries ("f32", "f16", "q8_0", "i64", ...).
//
// This is a LIST OF FAMILIES THAT CRASH THE PROCESS, not a list of families that
// perform badly. It exists because the failure is not an exception: loading a
// supertonic package whose weights are f16 or q8_0 reaches ggml_concat with one
// f16 operand and one f32 one, and ggml_abort takes the backend down with
// SIGABRT on the FIRST request. Nothing upstream of the load can catch that, so
// an operator sees a model that loaded successfully and a backend that dies on
// every request with no status and no message.
//
// A family with no entry is unrestricted, which is every family but one.
//
// Split out of loaded_model.cpp, where the caller lives, so that the policy is
// stdlib-only and can be held by a test: the caller needs a real GGUF on disk
// and an engine, and neither is available to a unit test. What the test pins is
// that the table says what it is meant to say, so widening it is a deliberate
// act rather than a typo. It CANNOT pin the removal criterion, which is
// "upstream fixed it": no test can know that without downloading the package and
// synthesising, so that step stays a documented manual one at the table itself.
bool weight_dtype_is_supported(const std::string &family, const std::string &dtype);

// The dtypes `family` is restricted to, as "f32, i64", or empty when it is not
// restricted at all. For the refusal message, so the operator is told what to
// look for rather than only what is wrong.
std::string supported_weight_dtypes(const std::string &family);

} // namespace audiocpp_backend
