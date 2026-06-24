// Unit tests for the shared message-reconstruction helpers (message_content.h).
//
// Build & run standalone (nlohmann/json single header on the include path):
//   g++ -std=c++17 -I<dir-with-nlohmann> message_content_test.cpp -o t && ./t
// or via CMake: -DLLAMA_GRPC_BUILD_TESTS=ON then ctest.
//
// Regression coverage for:
//   #10524 - a user/system prompt that is itself a JSON-array string must stay
//            plain text, never be reinterpreted as OpenAI structured parts.
//   #7324  - assistant/tool null content -> "" (templates slice content[:N]);
//            assistant+tool_calls+empty content -> " " (not "", which becomes null).
//   #7528  - tool message array content must reach the template as a string.
//   multimodal - last user message text + media -> typed-part array, media kept.

#include <cassert>
#include <iostream>
#include <string>

#include "message_content.h"

using nlohmann::ordered_json;
using llama_grpc::normalize_message_content;
using llama_grpc::normalize_template_message;
using llama_grpc::build_reconstructed_message;
using llama_grpc::ReconstructedMessageInput;

static int failures = 0;

static void check(bool ok, const std::string& name, const std::string& detail = "") {
    if (!ok) {
        std::cerr << "FAIL " << name << (detail.empty() ? "" : ": " + detail) << "\n";
        failures++;
    }
}

// ---- normalize_message_content -------------------------------------------

static void expect_norm_string(const char* name, const std::string& role,
                               const std::string& content, const std::string& want) {
    auto got = normalize_message_content(role, content);
    if (!got.is_string()) {
        check(false, name, "expected a JSON string, got " +
                               std::string(got.is_array() ? "array" : got.is_object() ? "object" : "other") +
                               " (" + got.dump() + ")");
        return;
    }
    check(got.get<std::string>() == want, name, "expected \"" + want + "\", got \"" + got.get<std::string>() + "\"");
}

static void test_normalize() {
    const std::string ingredients = R"(["1/4 cup brown sugar, packed","1 pound ground beef"])";

    // #10524 - JSON-array text must stay a string. Role-INDEPENDENT array defense.
    for (const char* role : {"user", "system", "developer", "function", "assistant", "tool"}) {
        expect_norm_string((std::string("json_array_stays_text:") + role).c_str(), role, ingredients, ingredients);
    }

    // #10524 - user/system/developer JSON-object text stays verbatim (NOT re-dumped).
    expect_norm_string("user_json_object_verbatim", "user", R"({"a":1})", R"({"a":1})");
    expect_norm_string("system_json_object_verbatim", "system", R"({"a":1})", R"({"a":1})");
    expect_norm_string("developer_json_object_verbatim", "developer", R"({"a":1})", R"({"a":1})");

    // Plain text unchanged for all roles.
    expect_norm_string("user_plain_text", "user", "hello world", "hello world");
    expect_norm_string("assistant_non_json_text_kept", "assistant", "hi [unclosed", "hi [unclosed");

    // #7324 boundary - user/system/developer literal "null" preserved (never parsed).
    expect_norm_string("user_literal_null_stays", "user", "null", "null");
    expect_norm_string("system_literal_null_stays", "system", "null", "null");
    expect_norm_string("developer_literal_null_stays", "developer", "null", "null");

    // #7324 - assistant/tool literal null collapses to empty string.
    expect_norm_string("assistant_null_to_empty", "assistant", "null", "");
    expect_norm_string("tool_null_to_empty", "tool", "null", "");

    // #7324/#7528 - assistant/tool object bookkeeping stringified (stays a string).
    check(normalize_message_content("assistant", R"({"tool":"x"})").is_string(), "assistant_object_stringified");
    check(normalize_message_content("tool", R"({"error":"boom"})").is_string(), "tool_object_stringified");

    // #10524-family - a bare scalar that parses as a JSON number stays the string.
    expect_norm_string("assistant_scalar_number_stays_string", "assistant", "42", "42");

    // baseline - empty content stays empty.
    expect_norm_string("user_empty_stays_empty", "user", "", "");
}

// ---- normalize_template_message (BEFORE TEMPLATE sanitizer) ---------------

static void test_template_sanitizer() {
    // #7528 - a tool message with an ACTUAL array becomes a string.
    {
        ordered_json msg = {{"role", "tool"}, {"content", ordered_json::array({{{"type", "text"}, {"text", "r"}}})}};
        normalize_template_message(msg);
        check(msg["content"].is_string(), "before_template_tool_array_to_string", "got " + msg["content"].dump());
    }
    // #7324 - null content -> "" for any role.
    {
        ordered_json msg = {{"role", "assistant"}, {"content", nullptr}};
        normalize_template_message(msg);
        check(msg["content"].is_string() && msg["content"] == "", "before_template_null_to_empty");
    }
    // object content -> dumped string (would otherwise throw at the template).
    {
        ordered_json msg = {{"role", "assistant"}, {"content", {{"x", 1}}}};
        normalize_template_message(msg);
        check(msg["content"].is_string(), "before_template_object_to_string", "got " + msg["content"].dump());
    }
    // missing content field -> "".
    {
        ordered_json msg = {{"role", "user"}};
        normalize_template_message(msg);
        check(msg.contains("content") && msg["content"] == "", "before_template_missing_to_empty");
    }
    // multimodal: a well-typed user array must be left UNTOUCHED (role!=tool).
    {
        ordered_json parts = ordered_json::array();
        parts.push_back({{"type", "text"}, {"text", "x"}});
        ordered_json img; img["type"] = "image_url"; img["image_url"] = {{"url", "data:..."}};
        parts.push_back(img);
        ordered_json msg = {{"role", "user"}, {"content", parts}};
        normalize_template_message(msg);
        check(msg["content"].is_array() && msg["content"].size() == 2, "before_template_user_typed_array_preserved",
              "got " + msg["content"].dump());
    }
    // a plain string is left untouched.
    {
        ordered_json msg = {{"role", "user"}, {"content", "hello"}};
        normalize_template_message(msg);
        check(msg["content"] == "hello", "before_template_string_untouched");
    }
}

