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
// perform badly. It exists because the failure is not an exception: loading the
// supertonic f16 GGUF package reaches ggml_concat with one f16 operand and one
// f32 one, GGML_ASSERT(a->type == b->type) fails (external/ggml/src/ggml.c:2595)
// and ggml_abort takes the backend down with SIGABRT on the FIRST request.
// Nothing upstream of the load can catch that, so an operator sees a model that
// loaded successfully and a backend that dies on every request with no status
// and no message.
//
// EVIDENCE, per dtype, because the two are not equally attested:
//   - f16 was OBSERVED to abort here, identically through the unary TTS RPC and
//     through TTSStream, so it is the packaging and not the streaming path.
//     Upstream's docs/gguf.md:90 has supertonic's 16-bit column as "---", which
//     its own legend (:53) defines as not tested, so upstream neither confirms
//     nor contradicts it.
//   - q8_0 was NOT run here. Upstream records it as "No (unsupported weight
//     dtype)" in the same row, which is a weaker claim than the f16 abort: it
//     says the format is unusable, not that it takes the process down.
// Both are refused, because the allow list is what the family CAN run (f32 for
// weights, i64 for the shape and index constants) rather than a list of the
// dtypes that fail, and a format upstream calls unusable has no business being
// loaded either way.
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

// True when `family` has an entry in the table at all, which is the question a
// caller deciding whether to OPEN THE FILE has to ask. Distinct from
// "supported_weight_dtypes(family) is empty": that string is also empty for an
// entry with an empty allow list, and such an entry means "this family can run
// nothing", which weight_dtype_is_supported already answers by refusing every
// dtype. Deciding from the string would skip the check on precisely the entry
// that most needs it.
bool family_has_weight_dtype_allow_list(const std::string &family);

// The dtypes `family` is restricted to, as "f32, i64", or empty when it is not
// restricted at all. For the refusal message, so the operator is told what to
// look for rather than only what is wrong.
std::string supported_weight_dtypes(const std::string &family);

} // namespace audiocpp_backend
