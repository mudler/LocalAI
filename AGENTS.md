# AGENTS.md

Context file for AI agents working on LocalAI.

**Dual Format**: This file combines Category A (Operations Manual) and Category B (Context Guide) for comprehensive agent guidance.

**Domain Detected:** Ml / Training (Based on codebase patterns)

## Project Overview

LocalAI is a Go project using Go (Makefile).

**Key Info:**
- **Primary Language:** Go
- **Build System:** Go (Makefile)
- **Test Framework:** Go testing
- **Total Files:** 4478
- **Test Files:** 1313
- **AI Readiness Score:** 100/100 (Agent-Optimized)

---

## 🚨 AI Policy & Operations

Extracted from CONTRIBUTING.md - operational constraints and procedures.

### AI Policy

- Thank you for your interest in contributing to LocalAI! We appreciate your time and effort in helping to improve our project. Before you get started, please take a moment to review these guidelines.
- [Coding Guidelines](#coding-guidelines)
- Use [conventional commits](https://www.conventionalcommits.org/en/v1.0.0/)
- This project uses an [`.editorconfig`](.editorconfig) file to define formatting standards (indentation, line endings, charset, etc.). Please configure your editor to respect it.
- For AI-assisted development, see [`AGENTS.md`](AGENTS.md) (or the equivalent [`CLAUDE.md`](CLAUDE.md) symlink) for agent-specific guidelines including build instructions and backend architecture details. Contributions produced with AI assistance must follow the rules in the [AI Coding Assistants](#ai-coding-assistants) section below.

### Key Requirements

- [Prerequisites](#prerequisites)
- **GCC / C/C++ toolchain** (required for CGo and native backends)
- **Protocol Buffers compiler** (`protoc`) — needed for gRPC code generation
- Include a `requirements.txt` for any new dependencies.

### Development Procedures

- [Testing](#testing)
- [Download Go](https://go.dev/dl/) or install via your package manager
- macOS: `brew install go`
- Ubuntu/Debian: follow the [official instructions](https://go.dev/doc/install) (the `apt` version is often outdated)
- sudo apt-get install -y build-essential gcc g++ cmake git wget \



## 🧠 Machine Learning Architecture

This is a machine learning or model training system.

### Key Components

- **Data Pipeline:** Data loading, preprocessing, augmentation
- **Model Definition:** Architecture, hyperparameters, checkpoints
- **Training Loop:** Loss calculation, gradient updates, validation
- **Inference:** Model predictions, batch processing, latency optimization
- **Evaluation:** Metrics, benchmarks, comparison to baselines

### Critical Areas

1. **Data Leakage:** Ensure train/test/validation splits are isolated
2. **Reproducibility:** Set random seeds; version datasets and models
3. **Resource Management:** Monitor memory, GPU usage during training
4. **Versioning:** Track model checkpoints, hyperparameters, and results
5. **Evaluation Rigor:** Use proper metrics; avoid optimizing to test set

### Testing Strategy

- **Data Pipeline Tests:** Verify shape, type, and value ranges
- **Model Tests:** Check predictions with synthetic/known inputs
- **Training Tests:** Verify loss decreases on toy datasets
- **Inference Tests:** Check latency and memory usage
- **Regression Tests:** Compare results against baseline models





## 🏗️ Architecture & Context Guide

This section provides architectural context and agent-understanding for the codebase.

### Prerequisites

- **Go:** 1.18+ (or applicable language version)
- **Package Manager:** go modules
- **Test Runner:** Go testing

### Environment Requirements

- **Go:** 1.26.0+ (from `go.mod`)
  - GCC required for CGo/SQLite compilation
- **Python:** 1.18+
- **Package Manager:** go modules


### Project Structure

```
LocalAI/
├── Makefile
├── package.json
├── Makefile
├── src/                  # Source code
├── tests/                # Test suite (1313 files)
└── README.md             # Project documentation
```

### Architecture Overview

#### Key Components
- **Main Entry:** main.go, main.go, main.go, main.jsx, index.js
- **Test Suite:** 1313 test files
- **Build Configuration:** Makefile, package.json, Makefile

#### Design Principles

1. **Modularity** - Code organized by functionality with clear separation of concerns
2. **Testability** - Comprehensive test coverage across critical paths
3. **Clarity** - Explicit naming and structure for AI agent understanding
4. **Consistency** - Uniform patterns and conventions throughout codebase
5. **Maintainability** - Well-documented code with clear intent

### Directory Map

| Directory | Purpose |
|-----------|----------|
| `cmd/` | Command-line tools |
| `docs/` | Documentation |
| `examples/` | Usage examples |
| `pkg/` | Package definitions |
| `scripts/` | Build and utility scripts |
| `tests/` | Test suite |


### Development Workflow

#### Initial Setup

```bash
git clone https://github.com/YOUR_ORG/LocalAI.git
cd LocalAI
go mod download
```

#### Development Commands

**Running Tests:**
```bash
go build ./...            # Build project
go test ./...             # Run all tests
go test -v ./...          # Verbose test output
golangci-lint run         # Lint (if installed)
```

#### Code Quality
```bash
gofmt -w .                # Format code
go vet ./...              # Vet (static analysis)
```

### Code Style & Conventions

- **Naming:** Use camelCase for functions and variables
- **Type Hints:** Yes (strongly encouraged)
- **Error Handling:** Yes - handle errors at boundaries; let exceptions propagate when another layer owns recovery
- **Logging:** Yes
- **Testing:** Yes - write tests alongside code changes

### Testing Strategy

**Framework:** Go testing
**Test Files:** 1313 found

Before committing:
1. Run the full test suite: `go test ./...`
2. Ensure all tests pass: `go test -v ./...`
3. Run linter: `golangci-lint run`
4. Format code: `gofmt -w .`

### Writing Documentation

When updating docs:
1. Always include explanatory text before code snippets
2. Describe *why* and *what* before showing *how*
3. Keep sections focused on a single concept
4. Use clear, concrete examples

## Known Gotchas & Warnings

- Keep changes focused. Avoid unrelated refactors, formatting changes, or feature additions in the same PR.
- This downloads test model fixtures, runs protobuf generation, and executes the full test suite including llama-gguf, TTS, and stable-diffusion tests. Note: some tests require model files to be downloaded, so the first run may take longer.
- The React UI (`core/http/react-ui/`) is covered by Playwright e2e specs, gated by a **monotonic line-coverage ratchet** (`make test-ui-coverage-check`, run in CI). The metric is non-deterministic — a fast local box reads higher than a slow CI runner for the same code — so a small tolerance is unavoidable.

### Contributing Guidelines

This project has a detailed contribution guide at **`CONTRIBUTING.md`**.

**Key Requirements:**
- **DCO Sign-off Required**: Every commit must be signed with `git commit -s`

**Before submitting:**
1. Read `CONTRIBUTING.md` in full
2. Check recent merged PRs for patterns
3. Follow the specific requirements above

### Common Patterns

When contributing to this project:
1. Read existing code in the area you're modifying
2. Follow the established patterns and style
3. Write tests for new functionality
4. Use clear, descriptive variable and function names
5. Add docstrings for public APIs
6. Update tests when changing behavior

### What We Value

✅ Well-tested code with clear intent
✅ Consistent code style and naming conventions
✅ Code that is easy for AI agents to understand
✅ Clear, descriptive commit messages
✅ Modular, reusable components
✅ Comprehensive documentation

### What We Avoid

❌ Large functions doing multiple things
❌ Commented-out dead code
❌ Inconsistent naming or patterns
❌ Unclear error messages
❌ Unexplained magic numbers or strings
❌ Skipped tests or test TODOs

### AI Readiness Dimensions (Scoring)

This project is evaluated across 8 dimensions:

1. **Architecture** (20/100) - Code organization and modularity
2. **Testing** (15/100) - Test coverage and quality
3. **Dependencies** (12/100) - Dependency management
4. **Conventions** (8/100) - Consistent patterns
5. **Entry Points** (10/100) - Clear main/start locations
6. **Security** (15/100) - Input validation and error handling
7. **Build** (10/100) - Clear build/setup instructions
8. **Documentation** (8/100) - Code and project documentation

### Next Steps

Before making changes:
1. Read relevant source files to understand the existing code
2. Look at existing tests for similar functionality
3. Follow the patterns you see in the codebase
4. Write tests for your changes
5. Run `pytest` to verify nothing breaks
6. Run code quality checks: `ruff check . && mypy .`
7. Format your code: `ruff format .`

---

*Generated by Braxis - keeping AI agents in sync with your code*
