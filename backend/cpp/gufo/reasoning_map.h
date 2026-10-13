// Maps LocalAI's reasoning metadata (enable_thinking, reasoning_effort) onto
// the request fields the engine reads. Standard library only, so
// reasoning_map_test.cpp runs under backend/cpp/run-unit-tests.sh.
#pragma once

#include <cctype>
#include <optional>
#include <string>

namespace gufo_backend {

inline std::string ToLower(std::string s) {
  for (char& c : s) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
  return s;
}

// An unknown value stays unset, so the model default applies instead of a guess.
inline std::optional<bool> ParseBool(const std::string& raw) {
  const std::string v = ToLower(raw);
  if (v == "true" || v == "1" || v == "yes" || v == "on") return true;
  if (v == "false" || v == "0" || v == "no" || v == "off") return false;
  return std::nullopt;
}

struct ReasoningRequest {
  std::optional<bool> thinking;
  std::string effort;  // lower-cased; empty when not sent
};

// LocalAI keeps an operator's reasoning.disable over a request level and then
// sends both enable_thinking=false and the level (ApplyReasoningEffort in
// core/config/model_config.go). The engine reads that pair as a contradiction,
// so the operator's disable wins here and the level is dropped. An effort that
// is not a known level is still passed on: the engine rejects it by name.
inline ReasoningRequest ResolveReasoning(const std::optional<std::string>& enable_thinking,
                                         const std::optional<std::string>& effort) {
  ReasoningRequest r;
  if (enable_thinking) r.thinking = ParseBool(*enable_thinking);
  if (effort) r.effort = ToLower(*effort);
  if (r.thinking == false) r.effort.clear();
  return r;
}

}  // namespace gufo_backend
