# Contributing to LocalAI

Thank you for your interest in contributing to LocalAI! We appreciate your time and effort in helping to improve our project. Before you get started, please take a moment to review these guidelines.

## Table of Contents

- [Getting Started](#getting-started)
  - [Prerequisites](#prerequisites)
  - [Setting up the Development Environment](#setting-up-the-development-environment)
- [Contributing](#contributing)
  - [Submitting an Issue](#submitting-an-issue)
  - [Creating a Pull Request (PR)](#creating-a-pull-request-pr)
- [Coding Guidelines](#coding-guidelines)
- [Testing](#testing)
- [Documentation](#documentation)
- [Community and Communication](#community-and-communication)

## Getting Started

### Prerequisites

- Golang [1.21]
- Git
- macOS/Linux

### Setting up the Development Environment and running localAI in the local environment

1. Clone the repository: `git clone https://github.com/go-skynet/LocalAI.git`
2. Navigate to the project directory: `cd LocalAI`
3. Install the required dependencies ( see https://localai.io/basics/build/#build-localai-locally )
4. Build LocalAI: `make build`
5. Run LocalAI: `./local-ai`
6. To Build and live reload: `make build-dev`

## Contributing

We welcome contributions from everyone! To get started, follow these steps:

### Submitting an Issue

If you find a bug, have a feature request, or encounter any issues, please check the [issue tracker](https://github.com/go-skynet/LocalAI/issues) to see if a similar issue has already been reported. If not, feel free to [create a new issue](https://github.com/go-skynet/LocalAI/issues/new) and provide as much detail as possible.

### Creating a Pull Request (PR)

Before jumping into a PR for a massive feature or big change, it is preferred to discuss it first via an issue.

1. Fork the repository.
2. Create a new branch with a descriptive name: `git checkout -b [branch name]`
3. Make your changes and commit them.
4. Push the changes to your fork: `git push origin [branch name]`
5. Create a new pull request from your branch to the main project's `main` or `master` branch.
6. Provide a clear description of your changes in the pull request.
7. Make any requested changes during the review process.
8. Once your PR is approved, it will be merged into the main project.

## Coding Guidelines

This project uses an [`.editorconfig`](.editorconfig) file to define formatting standards (indentation, line endings, charset, etc.). Please configure your editor to respect it.

For AI-assisted development, see [`CLAUDE.md`](CLAUDE.md) for agent-specific guidelines including build instructions and backend architecture details.

### General Principles

- Write code that can be tested. All new features and bug fixes should include test coverage.
- Use comments sparingly to explain **why** code does something, not **what** it does. Comments should add context that would be difficult to deduce from reading the code alone.
- Keep changes focused. Avoid unrelated refactors, formatting changes, or feature additions in the same PR.

### Go Code

- Prefer modern Go idioms — for example, use `any` instead of `interface{}`.
- Use [`golangci-lint`](https://golangci-lint.run) to catch common issues before submitting a PR.
- Use [`github.com/mudler/xlog`](https://github.com/mudler/xlog) for logging (same API as `slog`). Do not use `fmt.Println` or the standard `log` package for operational logging.
- Use tab indentation for Go files (as defined in `.editorconfig`).

### Python Code

- Use 4-space indentation (as defined in `.editorconfig`).
- Include a `requirements.txt` for any new dependencies.

### Code Review

- All contributions go through code review via pull requests.
- Reviewers will check for correctness, test coverage, adherence to these guidelines, and clarity of intent.
- Be responsive to review feedback and keep discussions constructive.

## Testing

`make test` cannot handle all the model now. Please be sure to add a test case for the new features or the part was changed.

### Running AIO tests

All-In-One images has a set of tests that automatically verifies that most of the endpoints works correctly, a flow can be :

```bash
# Build the LocalAI docker image
make DOCKER_IMAGE=local-ai docker

# Build the corresponding AIO image
BASE_IMAGE=local-ai DOCKER_AIO_IMAGE=local-ai-aio:test make docker-aio

# Run the AIO e2e tests
LOCALAI_IMAGE_TAG=test LOCALAI_IMAGE=local-ai-aio make run-e2e-aio
```

## Documentation

We are welcome the contribution of the documents, please open new PR or create a new issue. The documentation is available under `docs/` https://github.com/mudler/LocalAI/tree/master/docs

### Gallery YAML Schema

LocalAI provides a JSON Schema for gallery model YAML files at:

`core/schema/gallery-model.schema.json`

This schema mirrors the internal gallery model configuration and can be used by editors (such as VS Code) to enable autocomplete, validation, and inline documentation when creating or modifying gallery files.

To use it with the YAML language server, add the following comment at the top of a gallery YAML file:

```yaml
# yaml-language-server: $schema=../core/schema/gallery-model.schema.json
```

## Community and Communication

- You can reach out via the Github issue tracker.
- Open a new discussion at [Discussion](https://github.com/go-skynet/LocalAI/discussions)
- Join the Discord channel [Discord](https://discord.gg/uJAeKSAGDy)
