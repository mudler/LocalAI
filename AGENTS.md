# Build and testing

Building and testing the project depends on the components involved and the platform where development is taking place. Due to the amount of context required it's usually best not to try building or testing the project unless the user requests it. If you must build the project then inspect the Makefile in the project root and the Makefiles of any backends that are effected by changes you are making. In addition the workflows in .github/workflows can be used as a reference when it is unclear how to build or test a component. The primary Makefile contains targets for building inside or outside Docker, if the user has not previously specified a preference then ask which they would like to use.

# Coding style

- The project has the following .editorconfig

```
root = true

[*]
indent_style = space
indent_size = 2
end_of_line = lf
charset = utf-8
trim_trailing_whitespace = true
insert_final_newline = true

[*.go]
indent_style = tab

[Makefile]
indent_style = tab

[*.proto]
indent_size = 2

[*.py]
indent_size = 4

[*.js]
indent_size = 2

[*.yaml]
indent_size = 2

[*.md]
trim_trailing_whitespace = false
```

- Use comments sparingly to explain why code does something, not what it does. Comments are there to add context that would be difficult to deduce from reading the code.
- Prefer modern Go e.g. use `any` not `interface{}`

# Logging

Use `github.com/mudler/xlog` for logging which has the same API as slog.

# llama.cpp Backend

The llama.cpp backend (`backend/cpp/llama-cpp/grpc-server.cpp`) is a gRPC adaptation of the upstream HTTP server (`llama.cpp/tools/server/server.cpp`). It uses the same underlying server infrastructure from `llama.cpp/tools/server/server-context.cpp`.

## Building and Testing

- Test llama.cpp backend compilation: `make backends/llama-cpp`
- The backend is built as part of the main build process
- Check `backend/cpp/llama-cpp/Makefile` for build configuration

## Architecture

- **grpc-server.cpp**: gRPC server implementation, adapts HTTP server patterns to gRPC
- Uses shared server infrastructure: `server-context.cpp`, `server-task.cpp`, `server-queue.cpp`, `server-common.cpp`
- The gRPC server mirrors the HTTP server's functionality but uses gRPC instead of HTTP

## Common Issues When Updating llama.cpp

When fixing compilation errors after upstream changes:
1. Check how `server.cpp` (HTTP server) handles the same change
2. Look for new public APIs or getter methods
3. Store copies of needed data instead of accessing private members
4. Update function calls to match new signatures
5. Test with `make backends/llama-cpp`

## Key Differences from HTTP Server

- gRPC uses `BackendServiceImpl` class with gRPC service methods
- HTTP server uses `server_routes` with HTTP handlers
- Both use the same `server_context` and task queue infrastructure
- gRPC methods: `LoadModel`, `Predict`, `PredictStream`, `Embedding`, `Rerank`, `TokenizeString`, `GetMetrics`, `Health`

## Tool Call Parsing Maintenance

When working on JSON/XML tool call parsing functionality, always check llama.cpp for reference implementation and updates:

### Checking for XML Parsing Changes

1. **Review XML Format Definitions**: Check `llama.cpp/common/chat-parser-xml-toolcall.h` for `xml_tool_call_format` struct changes
2. **Review Parsing Logic**: Check `llama.cpp/common/chat-parser-xml-toolcall.cpp` for parsing algorithm updates
3. **Review Format Presets**: Check `llama.cpp/common/chat-parser.cpp` for new XML format presets (search for `xml_tool_call_format form`)
4. **Review Model Lists**: Check `llama.cpp/common/chat.h` for `COMMON_CHAT_FORMAT_*` enum values that use XML parsing:
   - `COMMON_CHAT_FORMAT_GLM_4_5`
   - `COMMON_CHAT_FORMAT_MINIMAX_M2`
   - `COMMON_CHAT_FORMAT_KIMI_K2`
   - `COMMON_CHAT_FORMAT_QWEN3_CODER_XML`
   - `COMMON_CHAT_FORMAT_APRIEL_1_5`
   - `COMMON_CHAT_FORMAT_XIAOMI_MIMO`
   - Any new formats added

### Model Configuration Options

Always check `llama.cpp` for new model configuration options that should be supported in LocalAI:

1. **Check Server Context**: Review `llama.cpp/tools/server/server-context.cpp` for new parameters
2. **Check Chat Params**: Review `llama.cpp/common/chat.h` for `common_chat_params` struct changes
3. **Check Server Options**: Review `llama.cpp/tools/server/server.cpp` for command-line argument changes
4. **Examples of options to check**:
   - `ctx_shift` - Context shifting support
   - `parallel_tool_calls` - Parallel tool calling
   - `reasoning_format` - Reasoning format options
   - Any new flags or parameters

### Implementation Guidelines

1. **Feature Parity**: Always aim for feature parity with llama.cpp's implementation
2. **Test Coverage**: Add tests for new features matching llama.cpp's behavior
3. **Documentation**: Update relevant documentation when adding new formats or options
4. **Backward Compatibility**: Ensure changes don't break existing functionality

### Files to Monitor

- `llama.cpp/common/chat-parser-xml-toolcall.h` - Format definitions
- `llama.cpp/common/chat-parser-xml-toolcall.cpp` - Parsing logic
- `llama.cpp/common/chat-parser.cpp` - Format presets and model-specific handlers
- `llama.cpp/common/chat.h` - Format enums and parameter structures
- `llama.cpp/tools/server/server-context.cpp` - Server configuration options
