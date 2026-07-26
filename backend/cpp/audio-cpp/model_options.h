#pragma once

// Parses the model YAML's `options:` list (ModelOptions.Options in
// backend.proto) into a struct. Standard library only: this unit is compiled
// and tested by backend/cpp/run-unit-tests.sh without an audio.cpp checkout.

#include <map>
#include <string>
#include <vector>

namespace audiocpp_backend {

struct ModelOptions {
    // audio.cpp model family. Empty means "derive from the GGUF's embedded
    // audiocpp.model_spec.family key"; a non-GGUF path with an empty family is
    // rejected at load time, not here.
    std::string family;
    // Pins the audio.cpp task, overriding RPC-based routing. Empty means route.
    std::string task;
    // ggml backend: cpu, cuda, vulkan, metal, best.
    std::string backend = "cpu";
    int device = 0;
    // True once a `device:` entry has been seen. 0 is both the default and a
    // legitimate device index, so the value alone cannot tell an explicit
    // `device:0` from an unset option, and a caller merging in its own fallback
    // would silently override the explicit choice.
    bool device_set = false;
    // 0 means "let the runtime decide".
    int threads = 0;
    std::string model_spec_override;
    // 0 disables the run guard's fail-fast, restoring an unbounded wait.
    int busy_timeout_ms = 0;
    // `load.<key>:<value>` entries, prefix stripped.
    std::map<std::string, std::string> load_options;
    // `session.<key>:<value>` entries, prefix stripped.
    std::map<std::string, std::string> session_options;
};

struct ParsedOptions {
    ModelOptions options;
    // Non-empty means the caller must fail the load with INVALID_ARGUMENT.
    std::string error;
};

ParsedOptions parse_model_options(const std::vector<std::string> &entries);

} // namespace audiocpp_backend
