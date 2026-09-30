// Load-time options for the gufo backend (ModelOptions.Options, "key:value" strings).
// Header-only and free of gRPC and engine types so it can be unit-tested alone.
#pragma once

#include <cerrno>
#include <cstdint>
#include <cstdlib>
#include <optional>
#include <string>
#include <vector>

namespace gufo_backend {

struct Options {
  std::string speculative;  // "", "off", "dflash2", "mtp", "dspark"
  std::string draft_model;
  int draft_tokens = 7;
  int min_draft_tokens = 1;
  std::size_t sessions = 1;
  std::size_t max_pending = 16;
  std::size_t max_pending_per_client = 4;
  std::uint64_t request_timeout_ms = 0;
  std::size_t max_output_bytes = 1048576;
  // gufo fails a request with kOutputBackpressure when the consumer lags past
  // these. Its defaults (64 KiB, 256 KiB) are sized for an HTTP socket; a gRPC
  // client reading slowly would hit them, so the backend raises them.
  std::size_t max_buffered_output_bytes = 8388608;
  std::size_t max_buffered_output_bytes_total = 33554432;
  std::optional<bool> think;
  std::string reasoning_effort;  // "", minimal, low, medium, high, xhigh, max
  std::optional<bool> preserve_thinking;
  std::string cache_disk;
  std::uint64_t cache_disk_bytes = 0;
};

struct ParseResult {
  Options options;
  std::string error;
  bool ok() const { return error.empty(); }
};

namespace detail {

inline bool ParseUnsigned(const std::string& text, unsigned long long* out) {
  // strtoull skips leading whitespace and accepts a sign, so " -1" would wrap
  // to 2^64-1; only a bare run of ASCII digits is a valid value.
  if (text.empty()) return false;
  for (const char c : text) {
    if (c < '0' || c > '9') return false;
  }
  errno = 0;
  char* end = nullptr;
  const unsigned long long value = std::strtoull(text.c_str(), &end, 10);
  if (errno != 0 || end == text.c_str() || *end != '\0') return false;
  *out = value;
  return true;
}

inline bool ParseBool(const std::string& text, bool* out) {
  if (text == "true" || text == "1" || text == "yes" || text == "on") { *out = true; return true; }
  if (text == "false" || text == "0" || text == "no" || text == "off") { *out = false; return true; }
  return false;
}

}  // namespace detail

inline ParseResult ParseOptions(const std::vector<std::string>& raw) {
  ParseResult result;
  Options& o = result.options;
  bool per_client_set = false;
  auto fail = [&](const std::string& message) {
    result.error = "gufo: " + message;
    return result;
  };
  for (const std::string& entry : raw) {
    const auto colon = entry.find(':');
    if (colon == std::string::npos) return fail("option '" + entry + "' must be key:value");
    const std::string key = entry.substr(0, colon);
    const std::string value = entry.substr(colon + 1);
    unsigned long long number = 0;
    bool flag = false;

    if (key == "speculative") {
      if (value != "off" && value != "dflash2" && value != "mtp" && value != "dspark")
        return fail("speculative must be one of off, dflash2, mtp, dspark");
      o.speculative = value;
    } else if (key == "draft_model") {
      o.draft_model = value;
    } else if (key == "draft_tokens" || key == "min_draft_tokens") {
      if (!detail::ParseUnsigned(value, &number) || number == 0 || number > 64)
        return fail(key + " must be an integer from 1 to 64");
      (key == "draft_tokens" ? o.draft_tokens : o.min_draft_tokens) = static_cast<int>(number);
    } else if (key == "sessions") {
      if (!detail::ParseUnsigned(value, &number) || number == 0 || number > 64)
        return fail("sessions must be an integer from 1 to 64");
      o.sessions = number;
    } else if (key == "max_pending") {
      if (!detail::ParseUnsigned(value, &number) || number == 0) return fail("max_pending must be a positive integer");
      o.max_pending = number;
    } else if (key == "max_pending_per_client") {
      if (!detail::ParseUnsigned(value, &number) || number == 0) return fail("max_pending_per_client must be a positive integer");
      o.max_pending_per_client = number;
      per_client_set = true;
    } else if (key == "request_timeout_ms") {
      // gufo keeps this as signed std::chrono::milliseconds and throws on a
      // negative count, so a huge value must be refused here with a clear name.
      if (!detail::ParseUnsigned(value, &number) || number > 86400000ULL)
        return fail("request_timeout_ms must be an integer from 0 to 86400000 (24 h)");
      o.request_timeout_ms = number;
    } else if (key == "max_output_bytes") {
      if (!detail::ParseUnsigned(value, &number) || number == 0) return fail("max_output_bytes must be a positive integer");
      o.max_output_bytes = number;
    } else if (key == "max_buffered_output_bytes") {
      if (!detail::ParseUnsigned(value, &number) || number == 0) return fail("max_buffered_output_bytes must be a positive integer");
      o.max_buffered_output_bytes = number;
    } else if (key == "max_buffered_output_bytes_total") {
      if (!detail::ParseUnsigned(value, &number) || number == 0) return fail("max_buffered_output_bytes_total must be a positive integer");
      o.max_buffered_output_bytes_total = number;
    } else if (key == "think") {
      if (!detail::ParseBool(value, &flag)) return fail("think must be true or false");
      o.think = flag;
    } else if (key == "preserve_thinking") {
      if (!detail::ParseBool(value, &flag)) return fail("preserve_thinking must be true or false");
      o.preserve_thinking = flag;
    } else if (key == "reasoning_effort") {
      if (value != "minimal" && value != "low" && value != "medium" && value != "high" && value != "xhigh" && value != "max")
        return fail("reasoning_effort must be one of minimal, low, medium, high, xhigh, max");
      o.reasoning_effort = value;
    } else if (key == "cache_disk") {
      o.cache_disk = value;
    } else if (key == "cache_disk_bytes") {
      if (!detail::ParseUnsigned(value, &number)) return fail("cache_disk_bytes must be a non-negative integer");
      o.cache_disk_bytes = number;
    } else {
      return fail("unknown option '" + key + "'");
    }
  }
  if (o.min_draft_tokens > o.draft_tokens) return fail("min_draft_tokens must not exceed draft_tokens");
  // gufo rejects per-client > total with an opaque "invalid text scheduler
  // limits"; the default follows a small max_pending down, an explicit value
  // gets a named error instead.
  if (o.max_pending_per_client > o.max_pending) {
    if (per_client_set) return fail("max_pending_per_client must not exceed max_pending");
    o.max_pending_per_client = o.max_pending;
  }
  // Every gufo speculative mode reads its drafter from a separate GGUF (the MTP
  // head included), so none of them can start without draft_model.
  if (!o.speculative.empty() && o.speculative != "off" && o.draft_model.empty())
    return fail("speculative:" + o.speculative + " needs draft_model");
  return result;
}

}  // namespace gufo_backend
