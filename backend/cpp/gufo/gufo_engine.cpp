#include "gufo_engine.h"

#include <atomic>
#include <chrono>
#include <optional>
#include <string_view>
#include <utility>

#include "base64.h"
#include "src/cli/serve/chat_output.hpp"
#include "src/cli/serve/inference_backend.hpp"
#include "src/core/reasoning.hpp"
#include "src/core/sampling.hpp"
#include "src/core/utf8.hpp"

namespace gs = gufo::server;
namespace gt = gufo::tokenization;

namespace gufo_backend {

namespace {

using FinishReason = gs::TextGenerationBackend::FinishReason;

gt::ChatRole RoleFrom(const std::string& role) {
  if (role == "system") return gt::ChatRole::kSystem;
  if (role == "developer") return gt::ChatRole::kDeveloper;
  if (role == "assistant") return gt::ChatRole::kAssistant;
  if (role == "tool") return gt::ChatRole::kTool;
  return gt::ChatRole::kUser;
}

gs::ChatRequest::ToolChoice ToolChoiceFrom(const std::string& choice) {
  // A JSON object naming one function is treated as "required": v1 cannot pin a single tool.
  if (choice == "none") return gs::ChatRequest::ToolChoice::kNone;
  if (choice == "required" || (!choice.empty() && choice[0] == '{')) return gs::ChatRequest::ToolChoice::kRequired;
  return gs::ChatRequest::ToolChoice::kAuto;
}

gt::ChatMessage ToGufoMessage(const PlainMessage& m) {
  gt::ChatMessage out(RoleFrom(m.role), m.content);
  out.name = m.name;
  out.tool_call_id = m.tool_call_id;
  // gufo's own OpenAI parser keeps reasoning only on assistant turns; a
  // thought on any other role would be rendered into the prompt verbatim.
  if (out.role == gt::ChatRole::kAssistant) out.thought = m.reasoning;
  for (const PlainToolCall& call : m.tool_calls) {
    gt::ChatMessage::ToolCall tc;
    tc.id = call.id;
    tc.name = call.name;
    for (const ToolArg& a : call.args) tc.arguments.push_back({a.name, a.value, a.is_string});
    out.tool_calls.push_back(std::move(tc));
  }
  for (const std::string& b64 : m.images_b64) {
    std::vector<std::uint8_t> bytes;
    if (!Base64Decode(b64, &bytes))
      throw EngineError(ErrorKind::kInvalidArgument, "gufo: an image is not valid base64");
    gt::ChatMessage::ImagePart part;
    // LocalAI delivers images separately from the text, so they all go after
    // the message content, in input order.
    part.offset = out.content.size();
    part.bytes = std::make_shared<const std::vector<std::uint8_t>>(std::move(bytes));
    out.images.push_back(std::move(part));
  }
  return out;
}

std::optional<gufo::ReasoningEffort> EffortFrom(const std::string& name) {
  if (name == "minimal") return gufo::ReasoningEffort::kMinimal;
  if (name == "low") return gufo::ReasoningEffort::kLow;
  if (name == "medium") return gufo::ReasoningEffort::kMedium;
  if (name == "high") return gufo::ReasoningEffort::kHigh;
  if (name == "xhigh") return gufo::ReasoningEffort::kXHigh;
  if (name == "max") return gufo::ReasoningEffort::kMax;
  return std::nullopt;
}

// Mirrors gufo's openai_chat.cpp: request fields are resolved first (an effort
// implies thinking on, "off"/"none" means thinking off, and a contradiction is
// an error), then every field the request left unset takes the load default.
gufo::ReasoningOptions ResolveRequestReasoning(const GenRequest& req, const gufo::ReasoningOptions& defaults) {
  gufo::ReasoningOptions r;
  r.enabled = req.thinking;
  if (!req.reasoning_effort.empty()) {
    bool enable = true;
    if (req.reasoning_effort == "off" || req.reasoning_effort == "none") {
      enable = false;
    } else {
      const auto effort = EffortFrom(req.reasoning_effort);
      if (!effort)
        throw EngineError(ErrorKind::kInvalidArgument,
                          "gufo: reasoning_effort must be off, none, minimal, low, medium, high, xhigh or max");
      r.effort = effort;
    }
    if (r.enabled.has_value() && *r.enabled != enable)
      throw EngineError(ErrorKind::kInvalidArgument,
                        "gufo: reasoning controls disagree about whether thinking is enabled");
    r.enabled = enable;
  }
  if (!r.enabled.has_value()) r.enabled = defaults.enabled;
  if (!r.effort.has_value()) r.effort = defaults.effort;
  if (!r.preserve_thinking.has_value()) r.preserve_thinking = defaults.preserve_thinking;
  return r;
}

gufo::sampling::SamplingConfig ApplySampling(gufo::sampling::SamplingConfig base, const SamplingRequest& s) {
  if (s.temperature > 0) base.temperature = s.temperature;
  if (s.top_p > 0) base.top_p = s.top_p;
  if (s.top_k > 0) base.top_k = s.top_k;
  if (s.min_p > 0) base.min_p = s.min_p;
  if (s.repeat_penalty > 0) base.repeat_penalty = s.repeat_penalty;
  if (s.frequency_penalty != 0) base.frequency_penalty = s.frequency_penalty;
  if (s.presence_penalty != 0) base.presence_penalty = s.presence_penalty;
  // LocalAI sends 0 for "no seed"; gufo's own default of -1 means random.
  if (s.seed > 0) base.seed = s.seed;
  return base;
}

std::string FinishName(FinishReason r) {
  switch (r) {
    case FinishReason::kLength: return "length";
    case FinishReason::kCancelled: return "cancelled";
    case FinishReason::kStop:
    case FinishReason::kStopSequence: return "stop";
  }
  return "stop";
}

[[noreturn]] void Rethrow(const gs::TextGenerationError& e) {
  using Code = gs::TextGenerationErrorCode;
  switch (e.code()) {
    case Code::kQueueFull:
    case Code::kClientQueueFull:
    case Code::kOutputLimit:
    case Code::kOutputBackpressure:
      throw EngineError(ErrorKind::kResourceExhausted, e.what());
    case Code::kDeadlineExceeded:
      throw EngineError(ErrorKind::kDeadlineExceeded, e.what());
    case Code::kToolChoiceUnsatisfied:
      throw EngineError(ErrorKind::kFailedPrecondition, e.what());
    case Code::kSchedulerStopping:
      break;
  }
  throw EngineError(ErrorKind::kInternal, e.what());
}

void CopyUsage(const gs::TextGenerationBackend::Result& result, GenResult* out) {
  out->prompt_tokens = result.prompt_tokens;
  out->completion_tokens = result.completion_tokens;
  out->cached_tokens = result.cached_prompt_tokens;
  out->prefill_ms = result.prefill_ms;
  out->decode_ms = result.decode_ms;
}

}  // namespace

// Only immutable-after-Load state lives here: Generate runs concurrently on the
// gRPC thread pool and keeps every per-request value on its own stack.
struct Engine::Impl {
  gs::InferenceBackend backend;
  std::atomic<bool> loaded{false};
};

Engine::Engine() : impl_(std::make_unique<Impl>()) {}
Engine::~Engine() = default;

bool Engine::loaded() const { return impl_->loaded.load(std::memory_order_acquire); }

bool Engine::Load(const LoadArgs& args, std::string* error) {
  const Options& o = args.options;

  // Same rule as gufo's ResolveReasoningDefaults: an effort implies thinking
  // on, so it cannot be combined with think:false.
  gufo::ReasoningOptions reasoning_overrides;
  reasoning_overrides.enabled = o.think;
  reasoning_overrides.preserve_thinking = o.preserve_thinking;
  if (!o.reasoning_effort.empty()) {
    const auto effort = EffortFrom(o.reasoning_effort);
    if (!effort) {
      *error = "gufo: reasoning_effort must be one of minimal, low, medium, high, xhigh, max";
      return false;
    }
    if (o.think == false) {
      *error = "gufo: reasoning_effort cannot be set while think is false";
      return false;
    }
    reasoning_overrides.enabled = true;
    reasoning_overrides.effort = effort;
  }

  gs::TextSpeculativeConfig spec;
  if (o.speculative == "dflash2") spec.backend = gs::TextSpeculativeBackend::kDFlash;
  else if (o.speculative == "dspark") spec.backend = gs::TextSpeculativeBackend::kDSpark;
  else if (o.speculative == "mtp") spec.backend = gs::TextSpeculativeBackend::kMtp;
  else spec.backend = gs::TextSpeculativeBackend::kDisabled;
  spec.draft_model_path = o.draft_model;
  spec.max_draft_tokens = static_cast<std::uint32_t>(o.draft_tokens);
  spec.min_draft_tokens = static_cast<std::uint32_t>(o.min_draft_tokens);

  gs::TextSchedulerPolicy scheduler;
  scheduler.max_pending_requests = o.max_pending;
  scheduler.max_pending_requests_per_client = o.max_pending_per_client;
  scheduler.max_output_bytes_per_request = o.max_output_bytes;
  scheduler.max_buffered_output_bytes_per_request = o.max_buffered_output_bytes;
  scheduler.max_buffered_output_bytes_total = o.max_buffered_output_bytes_total;
  scheduler.request_timeout = std::chrono::milliseconds(static_cast<std::int64_t>(o.request_timeout_ms));

  gs::TextDiskCacheConfig disk;
  disk.directory = o.cache_disk;
  if (o.cache_disk_bytes > 0) disk.capacity_bytes = o.cache_disk_bytes;

  if (!impl_->backend.load(args.model_path, error, args.max_context, o.sessions, gs::TextPrefillPolicy{}, scheduler,
                           spec, disk, args.mmproj_path)) {
    return false;
  }
  impl_->backend.set_model_id(args.model_path);

  // Start from the model's own defaults so an unset option keeps them.
  gufo::ReasoningOptions reasoning = impl_->backend.reasoning_defaults();
  if (reasoning_overrides.enabled) reasoning.enabled = reasoning_overrides.enabled;
  if (reasoning_overrides.effort) reasoning.effort = reasoning_overrides.effort;
  if (reasoning_overrides.preserve_thinking) reasoning.preserve_thinking = reasoning_overrides.preserve_thinking;
  impl_->backend.set_reasoning_defaults(reasoning);
  impl_->loaded.store(true, std::memory_order_release);
  return true;
}

GenResult Engine::Generate(const GenRequest& req, const std::function<bool(const Piece&)>& on_piece,
                           const std::function<bool()>& is_cancelled) {
  if (!loaded()) throw EngineError(ErrorKind::kFailedPrecondition, "gufo: no model is loaded");
  gs::InferenceBackend& backend = impl_->backend;
  const bool raw = req.messages.empty();
  if (raw && req.raw_prompt.empty()) throw EngineError(ErrorKind::kInvalidArgument, "gufo: the request has no messages and no prompt");

  gs::ChatRequest chat;
  if (!raw) {
    chat.client_id = req.client_id;
    chat.tool_choice = ToolChoiceFrom(req.tool_choice);
    chat.stop_sequences = req.stop;
    for (const PlainMessage& m : req.messages) chat.messages.push_back(ToGufoMessage(m));
    for (const PlainTool& t : req.tools) {
      gt::ChatTool tool;
      tool.name = t.name;
      tool.description = t.description;
      tool.parameters_json = t.parameters_json;
      tool.definition_json = t.definition_json;
      chat.tools.push_back(std::move(tool));
    }
    chat.reasoning = ResolveRequestReasoning(req, backend.reasoning_defaults());
  }

  const auto sampling = ApplySampling(backend.sampling_defaults().Resolve(chat.reasoning.enabled), req.sampling);
  const bool streaming = static_cast<bool>(on_piece);
  const auto initial = raw ? gs::TextGenerationBackend::InitialOutputState::kContent : backend.initial_output_state(chat);


  // Everything that can throw is built before start_*: once a request is
  // submitted, Wait must run so its output is drained.
  gs::chat_output::StreamingTextFilter filter(initial, [&](std::string_view piece, bool is_reasoning) {
    return piece.empty() || on_piece(Piece{std::string(piece), is_reasoning});
  });
  gufo::core::Utf8Decoder decoder;

  GenResult out;
  try {
    std::shared_ptr<gs::TextGenerationBackend::GenerationRequest> generation;
    // gufo copies is_cancelled into the request and polls it from its
    // scheduler thread; passing it directly (not a lambda over this frame)
    // keeps that copy valid even if the scheduler polls once more after Wait.
    if (raw) {
      generation = backend.start_complete(req.raw_prompt, req.max_tokens, sampling, is_cancelled, streaming,
                                          /*ignore_eos=*/false, req.client_id, req.stop);
    } else {
      generation = backend.start_chat(chat, req.max_tokens, sampling, is_cancelled, streaming);
    }

    // With stream_output=false gufo publishes no pieces and Result.text holds
    // the whole output, so the non-streaming path waits without a callback.
    gs::TextGenerationBackend::Result result;
    if (!streaming) {
      result = generation->Wait();
    } else if (raw) {
      result = generation->Wait([&](std::string_view piece) {
        if (is_cancelled && is_cancelled()) return false;
        const std::string text = decoder.Push(piece);
        return text.empty() || on_piece(Piece{text, false});
      });
    } else {
      result = generation->Wait([&](std::string_view piece) {
        if (is_cancelled && is_cancelled()) return false;
        return filter.Push(piece);
      });
    }

    CopyUsage(result, &out);
    // Like gufo's HTTP handlers, a cancelled request ends here: the client is
    // gone, and a partial output must not trip the tool_choice check.
    if (result.cancelled) {
      out.finish_reason = "cancelled";
      return out;
    }
    // gufo falls back to a deferred path that returns an empty Result when it
    // cannot render the prompt; every submitted request has prompt_tokens > 0.
    if (result.prompt_tokens == 0) throw EngineError(ErrorKind::kInternal, "gufo: the prompt could not be prepared");

    if (raw) {
      if (streaming) {
        const std::string tail = decoder.Push({}, true);
        if (!tail.empty()) on_piece(Piece{tail, false});
      } else {
        out.content = decoder.Push(result.text, true);
      }
      out.finish_reason = FinishName(result.finish_reason);
      return out;
    }

    std::string text;
    if (streaming) {
      (void)filter.Push({}, /*final=*/true);
      text = std::string(filter.raw());
    } else {
      // The filter would sanitize the bytes in the streaming path; protobuf
      // strings must be valid UTF-8 either way.
      text = decoder.Push(result.text, true);
    }
    const bool enforce_required = result.finish_reason != FinishReason::kStopSequence;
    const auto parsed = gs::chat_output::ParseGeneration(text, initial, chat.tools, chat.tool_choice, enforce_required);
    if (streaming) (void)filter.Finish(parsed.hide_tool_markup, parsed.text);

    out.content = parsed.text;
    out.reasoning = parsed.reasoning_content;
    for (const auto& call : parsed.tool_calls) {
      PlainToolCall pc;
      pc.id = call.id;
      pc.name = call.name;
      for (const auto& a : call.arguments) pc.args.push_back({a.name, a.value, a.is_string});
      out.tool_calls.push_back(std::move(pc));
    }
    out.finish_reason = out.tool_calls.empty() ? FinishName(result.finish_reason) : "tool_calls";
  } catch (const gs::TextGenerationError& e) {
    Rethrow(e);
  } catch (const std::invalid_argument& e) {
    throw EngineError(ErrorKind::kInvalidArgument, e.what());
  } catch (const std::length_error& e) {
    throw EngineError(ErrorKind::kInvalidArgument, std::string("gufo: context length exceeded: ") + e.what());
  }
  return out;
}

std::vector<std::uint32_t> Engine::Tokenize(const std::string& text) const { return impl_->backend.tokenize(text); }

}  // namespace gufo_backend
