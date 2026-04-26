# LocalAI Agent Instructions

This file is the entry point for AI coding assistants (Claude Code, Cursor, Copilot, Codex, Aider, etc.) working on LocalAI. It is an index to detailed topic guides in the `.agents/` directory. Read the relevant file(s) for the task at hand — you don't need to load all of them.

Human contributors: see [CONTRIBUTING.md](CONTRIBUTING.md) for the development workflow.

## Policy for AI-Assisted Contributions

LocalAI follows the Linux kernel project's [guidelines for AI coding assistants](https://docs.kernel.org/process/coding-assistants.html). Before submitting AI-assisted code, read [.agents/ai-coding-assistants.md](.agents/ai-coding-assistants.md). Key rules:

- **No `Signed-off-by` from AI.** Only the human submitter may sign off on the Developer Certificate of Origin.
- **No `Co-Authored-By: <AI>` trailers.** The human contributor owns the change.
- **Use an `Assisted-by:` trailer** to attribute AI involvement. Format: `Assisted-by: AGENT_NAME:MODEL_VERSION [TOOL1] [TOOL2]`.
- **The human submitter is responsible** for reviewing, testing, and understanding every line of generated code.

## Topics

| File | When to read |
|------|-------------|
| [.agents/ai-coding-assistants.md](.agents/ai-coding-assistants.md) | Policy for AI-assisted contributions — licensing, DCO, attribution |
| [.agents/building-and-testing.md](.agents/building-and-testing.md) | Building the project, running tests, Docker builds for specific platforms |
| [.agents/adding-backends.md](.agents/adding-backends.md) | Adding a new backend (Python, Go, or C++) — full step-by-step checklist, including importer integration (the `/import-model` dropdown is server-driven from `GET /backends/known`) |
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
- **New API endpoints**: LocalAI advertises its capability surface in several independent places — swagger `@Tags`, `/api/instructions` registry, auth `RouteFeatureRegistry`, React UI `capabilities.js`, docs. Read [.agents/api-endpoints-and-auth.md](.agents/api-endpoints-and-auth.md) and follow its checklist — missing any surface means clients, admins, and the UI won't know the endpoint exists.
- **Build**: Inspect `Makefile` and `.github/workflows/` — ask the user before running long builds
- **UI**: The active UI is the React app in `core/http/react-ui/`. The older Alpine.js/HTML UI in `core/http/static/` is pending deprecation — all new UI work goes in the React UI
