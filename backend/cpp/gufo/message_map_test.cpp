#include <iostream>
#include "message_map.h"

static int failures = 0;
#define CHECK(cond) do { if (!(cond)) { ++failures; std::cerr << __FILE__ << ":" << __LINE__ << " FAILED: " #cond "\n"; } } while (0)

using namespace gufo_backend;

// Malformed client JSON must come back as false plus a message, never as an
// exception escaping into the gRPC handler.
static bool CallsRejected(const std::string& json) {
  std::vector<PlainToolCall> calls; std::string err;
  try {
    return !ParseToolCallsJson(json, &calls, &err) && !err.empty() && calls.empty();
  } catch (const std::exception& e) {
    std::cerr << "  ParseToolCallsJson threw: " << e.what() << "\n";
    return false;
  }
}

static bool ToolsRejected(const std::string& json) {
  std::vector<PlainTool> tools; std::string err;
  try {
    return !ParseToolsJson(json, &tools, &err) && !err.empty() && tools.empty();
  } catch (const std::exception& e) {
    std::cerr << "  ParseToolsJson threw: " << e.what() << "\n";
    return false;
  }
}

static std::string Nested(std::size_t depth) {
  return std::string(depth, '[') + std::string(depth, ']');
}

int main() {
  {
    std::vector<PlainToolCall> calls; std::string err;
    const std::string json =
        R"([{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Rome\",\"days\":3,\"metric\":true}"}}])";
    CHECK(ParseToolCallsJson(json, &calls, &err));
    CHECK(calls.size() == 1);
    CHECK(calls[0].id == "call_1" && calls[0].name == "get_weather");
    CHECK(calls[0].args.size() == 3);
    CHECK(calls[0].args[0].name == "city" && calls[0].args[0].value == "Rome" && calls[0].args[0].is_string);
    CHECK(calls[0].args[1].name == "days" && calls[0].args[1].value == "3" && !calls[0].args[1].is_string);
    CHECK(calls[0].args[2].value == "true" && !calls[0].args[2].is_string);
  }
  {
    std::vector<PlainToolCall> calls; std::string err;
    CHECK(ParseToolCallsJson("", &calls, &err) && calls.empty());          // empty means none
    CHECK(!ParseToolCallsJson("not json", &calls, &err) && !err.empty());
    CHECK(!ParseToolCallsJson(R"([{"function":{"name":"f","arguments":"{bad"}}])", &calls, &err));
    CHECK(!ParseToolCallsJson(R"([{"function":{"name":"bad name","arguments":"{}"}}])", &calls, &err));
  }
  {
    std::vector<PlainTool> tools; std::string err;
    const std::string json =
        R"([{"type":"function","function":{"name":"get_weather","description":"Weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}])";
    CHECK(ParseToolsJson(json, &tools, &err));
    CHECK(tools.size() == 1 && tools[0].name == "get_weather" && tools[0].description == "Weather");
    CHECK(tools[0].parameters_json.find("\"city\"") != std::string::npos);
    CHECK(tools[0].definition_json.find("\"type\":\"function\"") != std::string::npos);
  }
  {
    std::vector<PlainTool> tools; std::string err;
    CHECK(ParseToolsJson(R"([{"name":"f","parameters":{}}])", &tools, &err) && tools.size() == 1);  // flat form
    CHECK(tools[0].parameters_json == "{}");
    CHECK(ParseToolsJson("", &tools, &err) && tools.empty());
    CHECK(!ParseToolsJson(R"([{"function":{"description":"no name"}}])", &tools, &err));
    CHECK(!ParseToolsJson(R"({"not":"an array"})", &tools, &err));
  }
  {
    CHECK(ValidToolName("get_weather"));
    CHECK(!ValidToolName(""));
    CHECK(!ValidToolName(std::string(65, 'a')));
    CHECK(ValidToolName(std::string(64, 'a')));
    CHECK(!ValidToolName("a b"));
    CHECK(!ValidToolName("a<b"));
    CHECK(!ValidToolName("a\"b"));
    CHECK(!ValidToolName("a\\b"));
    CHECK(!ValidToolName("caf\xc3\xa9"));  // non-ASCII
    CHECK(!ValidToolName("a>b"));
    CHECK(!ValidToolName("a\tb"));
    CHECK(!ValidToolName(std::string("a\0b", 3)));
    CHECK(!ValidToolName("a\x7f"));
    CHECK(ValidToolName("ns.tool-name:v2/x"));
  }

  // Non-string scalars and nested values keep their JSON text; keys come out
  // sorted because nlohmann::json objects are std::map backed.
  {
    std::vector<PlainToolCall> calls; std::string err;
    const std::string json =
        R"([{"function":{"name":"f","arguments":"{\"z\":3,\"y\":true,\"x\":null,\"w\":1.5,\"v\":{\"b\":1,\"a\":[1,\"s\"]},\"u\":[],\"t\":\"\"}"}}])";
    CHECK(ParseToolCallsJson(json, &calls, &err));
    CHECK(calls.size() == 1 && calls[0].args.size() == 7);
    if (calls.size() == 1 && calls[0].args.size() == 7) {
      const auto& a = calls[0].args;
      CHECK(a[0].name == "t" && a[0].value.empty() && a[0].is_string);
      CHECK(a[1].name == "u" && a[1].value == "[]" && !a[1].is_string);
      CHECK(a[2].name == "v" && a[2].value == R"({"a":[1,"s"],"b":1})" && !a[2].is_string);
      CHECK(a[3].name == "w" && a[3].value == "1.5" && !a[3].is_string);
      CHECK(a[4].name == "x" && a[4].value == "null" && !a[4].is_string);
      CHECK(a[5].name == "y" && a[5].value == "true" && !a[5].is_string);
      CHECK(a[6].name == "z" && a[6].value == "3" && !a[6].is_string);
    }
  }
  // Already-decoded object arguments, missing arguments, and the no-argument
  // spellings ("" and null) that clients send for zero-parameter tools.
  {
    std::vector<PlainToolCall> calls; std::string err;
    CHECK(ParseToolCallsJson(R"([{"function":{"name":"f","arguments":{"a":"b"}}}])", &calls, &err));
    CHECK(calls.size() == 1 && calls[0].args.size() == 1 && calls[0].args[0].value == "b");
    CHECK(ParseToolCallsJson(R"([{"function":{"name":"f"}}])", &calls, &err) && calls.size() == 1 && calls[0].args.empty());
    CHECK(ParseToolCallsJson(R"([{"function":{"name":"f","arguments":""}}])", &calls, &err) && calls.size() == 1 && calls[0].args.empty());
    CHECK(ParseToolCallsJson(R"([{"function":{"name":"f","arguments":null}}])", &calls, &err) && calls.size() == 1 && calls[0].args.empty());
    CHECK(ParseToolCallsJson(R"([{"id":null,"function":{"name":"f"}}])", &calls, &err) && calls.size() == 1 && calls[0].id.empty());
    CHECK(ParseToolCallsJson("[]", &calls, &err) && calls.empty());
    CHECK(ParseToolCallsJson(R"([{"name":"flat","arguments":"{}"}])", &calls, &err) && calls.size() == 1 && calls[0].name == "flat");
  }
  // Every shape that makes nlohmann throw: non-object items, wrong-typed
  // fields, non-object arguments, and nesting deep enough to exhaust the stack.
  {
    CHECK(CallsRejected("[1]"));
    CHECK(CallsRejected("[null]"));
    CHECK(CallsRejected(R"(["f"])"));
    CHECK(CallsRejected("[[]]"));
    CHECK(CallsRejected(R"([{"function":"f"}])"));
    CHECK(CallsRejected(R"([{"function":null}])"));
    CHECK(CallsRejected(R"([{"function":[1]}])"));
    CHECK(CallsRejected(R"([{"id":5,"function":{"name":"f"}}])"));
    CHECK(CallsRejected(R"([{"function":{"name":null}}])"));
    CHECK(CallsRejected(R"([{"function":{"name":7}}])"));
    CHECK(CallsRejected(R"([{"function":{"name":"f","arguments":[1,2]}}])"));
    CHECK(CallsRejected(R"([{"function":{"name":"f","arguments":"[1,2]"}}])"));
    CHECK(CallsRejected(R"([{"function":{"name":"f","arguments":"3"}}])"));
    CHECK(CallsRejected(R"([{"function":{"name":"f","arguments":5}}])"));
    CHECK(CallsRejected(R"([{"function":{"name":"f","arguments":"null"}}])"));
    CHECK(CallsRejected(R"({"function":{"name":"f"}})"));
    CHECK(CallsRejected("null"));
    CHECK(CallsRejected(R"([{"function":{"name":"ok"}},{"function":{"name":5}}])"));  // no partial output
    CHECK(CallsRejected("[" + Nested(200000) + "]"));
    {
      std::vector<PlainToolCall> calls; std::string err;
      CHECK(!ParseToolCallsJson("[" + Nested(200000) + "]", &calls, &err) && err.find("too deeply") != std::string::npos);
    }
    CHECK(CallsRejected(R"([{"function":{"name":"f","arguments":")" + std::string(200000, '[') + R"("}}])"));
    CHECK(CallsRejected(R"([{"function":{"name":"f","arguments":"{\"a\":)" + Nested(200000) + R"(}"}}])"));
  }
  {
    CHECK(ToolsRejected("[1]"));
    CHECK(ToolsRejected("[null]"));
    CHECK(ToolsRejected(R"(["f"])"));
    CHECK(ToolsRejected(R"([{"function":"f"}])"));
    CHECK(ToolsRejected(R"([{"function":null}])"));
    CHECK(ToolsRejected(R"([{"function":{"name":null}}])"));
    CHECK(ToolsRejected(R"([{"function":{"name":5}}])"));
    CHECK(ToolsRejected(R"([{"function":{"name":"f","description":7}}])"));
    CHECK(ToolsRejected(R"([{"function":{"name":"f","parameters":5}}])"));
    CHECK(ToolsRejected(R"([{"function":{"name":"f","parameters":"{}"}}])"));
    CHECK(ToolsRejected(R"([{"function":{"name":"f","parameters":[]}}])"));
    CHECK(ToolsRejected("null"));
    CHECK(ToolsRejected(R"([{"function":{"name":"f","parameters":{"a":)" + Nested(200000) + "}}}]"));
  }
  // Null description and parameters mean "not given".
  {
    std::vector<PlainTool> tools; std::string err;
    CHECK(ParseToolsJson(R"([{"function":{"name":"f","description":null,"parameters":null}}])", &tools, &err));
    CHECK(tools.size() == 1 && tools[0].description.empty() && tools[0].parameters_json == "{}");
    CHECK(ParseToolsJson(R"([{"name":"g"}])", &tools, &err) && tools.size() == 1 && tools[0].parameters_json == "{}");
    CHECK(ParseToolsJson("[]", &tools, &err) && tools.empty());
    // Reasonable schema depth still parses.
    std::string schema = "{}";
    for (int i = 0; i < 32; ++i) schema = R"({"type":"object","properties":{"p":)" + schema + "}}";
    CHECK(ParseToolsJson(R"([{"name":"deep","parameters":)" + schema + "}]", &tools, &err) && tools.size() == 1);
  }
  CHECK(kMaxImages == 16);
  return failures == 0 ? 0 : 1;
}