// ---- build_reconstructed_message ----------------------------------------

static void test_reconstruction() {
    const std::string ingredients = R"(["1/4 cup brown sugar","1 pound ground beef"])";

    // #10524 end-state - user JSON-array text, no media -> string content.
    {
        ReconstructedMessageInput in;
        in.role = "user"; in.content = ingredients;
        auto m = build_reconstructed_message(in);
        check(m["content"].is_string() && m["content"] == ingredients, "recon_user_json_array_string",
              "got " + m["content"].dump());
    }
    // multimodal - user text + one image on last user msg -> typed array, image kept.
    {
        ReconstructedMessageInput in;
        in.role = "user"; in.content = ingredients; in.is_last_user_msg = true;
        in.images.push_back("BASE64IMG");
        auto m = build_reconstructed_message(in);
        check(m["content"].is_array() && m["content"].size() == 2, "recon_multimodal_text_plus_image",
              "got " + m["content"].dump());
        check(m["content"][0]["type"] == "text" && m["content"][0]["text"] == ingredients, "recon_multimodal_text_first");
        check(m["content"][1]["type"] == "image_url", "recon_multimodal_image_kept");
    }
    // multimodal media-only - empty text + image on last user msg.
    {
        ReconstructedMessageInput in;
        in.role = "user"; in.content = ""; in.is_last_user_msg = true;
        in.images.push_back("BASE64IMG");
        auto m = build_reconstructed_message(in);
        check(m["content"].is_array() && m["content"].size() == 1 && m["content"][0]["type"] == "image_url",
              "recon_media_only", "got " + m["content"].dump());
    }
    // #7528 - tool array-string content stays a string.
    {
        ReconstructedMessageInput in;
        in.role = "tool"; in.content = R"(["a","b"])"; in.tool_call_id = "call_1";
        auto m = build_reconstructed_message(in);
        check(m["content"].is_string() && m["content"] == R"(["a","b"])", "recon_tool_array_string",
              "got " + m["content"].dump());
        check(m["tool_call_id"] == "call_1", "recon_tool_call_id_set");
    }
    // tool empty content -> "".
    {
        ReconstructedMessageInput in;
        in.role = "tool"; in.content = "";
        auto m = build_reconstructed_message(in);
        check(m["content"].is_string() && m["content"] == "", "recon_tool_empty_to_string");
    }
    // #7324 - assistant + tool_calls + empty content -> " " (single space, not "").
    {
        ReconstructedMessageInput in;
        in.role = "assistant"; in.content = "";
        in.tool_calls = R"([{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}])";
        auto m = build_reconstructed_message(in);
        check(m["content"].is_string() && m["content"] == " ", "recon_toolcalls_empty_content_space",
              "got " + m["content"].dump());
        check(m["tool_calls"].is_array() && m["tool_calls"].size() == 1, "recon_toolcalls_parsed");
    }
    // assistant + tool_calls + real content keeps the content.
    {
        ReconstructedMessageInput in;
        in.role = "assistant"; in.content = "I'll call f";
        in.tool_calls = R"([{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}])";
        auto m = build_reconstructed_message(in);
        check(m["content"] == "I'll call f", "recon_toolcalls_with_content_kept");
    }
    // assistant null content -> "".
    {
        ReconstructedMessageInput in;
        in.role = "assistant"; in.content = "null";
        auto m = build_reconstructed_message(in);
        check(m["content"] == "", "recon_assistant_null_to_empty");
    }
    // malformed tool_calls JSON must not throw; content preserved.
    {
        ReconstructedMessageInput in;
        in.role = "assistant"; in.content = "hi"; in.tool_calls = "{not json";
        auto m = build_reconstructed_message(in);
        check(m["content"] == "hi" && !m.contains("tool_calls"), "recon_malformed_toolcalls_safe");
    }
    // optional fields: name + reasoning carried through.
    {
        ReconstructedMessageInput in;
        in.role = "tool"; in.content = "result"; in.name = "get_weather"; in.reasoning_content = "thinking";
        auto m = build_reconstructed_message(in);
        check(m["name"] == "get_weather" && m["reasoning_content"] == "thinking", "recon_optional_fields");
    }
}

int main() {
    test_normalize();
    test_template_sanitizer();
    test_reconstruction();

    if (failures == 0) {
        std::cout << "OK: all message_content tests passed\n";
        return 0;
    }
    std::cerr << failures << " test(s) failed\n";
    return 1;
}
