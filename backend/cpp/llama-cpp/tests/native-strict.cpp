// SPDX-License-Identifier: MIT
#include "chat.h"

#include <iostream>
#include <type_traits>
#include <utility>

// Keep the harness runnable against the unpatched pin: missing transport
// support must produce a runtime regression failure, not a compiler error.
template <typename T, typename = void> struct strict_flag {
    static bool get(const T &) { return false; }
};
template <typename T> struct strict_flag<T, std::void_t<decltype(std::declval<T>().strict)>> {
    static bool get(const T & tool) { return tool.strict; }
};

int main() {
    int failures = 0;
    auto check = [&](bool ok, const char * label) {
        std::cout << (ok ? "PASS " : "FAIL ") << label << '\n';
        failures += !ok;
    };
    using json = common_json;
    const auto parameters = json::parse(R"({"type":"object","properties":{"prompt":{"type":"string"}},"required":["prompt"],"additionalProperties":false})");
    for (int mode = 0; mode != 3; ++mode) {
        const bool expected = mode == 0;
        json function = {{"name", "sound"}, {"description", "Sound tool"}, {"parameters", parameters}};
        if (mode != 2) {
            function["strict"] = expected;
        }
        const json incoming = json::array({{{"type", "function"}, {"function", function}}});
        const auto native = common_chat_tools_parse_oaicompat(incoming);
        check(native.size() == 1, "incoming tool count");
        if (native.size() != 1) { continue; }
        std::cout << "CASE " << (mode == 0 ? "true" : mode == 1 ? "false" : "absent") << '\n';
        check(strict_flag<common_chat_tool>::get(native[0]) == expected, "incoming -> native strict");
        const auto outgoing = common_chat_tools_to_json_oaicompat(native);
        const auto & out = outgoing.at(0).at("function");
        check(out.value("strict", false) == expected, "native -> outgoing strict");
        check(expected ? out.contains("strict") : !out.contains("strict"),
              "only strict tools add a serialized strict field");
        check(out.at("name") == "sound" && out.at("description") == "Sound tool" &&
              out.at("parameters") == parameters, "other tool fields unchanged");
        const auto reparsed = common_chat_tools_parse_oaicompat(outgoing);
        check(strict_flag<common_chat_tool>::get(reparsed.at(0)) == expected, "outgoing -> native strict");
    }
    const common_chat_tool legacy = {"legacy", "Three-field initializer", parameters.dump()};
    check(!strict_flag<common_chat_tool>::get(legacy), "three-field aggregate defaults false");
    const auto legacy_json = common_chat_tools_to_json_oaicompat({legacy});
    check(!legacy_json.at(0).at("function").value("strict", false), "legacy serialization remains non-strict");
    check(common_chat_tools_parse_oaicompat(json()).empty(), "null input remains empty");
    check(common_chat_tools_to_json_oaicompat({}).is_null(), "empty output remains null");
    return failures ? 1 : 0;
}
