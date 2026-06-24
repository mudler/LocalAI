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
    // ggml backend: cpu, cuda, hip (or rocm), vulkan, metal, best.
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
    // How long AudioTranscriptionLive waits for the next audio frame before it
    // cancels the stream and gives the model's lane back. 0 means NO LIMIT.
    //
    // It exists because that RPC holds the lane for the whole stream, so a peer
    // that stops sending WITHOUT closing blocks every other request against this
    // model for as long as its socket stays up. No other RPC can do that: they
    // hold the lane across compute, which ends on its own.
    //
    // 30 seconds, and the number is picked from what the only in-tree client
    // does. core/http/endpoints/openai/realtime.go drives a 300 ms ticker and
    // feeds every tick that produced new audio while a turn is open, so 30 s of
    // silence is a hundred ticks that delivered nothing: the peer is gone, or
    // its socket is wedged. It is also comfortably longer than any pause a
    // speaker takes mid-utterance, which is the case that must never be cut off,
    // and backend.proto allows one stream to span many utterances, so a client
    // that pauses for longer than this between them should raise it rather than
    // discover it. Lowering it below a few seconds risks cancelling a live
    // speaker; 0 turns the limit off for a client that legitimately idles.
    int live_idle_timeout_ms = 30000;
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
