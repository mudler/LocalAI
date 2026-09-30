// gRPC-free wrapper over gufo's InferenceBackend. Everything the gRPC layer
// needs from gufo goes through here, so grpc-server.cpp never includes a gufo
// header and this file can be compile-checked without gRPC.
#pragma once

#include <cstddef>
#include <cstdint>
#include <functional>
#include <memory>
#include <optional>
#include <stdexcept>
#include <string>
#include <vector>

#include "gufo_options.h"
#include "message_map.h"

namespace gufo_backend {

enum class ErrorKind { kInvalidArgument, kResourceExhausted, kDeadlineExceeded, kFailedPrecondition, kInternal };

class EngineError : public std::runtime_error {
 public:
  EngineError(ErrorKind kind, const std::string& message) : std::runtime_error(message), kind_(kind) {}
  ErrorKind kind() const { return kind_; }

 private:
  ErrorKind kind_;
};

struct LoadArgs {
  std::string model_path;
  std::string mmproj_path;        // empty: gufo looks for mmproj-BF16.gguf beside the model
  std::uint32_t max_context = 0;  // 0: the GGUF's native context
  Options options;
};

// Zero means "not set": the model's own preset applies.
struct SamplingRequest {
  float temperature = 0, top_p = 0, min_p = 0;
  float repeat_penalty = 0, frequency_penalty = 0, presence_penalty = 0;
  int top_k = 0;
  int seed = 0;
};

struct GenRequest {
  std::vector<PlainMessage> messages;
  std::vector<PlainTool> tools;
  std::string tool_choice;  // "", "auto", "none", "required", or a JSON object
  std::string raw_prompt;   // non-empty and no messages: raw completion
  std::vector<std::string> stop;
  SamplingRequest sampling;
  std::size_t max_tokens = 0;  // 0: until EOS or the context is full
  std::optional<bool> thinking;
  std::string reasoning_effort;  // "", off, none, minimal, low, medium, high, xhigh, max
  std::string client_id = "anonymous";
};

struct Piece {
  std::string text;
  bool is_reasoning = false;
};

struct GenResult {
  std::string content;
  std::string reasoning;
  std::string finish_reason;  // stop | length | cancelled | tool_calls
  std::vector<PlainToolCall> tool_calls;
  std::size_t prompt_tokens = 0;
  std::size_t completion_tokens = 0;
  std::size_t cached_tokens = 0;
  double prefill_ms = 0;
  double decode_ms = 0;
};

class Engine {
 public:
  Engine();
  ~Engine();
  Engine(const Engine&) = delete;
  Engine& operator=(const Engine&) = delete;

  // Called once per Engine; destroy the Engine to unload the model.
  bool Load(const LoadArgs& args, std::string* error);
  bool loaded() const;

  // Safe to call concurrently. on_piece may be empty (non-streaming).
  // Returning false from it, or true from is_cancelled, cancels the request;
  // is_cancelled is also polled from gufo's scheduler thread, so it must be
  // thread-safe. Throws EngineError.
  GenResult Generate(const GenRequest& request, const std::function<bool(const Piece&)>& on_piece,
                     const std::function<bool()>& is_cancelled);

  std::vector<std::uint32_t> Tokenize(const std::string& text) const;

 private:
  struct Impl;
  std::unique_ptr<Impl> impl_;
};

}  // namespace gufo_backend
