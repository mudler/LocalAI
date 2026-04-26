# AI Coding Assistants

This document provides guidance for AI tools and developers using AI
assistance when contributing to LocalAI.

**LocalAI follows the same guidelines as the Linux kernel project for
AI-assisted contributions.** See the upstream policy here:
<https://docs.kernel.org/process/coding-assistants.html>

The rules below mirror that policy, adapted to LocalAI's license and
project layout. If anything is unclear, the kernel document is the
authoritative reference for intent.

AI tools helping with LocalAI development should follow the standard
project development process:

- [CONTRIBUTING.md](../CONTRIBUTING.md) — development workflow, commit
  conventions, and PR guidelines
- [.agents/coding-style.md](coding-style.md) — code style, editorconfig,
  logging, and documentation conventions
- [.agents/building-and-testing.md](building-and-testing.md) — build and
  test procedures

## Licensing and Legal Requirements

All contributions must comply with LocalAI's licensing requirements:

- LocalAI is licensed under the **MIT License** — see the [LICENSE](../LICENSE)
  file
- New source files should use the SPDX license identifier `MIT` where
  applicable to the file type
- Contributions must be compatible with the MIT License and must not
  introduce code under incompatible licenses (e.g., GPL) without an
  explicit discussion with maintainers

## Signed-off-by and Developer Certificate of Origin

**AI agents MUST NOT add `Signed-off-by` tags.** Only humans can legally
certify the Developer Certificate of Origin (DCO). The human submitter
is responsible for:

- Reviewing all AI-generated code
- Ensuring compliance with licensing requirements
- Adding their own `Signed-off-by` tag (when the project requires DCO)
  to certify the contribution
- Taking full responsibility for the contribution

AI agents MUST NOT add `Co-Authored-By` trailers for themselves either.
A human reviewer owns the contribution; the AI's involvement is recorded
via `Assisted-by` (see below).

## Attribution

When AI tools contribute to LocalAI development, proper attribution helps
track the evolving role of AI in the development process. Contributions
should include an `Assisted-by` tag in the commit message trailer in the
following format:

```
Assisted-by: AGENT_NAME:MODEL_VERSION [TOOL1] [TOOL2]
```

Where:

- `AGENT_NAME` — name of the AI tool or framework (e.g., `Claude`,
  `Copilot`, `Cursor`)
- `MODEL_VERSION` — specific model version used (e.g.,
  `claude-opus-4-7`, `gpt-5`)
- `[TOOL1] [TOOL2]` — optional specialized analysis tools invoked by the
  agent (e.g., `golangci-lint`, `staticcheck`, `go vet`)

Basic development tools (git, go, make, editors) should **not** be listed.

### Example

```
fix(llama-cpp): handle empty tool call arguments

Previously the parser panicked when the model returned a tool call with
an empty arguments object. Fall back to an empty JSON object in that
case so downstream consumers receive a valid payload.

Assisted-by: Claude:claude-opus-4-7 golangci-lint
Signed-off-by: Jane Developer <jane@example.com>
```

## Scope and Responsibility

Using an AI assistant does not reduce the contributor's responsibility.
The human submitter must:

- Understand every line that lands in the PR
- Verify that generated code compiles, passes tests, and follows the
  project style
- Confirm that any referenced APIs, flags, or file paths actually exist
  in the current tree (AI models may hallucinate identifiers)
- Not submit AI output verbatim without review

Reviewers may ask for clarification on any change regardless of how it
was produced. "An AI wrote it" is not an acceptable answer to a design
question.
