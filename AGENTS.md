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
| [.agents/ci-caching.md](.agents/ci-caching.md) | CI build cache layout (registry-backed BuildKit cache on quay.io/go-skynet/ci-cache, per-arch keys), `DEPS_REFRESH` weekly cache-buster for unpinned Python deps, prebuilt `base-grpc-*` images for llama.cpp variants, per-arch native + manifest-merge pattern, `setup-build-disk` `/mnt` relocation, path filter on master push, manual eviction |
| [.agents/adding-backends.md](.agents/adding-backends.md) | Adding a new backend (Python, Go, or C++) — full step-by-step checklist, including importer integration (the `/import-model` dropdown is server-driven from `GET /backends/known`) |
| [.agents/coding-style.md](.agents/coding-style.md) | Code style, editorconfig, logging, documentation conventions |
| [.agents/llama-cpp-backend.md](.agents/llama-cpp-backend.md) | Working on the llama.cpp backend — architecture, updating, tool call parsing |
| [.agents/vllm-backend.md](.agents/vllm-backend.md) | Working on the vLLM / vLLM-omni backends — native parsers, ChatDelta, CPU build, libnuma packaging, backend hooks |
| [.agents/sglang-backend.md](.agents/sglang-backend.md) | Working on the SGLang backend — `engine_args` validation against ServerArgs, speculative-decoding (EAGLE/EAGLE3/DFLASH/MTP) recipes, parser handling |
| [.agents/ds4-backend.md](.agents/ds4-backend.md) | Working on the ds4 backend - DSML state machine, thinking modes, KV cache, Metal+CUDA matrix |
| [.agents/testing-mcp-apps.md](.agents/testing-mcp-apps.md) | Testing MCP Apps (interactive tool UIs) in the React UI |
| [.agents/api-endpoints-and-auth.md](.agents/api-endpoints-and-auth.md) | Adding API endpoints, auth middleware, feature permissions, user access control |
| [.agents/debugging-backends.md](.agents/debugging-backends.md) | Debugging runtime backend failures, dependency conflicts, rebuilding backends |
| [.agents/adding-gallery-models.md](.agents/adding-gallery-models.md) | Adding GGUF models from HuggingFace to the model gallery |
| [.agents/localai-assistant-mcp.md](.agents/localai-assistant-mcp.md) | LocalAI Assistant chat modality — adding admin tools to the in-process MCP server, editing skill prompts, keeping REST + MCP + skills in sync |
| [.agents/backend-signing.md](.agents/backend-signing.md) | Backend OCI image signing (keyless cosign + sigstore-go) — producer-side CI setup, consumer-side gallery `verification:` block, strict mode (`LOCALAI_REQUIRE_BACKEND_INTEGRITY`), revocation via `not_before` |

## Quick Reference

- **Git hooks & coverage gates**: Run `make install-hooks` once per clone so the pre-commit lint + coverage gates run. **Never bypass them with `git commit --no-verify`, and never lower a coverage baseline or widen a gate's tolerance to turn a red gate green** — the coverage ratchet only moves up. If a change drops coverage, add tests to raise it (e.g. render-smoke specs). See [.agents/building-and-testing.md](.agents/building-and-testing.md).
- **Logging**: Use `github.com/mudler/xlog` (same API as slog)
- **Go style**: Prefer `any` over `interface{}`
- **Comments**: Explain *why*, not *what*
- **Docs (docs-with-code rule)**: When you change user-facing behavior (API endpoints, CLI flags, config keys, or features), update the corresponding page under `docs/content/` in the SAME change, not as a follow-up. A user-facing change without a matching docs update is incomplete. See also the documentation conventions in [.agents/coding-style.md](.agents/coding-style.md).
- **New API endpoints**: LocalAI advertises its capability surface in several independent places — swagger `@Tags`, `/api/instructions` registry, auth `RouteFeatureRegistry`, React UI `capabilities.js`, docs. Read [.agents/api-endpoints-and-auth.md](.agents/api-endpoints-and-auth.md) and follow its checklist — missing any surface means clients, admins, and the UI won't know the endpoint exists.
- **Admin endpoints → MCP tool**: every admin endpoint that an admin would manage conversationally (install/list/edit/toggle/upgrade) MUST also be exposed as an MCP tool in `pkg/mcp/localaitools/`. The LocalAI Assistant chat modality and the standalone `local-ai mcp-server` consume that package; drift between REST and MCP is a real risk. Read [.agents/localai-assistant-mcp.md](.agents/localai-assistant-mcp.md) — the `TestToolHTTPRouteMappingComplete` test fails until you wire the new tool and update the route map.
- **Build**: Inspect `Makefile` and `.github/workflows/` — ask the user before running long builds
- **Backend OS coverage**: a new backend must target every OS it can build for, not just Linux. `.github/backend-matrix.yml` has two matrices — `include:` (Linux) and `includeDarwin:` (macOS / Apple Silicon). Most C/C++/GGML and many Python backends build on Darwin too — wire the `includeDarwin` entry + `backend/index.yaml` `metal:` entries, or say in the PR why an OS is unsupported. See the darwin checklist in [.agents/adding-backends.md](.agents/adding-backends.md).
- **UI**: The active UI is the React app in `core/http/react-ui/`. The older Alpine.js/HTML UI in `core/http/static/` is pending deprecation — all new UI work goes in the React UI
