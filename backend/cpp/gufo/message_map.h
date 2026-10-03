// Proto-independent chat structures and the JSON conversions the gRPC layer
// needs. grpc-server.cpp turns backend::Message into PlainMessage, and the
// engine wrapper turns these into gufo's own types.
//
// The JSON comes straight from API clients, so every parse path reports
// failure through the return value: nlohmann's value() and get<>() throw on a
// type mismatch, and an exception escaping into a gRPC handler kills the
// backend process.
#pragma once

#include <cstddef>
#include <exception>
#include <string>
#include <vector>

#include <nlohmann/json.hpp>

namespace gufo_backend {

constexpr int kMaxImages = 16;

// nlohmann parses iteratively but dump() recurses, so a hostile payload of a
// few hundred KB of '[' overflows the stack. Real tool schemas stay far below
// this.
constexpr std::size_t kMaxJsonDepth = 128;

struct ToolArg {
  std::string name;
  std::string value;
  bool is_string = false;
};

struct PlainToolCall {
  std::string id;
  std::string name;
  std::vector<ToolArg> args;
};

struct PlainMessage {
  std::string role;
  std::string content;
  std::string name;
  std::string tool_call_id;
  std::string reasoning;
  std::vector<PlainToolCall> tool_calls;
  std::vector<std::string> images_b64;
};

struct PlainTool {
  std::string name;
  std::string description;
  std::string parameters_json = "{}";
  std::string definition_json;
};

// gufo rejects tool names it cannot render safely into a prompt.
inline bool ValidToolName(const std::string& name) {
  if (name.empty() || name.size() > 64) return false;
  for (const unsigned char c : name) {
    if (c < 0x21 || c > 0x7e) return false;  // printable ASCII, no space
    if (c == '<' || c == '>' || c == '"' || c == '\\') return false;
  }
  return true;
}

namespace detail {

// Scans raw JSON text so over-deep input is refused before it is parsed.
// Brackets inside string literals do not count.
inline bool JsonDepthWithin(const std::string& text, std::size_t max_depth) {
  std::size_t depth = 0;
  bool in_string = false, escaped = false;
  for (const char c : text) {
    if (in_string) {
      if (escaped) escaped = false;
      else if (c == '\\') escaped = true;
      else if (c == '"') in_string = false;
      continue;
    }
    if (c == '"') {
      in_string = true;
    } else if (c == '[' || c == '{') {
      if (++depth > max_depth) return false;
    } else if ((c == ']' || c == '}') && depth > 0) {
      --depth;
    }
  }
  return true;
}

// Returns nullptr on success, otherwise why the text was refused.
inline const char* ParseBounded(const std::string& text, nlohmann::json* out) {
  if (!JsonDepthWithin(text, kMaxJsonDepth)) return "is nested too deeply";
  *out = nlohmann::json::parse(text, nullptr, /*allow_exceptions=*/false);
  return out->is_discarded() ? "is not valid JSON" : nullptr;
}

// Missing and null both mean "not given"; any other non-string is an error.
inline bool OptionalString(const nlohmann::json& obj, const char* key, std::string* out) {
  out->clear();
  const auto it = obj.find(key);
  if (it == obj.end() || it->is_null()) return true;
  if (!it->is_string()) return false;
  *out = it->get<std::string>();
  return true;
}

// Both the OpenAI {"type":"function","function":{...}} wrapper and the flat
// form are accepted; returns nullptr when the item has neither shape.
inline const nlohmann::json* FunctionObject(const nlohmann::json& item) {
  if (!item.is_object()) return nullptr;
  const auto it = item.find("function");
  if (it == item.end()) return &item;
  return it->is_object() ? &*it : nullptr;
}

inline bool Fail(std::string* error, const char* message) {
  *error = message;
  return false;
}

inline bool Fail(std::string* error, const char* what, const char* why) {
  *error = std::string(what) + " " + why;
  return false;
}

}  // namespace detail

// Arguments come out in key order, not the client's order: nlohmann::json
// objects are std::map backed. That order is deterministic, which is what
// prompt rendering needs.
inline bool ParseToolCallsJson(const std::string& json, std::vector<PlainToolCall>* out, std::string* error) {
  out->clear();
  if (json.empty()) return true;
  try {
    nlohmann::json doc;
    if (const char* why = detail::ParseBounded(json, &doc)) return detail::Fail(error, "tool_calls", why);
    if (!doc.is_array()) return detail::Fail(error, "tool_calls must be a JSON array");
    std::vector<PlainToolCall> calls;
    for (const auto& item : doc) {
      const nlohmann::json* fn = detail::FunctionObject(item);
      if (fn == nullptr) return detail::Fail(error, "each tool call must be a JSON object with a function object");
      PlainToolCall call;
      if (!detail::OptionalString(item, "id", &call.id)) return detail::Fail(error, "tool call id must be a string");
      const auto name = fn->find("name");
      if (name == fn->end() || !name->is_string() || !ValidToolName(name->get<std::string>()))
        return detail::Fail(error, "tool call has an invalid name");
      call.name = name->get<std::string>();
      const auto raw = fn->find("arguments");
      // Zero-parameter calls arrive as a missing key, null, or "".
      if (raw != fn->end() && !raw->is_null() && !(raw->is_string() && raw->get_ref<const std::string&>().empty())) {
        nlohmann::json args;
        if (raw->is_string()) {
          if (const char* why = detail::ParseBounded(raw->get<std::string>(), &args))
            return detail::Fail(error, "tool call arguments", why);
        } else {
          args = *raw;
        }
        if (!args.is_object()) return detail::Fail(error, "tool call arguments must be a JSON object");
        for (auto it = args.begin(); it != args.end(); ++it) {
          ToolArg arg;
          arg.name = it.key();
          arg.is_string = it.value().is_string();
          arg.value = arg.is_string ? it.value().get<std::string>() : it.value().dump();
          call.args.push_back(std::move(arg));
        }
      }
      calls.push_back(std::move(call));
    }
    *out = std::move(calls);
    return true;
  } catch (const std::exception& e) {
    // Backstop for anything the checks above miss (e.g. bad_alloc).
    *error = std::string("tool_calls could not be read: ") + e.what();
    return false;
  }
}

// Bounds the text a single argument value may hold before it is parsed. The
// parse is iterative, so this caps memory and time, not stack; the depth check
// in ParseBounded is what keeps dump() from recursing off the stack.
constexpr std::size_t kMaxArgumentValueBytes = 1 << 20;

// Renders a model's tool-call arguments as the JSON object text the OpenAI
// wire format carries. The values are model output, so a value that is not
// valid, bounded JSON is sent as a string rather than trusted, and invalid
// UTF-8 is replaced instead of throwing. Keys come out in sorted order.
inline std::string ArgumentsToJson(const std::vector<ToolArg>& args) {
  try {
    nlohmann::json obj = nlohmann::json::object();
    for (const ToolArg& a : args) {
      nlohmann::json value;
      if (a.is_string || a.value.size() > kMaxArgumentValueBytes || detail::ParseBounded(a.value, &value) != nullptr)
        value = a.value;
      obj[a.name] = std::move(value);
    }
    return obj.dump(-1, ' ', false, nlohmann::json::error_handler_t::replace);
  } catch (const std::exception&) {
    // Only allocation failure is left; an empty object keeps the reply well formed.
    return "{}";
  }
}

inline bool ParseToolsJson(const std::string& json, std::vector<PlainTool>* out, std::string* error) {
  out->clear();
  if (json.empty()) return true;
  try {
    nlohmann::json doc;
    if (const char* why = detail::ParseBounded(json, &doc)) return detail::Fail(error, "tools", why);
    if (!doc.is_array()) return detail::Fail(error, "tools must be a JSON array");
    std::vector<PlainTool> tools;
    for (const auto& item : doc) {
      const nlohmann::json* fn = detail::FunctionObject(item);
      if (fn == nullptr) return detail::Fail(error, "each tool must be a JSON object with a function object");
      PlainTool tool;
      const auto name = fn->find("name");
      if (name == fn->end() || !name->is_string() || !ValidToolName(name->get<std::string>()))
        return detail::Fail(error, "tool has a missing or invalid name");
      tool.name = name->get<std::string>();
      if (!detail::OptionalString(*fn, "description", &tool.description))
        return detail::Fail(error, "tool description must be a string");
      nlohmann::json params = nlohmann::json::object();
      const auto p = fn->find("parameters");
      if (p != fn->end() && !p->is_null()) {
        if (!p->is_object()) return detail::Fail(error, "tool parameters must be a JSON object");
        params = *p;
      }
      tool.parameters_json = params.dump();
      nlohmann::json definition = {{"type", "function"},
                                   {"function", {{"name", tool.name}, {"description", tool.description}, {"parameters", params}}}};
      tool.definition_json = definition.dump();
      tools.push_back(std::move(tool));
    }
    *out = std::move(tools);
    return true;
  } catch (const std::exception& e) {
    *error = std::string("tools could not be read: ") + e.what();
    return false;
  }
}

}  // namespace gufo_backend
