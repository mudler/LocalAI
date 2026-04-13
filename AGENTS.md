# LocalAI Agent Instructions

This file is an index to detailed topic guides in the `.agents/` directory. Read the relevant file(s) for the task at hand — you don't need to load all of them.

## Topics

| File | When to read |
|------|-------------|
| [.agents/building-and-testing.md](.agents/building-and-testing.md) | Building the project, running tests, Docker builds for specific platforms |
| [.agents/adding-backends.md](.agents/adding-backends.md) | Adding a new backend (Python, Go, or C++) — full step-by-step checklist |
| [.agents/coding-style.md](.agents/coding-style.md) | Code style, editorconfig, logging, documentation conventions |
| [.agents/llama-cpp-backend.md](.agents/llama-cpp-backend.md) | Working on the llama.cpp backend — architecture, updating, tool call parsing |
| [.agents/vllm-backend.md](.agents/vllm-backend.md) | Working on the vLLM / vLLM-omni backends — native parsers, ChatDelta, CPU build, libnuma packaging, backend hooks |
| [.agents/testing-mcp-apps.md](.agents/testing-mcp-apps.md) | Testing MCP Apps (interactive tool UIs) in the React UI |
| [.agents/api-endpoints-and-auth.md](.agents/api-endpoints-and-auth.md) | Adding API endpoints, auth middleware, feature permissions, user access control |
| [.agents/debugging-backends.md](.agents/debugging-backends.md) | Debugging runtime backend failures, dependency conflicts, rebuilding backends |
| [.agents/adding-gallery-models.md](.agents/adding-gallery-models.md) | Adding GGUF models from HuggingFace to the model gallery |

## Quick Reference

- **Logging**: Use `github.com/mudler/xlog` (same API as slog)
- **Go style**: Prefer `any` over `interface{}`
- **Comments**: Explain *why*, not *what*
- **Docs**: Update `docs/content/` when adding features or changing config
- **Build**: Inspect `Makefile` and `.github/workflows/` — ask the user before running long builds
- **UI**: The active UI is the React app in `core/http/react-ui/`. The older Alpine.js/HTML UI in `core/http/static/` is pending deprecation — all new UI work goes in the React UI
