#pragma once
#include <string>

namespace ds4cpp {

// Render an assistant message's tool_calls JSON array into the DSML block
// that ds4 expects in its prompt. `tool_calls_json` is the value of
// proto.Message.tool_calls (OpenAI shape: array of {id, type, function:{name, arguments}}).
// Returns the DSML text to append after the assistant's content.
std::string RenderAssistantToolCalls(const std::string &tool_calls_json);

// Render a role="tool" message into the DSML "tool result" block. ds4's
// prompt template expects tool results inside a specific tag; we wrap the
// `content` with that tag and include the `tool_call_id` so the model can
// correlate.
std::string RenderToolResult(const std::string &tool_call_id, const std::string &content);

// Render the "## Tools" manifest that ds4 expects in the SYSTEM prompt when
// tools are available. Without this preamble the model has no idea tools
// exist and will not emit DSML tool calls. Mirrors append_tools_prompt_text()
// in ds4_server.c (~line 1646): a fixed preamble + "### Available Tool
// Schemas" section + one JSON schema per line (extracted from each OpenAI
// tool's .function object) + a fixed closing instruction. Returns empty
// when tools_json is empty / unparseable.
std::string RenderToolsManifest(const std::string &tools_json);

} // namespace ds4cpp
