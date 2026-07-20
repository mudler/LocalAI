// Unit tests for the backend matrix path filter (scripts/lib/backend-filter.mjs).
//
// Run with `make test-ci-scripts` (plain `node --test`, no dependencies), which
// is what .github/workflows/lint.yml runs. The production entrypoint
// scripts/changed-backends.js needs bun + js-yaml + @octokit/core; the logic
// under test here needs none of that.

import test from "node:test";
import assert from "node:assert/strict";

import { filterMatrix } from "./backend-filter.mjs";

// A representative slice of .github/backend-matrix.yml: one entry per build
// path that the filter treats differently. Kept inline rather than parsed from
// the real YAML so these tests stay dependency-free and stable when backends
// are added or removed.
const includes = [
  {
    backend: "diffusers",
    dockerfile: "./backend/Dockerfile.python",
    "tag-suffix": "-nvidia-cuda-13-diffusers",
  },
  {
    backend: "vllm",
    dockerfile: "./backend/Dockerfile.python",
    "tag-suffix": "-nvidia-cuda-13-vllm",
  },
  {
    backend: "ced",
    dockerfile: "./backend/Dockerfile.golang",
    "tag-suffix": "-ced",
  },
  {
    backend: "kokoros",
    dockerfile: "./backend/Dockerfile.rust",
    "tag-suffix": "-kokoros",
  },
  {
    backend: "llama-cpp",
    dockerfile: "./backend/Dockerfile.llama-cpp",
    "tag-suffix": "-llama-cpp",
  },
  {
    backend: "turboquant",
    dockerfile: "./backend/Dockerfile.turboquant",
    "tag-suffix": "-turboquant",
  },
];

const includesDarwin = [
  { backend: "diffusers", "tag-suffix": "-metal-darwin-arm64-diffusers" },
  { backend: "mlx", "tag-suffix": "-metal-darwin-arm64-mlx" },
  { backend: "whisper", lang: "go", "tag-suffix": "-metal-darwin-arm64-whisper" },
  { backend: "llama-cpp", lang: "go", "tag-suffix": "-metal-darwin-arm64-llama-cpp" },
  { backend: "ds4", lang: "go", "tag-suffix": "-metal-darwin-arm64-ds4" },
];

const run = changedFiles =>
  filterMatrix({ includes, includesDarwin, changedFiles });

const names = entries => entries.map(e => e.backend).sort();

test("a change to only package-gpu-libs.sh rebuilds every Python image", () => {
  // The PR #10946 regression: this script is COPY'd and run by
  // Dockerfile.python for every Python backend, but lives under scripts/, so
  // the per-backend prefix match produced an empty matrix and the cuDNN
  // packaging fix shipped to nothing.
  const { filtered, filteredDarwin, changedBackends } = run([
    "scripts/build/package-gpu-libs.sh",
  ]);

  assert.notEqual(filtered.length, 0, "expected a non-empty Linux matrix");
  assert.deepEqual(names(filtered), ["diffusers", "vllm"]);
  assert.ok(changedBackends.has("vllm"));

  // Darwin Python builds never invoke it (see scripts/build/python-darwin.sh).
  assert.deepEqual(filteredDarwin, []);
});

test("docs- and README-only changes never produce a matrix", () => {
  const { filtered, filteredDarwin, changedBackends } = run([
    "README.md",
    "docs/content/docs/overview.md",
  ]);

  assert.deepEqual(filtered, []);
  assert.deepEqual(filteredDarwin, []);
  assert.equal(changedBackends.size, 0);
});

test("a backend's own directory still triggers only that backend", () => {
  const { filtered, changedBackends } = run(["backend/python/vllm/backend.py"]);

  assert.deepEqual(names(filtered), ["vllm"]);
  assert.deepEqual([...changedBackends], ["vllm"]);
});

test("Dockerfile.python rebuilds Python images and nothing else", () => {
  const { filtered, filteredDarwin } = run(["backend/Dockerfile.python"]);

  assert.deepEqual(names(filtered), ["diffusers", "vllm"]);
  // Darwin doesn't build from any Dockerfile.
  assert.deepEqual(filteredDarwin, []);
});

test("Dockerfile.golang rebuilds Go images and nothing else", () => {
  const { filtered } = run(["backend/Dockerfile.golang"]);

  assert.deepEqual(names(filtered), ["ced"]);
});

test("a per-family Dockerfile rebuilds only its own family", () => {
  const { filtered } = run(["backend/Dockerfile.llama-cpp"]);

  assert.deepEqual(names(filtered), ["llama-cpp"]);
});

test("backend.proto rebuilds every backend on every OS", () => {
  const { filtered, filteredDarwin } = run(["backend/backend.proto"]);

  assert.equal(filtered.length, includes.length);
  assert.equal(filteredDarwin.length, includesDarwin.length);
});

test("backend/python/common rebuilds Python images on Linux and Darwin", () => {
  const { filtered, filteredDarwin } = run([
    "backend/python/common/libbackend.sh",
  ]);

  assert.deepEqual(names(filtered), ["diffusers", "vllm"]);
  assert.deepEqual(names(filteredDarwin), ["diffusers", "mlx"]);
});

test("the reusable Linux build workflow rebuilds Linux only", () => {
  const { filtered, filteredDarwin } = run([
    ".github/workflows/backend_build.yml",
  ]);

  assert.equal(filtered.length, includes.length);
  assert.deepEqual(filteredDarwin, []);
});

test("the reusable Darwin build workflow rebuilds Darwin only", () => {
  const { filtered, filteredDarwin } = run([
    ".github/workflows/backend_build_darwin.yml",
  ]);

  assert.deepEqual(filtered, []);
  assert.equal(filteredDarwin.length, includesDarwin.length);
});

test("golang-darwin.sh skips backends with bespoke Darwin build scripts", () => {
  const { filtered, filteredDarwin } = run([
    "scripts/build/golang-darwin.sh",
  ]);

  assert.deepEqual(filtered, []);
  assert.deepEqual(names(filteredDarwin), ["whisper"]);
});

test("a bespoke Darwin build script rebuilds only its own backend", () => {
  const { filteredDarwin } = run(["scripts/build/ds4-darwin.sh"]);

  assert.deepEqual(names(filteredDarwin), ["ds4"]);
});

test("an unclassified scripts/build/ file conservatively rebuilds everything", () => {
  const { filtered, filteredDarwin } = run([
    "scripts/build/package-something-new.sh",
  ]);

  assert.equal(filtered.length, includes.length);
  assert.equal(filteredDarwin.length, includesDarwin.length);
});

test("tests for the packaging scripts do not rebuild anything", () => {
  const { filtered, filteredDarwin } = run([
    "scripts/build/package-gpu-libs_test.sh",
  ]);

  assert.deepEqual(filtered, []);
  assert.deepEqual(filteredDarwin, []);
});

test("turboquant still retriggers on llama-cpp source changes", () => {
  const { filtered } = run(["backend/cpp/llama-cpp/grpc-server.cpp"]);

  assert.deepEqual(names(filtered), ["llama-cpp", "turboquant"]);
});
