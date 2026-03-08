# Contributing to LocalAI

Thank you for your interest in contributing to LocalAI! We appreciate your time and effort in helping to improve our project. Before you get started, please take a moment to review these guidelines.

## Table of Contents

- [Getting Started](#getting-started)
  - [Prerequisites](#prerequisites)
  - [Setting up the Development Environment](#setting-up-the-development-environment)
  - [Environment Variables](#environment-variables)
- [Contributing](#contributing)
  - [Submitting an Issue](#submitting-an-issue)
  - [Development Workflow](#development-workflow)
  - [Creating a Pull Request (PR)](#creating-a-pull-request-pr)
- [Coding Guidelines](#coding-guidelines)
- [Testing](#testing)
- [Documentation](#documentation)
- [Community and Communication](#community-and-communication)

## Getting Started

### Prerequisites

- **Go 1.21+** (the project currently uses Go 1.26 in `go.mod`, but 1.21 is the minimum supported version)
  - [Download Go](https://go.dev/dl/) or install via your package manager
  - macOS: `brew install go`
  - Ubuntu/Debian: follow the [official instructions](https://go.dev/doc/install) (the `apt` version is often outdated)
  - Verify: `go version`
- **Git**
- **GNU Make**
- **GCC / C/C++ toolchain** (required for CGo and native backends)
- **Protocol Buffers compiler** (`protoc`) — needed for gRPC code generation

#### System dependencies by platform

<details>
<summary><strong>Ubuntu / Debian</strong></summary>

```bash
sudo apt-get update
sudo apt-get install -y build-essential gcc g++ cmake git wget \
  protobuf-compiler libprotobuf-dev pkg-config \
  libopencv-dev libgrpc-dev
```

</details>

<details>
<summary><strong>CentOS / RHEL / Fedora</strong></summary>

```bash
sudo dnf groupinstall -y "Development Tools"
sudo dnf install -y cmake git wget protobuf-compiler protobuf-devel \
  opencv-devel grpc-devel
```

</details>

<details>
<summary><strong>macOS</strong></summary>

```bash
xcode-select --install
brew install cmake git protobuf grpc opencv wget
```

</details>

<details>
<summary><strong>Windows</strong></summary>

Use [WSL 2](https://learn.microsoft.com/en-us/windows/wsl/install) with an Ubuntu distribution, then follow the Ubuntu instructions above.

</details>

### Setting up the Development Environment

1. **Clone the repository:**

   ```bash
   git clone https://github.com/mudler/LocalAI.git
   cd LocalAI
   ```

2. **Build LocalAI:**

   ```bash
   make build
   ```

   This runs protobuf generation, installs Go tools, builds the React UI, and compiles the `local-ai` binary. Key build variables you can set:

   | Variable | Description | Example |
   |---|---|---|
   | `BUILD_TYPE` | GPU/accelerator type (`cublas`, `hipblas`, `intel`, ``) | `BUILD_TYPE=cublas make build` |
   | `GO_TAGS` | Additional Go build tags | `GO_TAGS=debug make build` |
   | `CUDA_MAJOR_VERSION` | CUDA major version (default: `13`) | `CUDA_MAJOR_VERSION=12` |

3. **Run LocalAI:**

   ```bash
   ./local-ai
   ```

4. **Development mode with live reload:**

   ```bash
   make build-dev
   ```

   This installs [`air`](https://github.com/air-verse/air) automatically and watches for file changes, rebuilding and restarting the server on each save.

5. **Containerized build** (no local toolchain needed):

   ```bash
   make docker
   ```

   For GPU-specific Docker builds, see the `docker-build-*` targets in the Makefile and refer to [CLAUDE.md](CLAUDE.md) for detailed backend build instructions.

### Environment Variables

LocalAI is configured primarily through environment variables (or equivalent CLI flags). The most useful ones for development are:

| Variable | Description | Default |
|---|---|---|
| `LOCALAI_DEBUG` | Enable debug mode | `false` |
| `LOCALAI_LOG_LEVEL` | Log verbosity (`error`, `warn`, `info`, `debug`, `trace`) | — |
| `LOCALAI_LOG_FORMAT` | Log format (`default`, `text`, `json`) | `default` |
| `LOCALAI_MODELS_PATH` | Path to model files | `./models` |
| `LOCALAI_BACKENDS_PATH` | Path to backend binaries | `./backends` |
| `LOCALAI_CONFIG_DIR` | Directory for dynamic config files (API keys, external backends) | `./configuration` |
| `LOCALAI_THREADS` | Number of threads for inference | — |
| `LOCALAI_ADDRESS` | Bind address for the API server | `:8080` |
| `LOCALAI_API_KEY` | API key(s) for authentication | — |
| `LOCALAI_CORS` | Enable CORS | `false` |
| `LOCALAI_DISABLE_WEBUI` | Disable the web UI | `false` |

See `core/cli/run.go` for the full list of supported environment variables.

## Contributing

We welcome contributions from everyone! To get started, follow these steps:

### Submitting an Issue

If you find a bug, have a feature request, or encounter any issues, please check the [issue tracker](https://github.com/go-skynet/LocalAI/issues) to see if a similar issue has already been reported. If not, feel free to [create a new issue](https://github.com/go-skynet/LocalAI/issues/new) and provide as much detail as possible.

### Development Workflow

#### Branch naming conventions

Use a descriptive branch name that indicates the type and scope of the change:

- `feature/<short-description>` — new functionality
- `fix/<short-description>` — bug fixes
- `docs/<short-description>` — documentation changes
- `refactor/<short-description>` — code refactoring

#### Commit messages

- Use a short, imperative subject line (e.g., "feat: add whisper backend support", not "Added whisper backend support")
- Keep the subject under 72 characters
- Use the body to explain **why** the change was made when the subject alone is not sufficient
- Use [conventional commits](https://www.conventionalcommits.org/en/v1.0.0/)

#### Creating a Pull Request (PR)

Before jumping into a PR for a massive feature or big change, it is preferred to discuss it first via an issue.

1. Fork the repository.
2. Create a new branch: `git checkout -b feature/my-change`
3. Make your changes, keeping commits focused and atomic.
4. Run tests locally before pushing (see [Testing](#testing) below).
5. Push to your fork: `git push origin feature/my-change`
6. Open a pull request against the `master` branch.
7. Fill in the PR description with:
   - What the change does and why
   - How it was tested
   - Any breaking changes or migration steps
8. Respond to review feedback promptly. Push follow-up commits rather than force-pushing amended commits so reviewers can see incremental changes.
9. Once approved, a maintainer will merge your PR.

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

All new features and bug fixes should include test coverage. The project uses [Ginkgo](https://onsi.github.io/ginkgo/) as its test framework.

### Running unit tests

```bash
make test
```

This downloads test model fixtures, runs protobuf generation, and executes the full test suite including llama-gguf, TTS, and stable-diffusion tests. Note: some tests require model files to be downloaded, so the first run may take longer.

To run tests for a specific package:

```bash
go test ./core/config/...
go test ./pkg/model/...
```

To run a specific test by name using Ginkgo's `--focus` flag:

```bash
go run github.com/onsi/ginkgo/v2/ginkgo --focus="should load a model" -v -r ./core/
```

### Running end-to-end tests

The e2e tests run LocalAI in a Docker container and exercise the API:

```bash
make test-e2e
```

### Running AIO tests

All-In-One images have a set of tests that automatically verify that most of the endpoints work correctly:

```bash
# Build the LocalAI docker image
make DOCKER_IMAGE=local-ai docker

# Build the corresponding AIO image
BASE_IMAGE=local-ai DOCKER_AIO_IMAGE=local-ai-aio:test make docker-aio

# Run the AIO e2e tests
LOCALAI_IMAGE_TAG=test LOCALAI_IMAGE=local-ai-aio make run-e2e-aio
```

### Testing backends

To prepare and test extra (Python) backends:

```bash
make prepare-test-extra   # build Python backends for testing
make test-extra           # run backend-specific tests
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
