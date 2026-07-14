#include "dsml_renderer.h"

// We accept either nlohmann::json (if available) or fall back to a tiny
// hand-rolled parser. The LocalAI tree already has nlohmann/json bundled
// in vendor paths; we use the apt-installed nlohmann-json3-dev (installed
// in Task 11 step 1) when present, otherwise the bundled copy.
#if __has_include(<nlohmann/json.hpp>)
#include <nlohmann/json.hpp>
using json = nlohmann::json;
#else
#error "nlohmann/json.hpp not found; install nlohmann-json3-dev"
#endif

#include <sstream>

namespace ds4cpp {

namespace {

void render_param(std::ostringstream &os, const std::string &name,
                  const json &value) {
    bool is_string = value.is_string();
    os << "<\xef\xbd\x9c" "DSML\xef\xbd\x9c" "parameter name=\"" << name
       << "\" string=\"" << (is_string ? "true" : "false") << "\">";
    if (is_string) {
        os << value.get<std::string>();
    } else {
        os << value.dump();
    }
    os << "</\xef\xbd\x9c" "DSML\xef\xbd\x9c" "parameter>\n";
}

} // namespace

std::string RenderAssistantToolCalls(const std::string &tool_calls_json) {
    if (tool_calls_json.empty()) return "";
    json arr;
    try {
        arr = json::parse(tool_calls_json);
    } catch (const std::exception &) {
        return "";
    }
    if (!arr.is_array() || arr.empty()) return "";

    std::ostringstream os;
    os << "\n\n<\xef\xbd\x9c" "DSML\xef\xbd\x9c" "tool_calls>\n";
    for (const auto &call : arr) {
        // OpenAI shape: { id, type, function: { name, arguments (JSON string) } }
        // Anthropic shape comes through normalized by LocalAI.
        std::string name;
        std::string args_str;
        if (call.contains("function")) {
            const auto &fn = call["function"];
            if (fn.contains("name") && fn["name"].is_string())
                name = fn["name"].get<std::string>();
            if (fn.contains("arguments") && fn["arguments"].is_string())
                args_str = fn["arguments"].get<std::string>();
        }
        os << "<\xef\xbd\x9c" "DSML\xef\xbd\x9c" "invoke name=\"" << name << "\">\n";
        if (!args_str.empty()) {
            json args;
            try {
                args = json::parse(args_str);
            } catch (...) {
                args = json{};
            }
            if (args.is_object()) {
                for (auto it = args.begin(); it != args.end(); ++it) {
                    render_param(os, it.key(), it.value());
                }
            }
        }
        os << "</\xef\xbd\x9c" "DSML\xef\xbd\x9c" "invoke>\n";
    }
    os << "</\xef\xbd\x9c" "DSML\xef\xbd\x9c" "tool_calls>";
    return os.str();
}

std::string RenderToolResult(const std::string &tool_call_id, const std::string &content) {
    std::ostringstream os;
    // ds4_server.c wraps tool results in a "tool_result" DSML tag carrying
    // the tool_call_id. Match that shape.
    os << "<\xef\xbd\x9c" "DSML\xef\xbd\x9c" "tool_result id=\"" << tool_call_id << "\">"
       << content
       << "</\xef\xbd\x9c" "DSML\xef\xbd\x9c" "tool_result>";
    return os.str();
}

std::string RenderToolsManifest(const std::string &tools_json) {
    if (tools_json.empty()) return "";
    json arr;
    try {
        arr = json::parse(tools_json);
    } catch (const std::exception &) {
        return "";
    }
    if (!arr.is_array() || arr.empty()) return "";

    // Extract each OpenAI tool's `function` object, dump as compact JSON, one
    // per line. Mirrors openai_function_schema_from_tool() in ds4_server.c.
    std::ostringstream schemas;
    for (const auto &tool : arr) {
        if (tool.contains("function") && tool["function"].is_object()) {
            schemas << tool["function"].dump() << "\n";
        } else if (tool.is_object()) {
            // Anthropic / direct-schema form: pass through.
            schemas << tool.dump() << "\n";
        }
    }
    if (schemas.tellp() == std::streampos(0)) return "";

    // Verbatim text from ds4_server.c append_tools_prompt_text. Do NOT
    // paraphrase - the model was trained on these exact bytes.
    std::ostringstream os;
    os << "## Tools\n\n"
          "You have access to a set of tools to help answer the user question. "
          "You can invoke tools by writing a \"<\xef\xbd\x9c" "DSML\xef\xbd\x9c" "tool_calls>\" block like the following:\n\n"
          "<\xef\xbd\x9c" "DSML\xef\xbd\x9c" "tool_calls>\n"
          "<\xef\xbd\x9c" "DSML\xef\xbd\x9c" "invoke name=\"$TOOL_NAME\">\n"
          "<\xef\xbd\x9c" "DSML\xef\xbd\x9c" "parameter name=\"$PARAMETER_NAME\" string=\"true|false\">$PARAMETER_VALUE</\xef\xbd\x9c" "DSML\xef\xbd\x9c" "parameter>\n"
          "...\n"
          "</\xef\xbd\x9c" "DSML\xef\xbd\x9c" "invoke>\n"
          "<\xef\xbd\x9c" "DSML\xef\xbd\x9c" "invoke name=\"$TOOL_NAME2\">\n"
          "...\n"
          "</\xef\xbd\x9c" "DSML\xef\xbd\x9c" "invoke>\n"
          "</\xef\xbd\x9c" "DSML\xef\xbd\x9c" "tool_calls>\n\n"
          "String parameters should be specified as raw text and set `string=\"true\"`. "
          "Preserve characters such as `>`, `&`, and `&&` exactly; never replace normal string characters with XML or HTML entity escapes. "
          "Only if a string value itself contains the exact closing parameter tag `</\xef\xbd\x9c" "DSML\xef\xbd\x9c" "parameter>`, write that tag as `&lt;/\xef\xbd\x9c" "DSML\xef\xbd\x9c" "parameter>` inside the value. "
          "For all other types (numbers, booleans, arrays, objects), pass the value in JSON format and set `string=\"false\"`.\n\n"
          "If thinking_mode is enabled (triggered by <think>), you MUST output your complete reasoning inside <think>...</think> BEFORE any tool calls or final response.\n\n"
          "Otherwise, output directly after </think> with tool calls or final response.\n\n"
          "### Available Tool Schemas\n\n"
       << schemas.str()
       << "\nYou MUST strictly follow the above defined tool name and parameter schemas to invoke tool calls. "
          "Use the exact parameter names from the schemas.";
    return os.str();
}

} // namespace ds4cpp
