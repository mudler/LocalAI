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
    "base-image": "nvidia/cuda:13.0.0-devel-ubuntu24.04",
  },
  {
    backend: "vllm",
    dockerfile: "./backend/Dockerfile.python",
    "tag-suffix": "-nvidia-cuda-13-vllm",
    "base-image": "nvidia/cuda:13.0.0-devel-ubuntu24.04",
  },
  {
    backend: "ced",
    dockerfile: "./backend/Dockerfile.golang",
    "tag-suffix": "-ced",
    "base-image": "ubuntu:24.04",
  },
  {
    backend: "kokoros",
    dockerfile: "./backend/Dockerfile.rust",
    "tag-suffix": "-kokoros",
    "base-image": "ubuntu:24.04",
  },
  {
    backend: "llama-cpp",
    dockerfile: "./backend/Dockerfile.llama-cpp",
    "tag-suffix": "-llama-cpp",
    "base-image": "ubuntu:24.04",
  },
  {
    backend: "turboquant",
    dockerfile: "./backend/Dockerfile.turboquant",
    "tag-suffix": "-turboquant",
    "base-image": "ubuntu:24.04",
  },
];

const includesDarwin = [
  { backend: "diffusers", "tag-suffix": "-metal-darwin-arm64-diffusers", "build-type": "mps" },
  { backend: "mlx", "tag-suffix": "-metal-darwin-arm64-mlx", "build-type": "mps" },
  { backend: "whisper", lang: "go", "tag-suffix": "-metal-darwin-arm64-whisper", "build-type": "metal" },
  { backend: "llama-cpp", lang: "go", "tag-suffix": "-metal-darwin-arm64-llama-cpp", "build-type": "metal" },
  { backend: "ds4", lang: "go", "tag-suffix": "-metal-darwin-arm64-ds4", "build-type": "metal" },
];

const run = (changedFiles, previousMatrix) =>
  filterMatrix({ includes, includesDarwin, changedFiles, previousMatrix });

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

// ---------------------------------------------------------------------------
// pkg/ subtrees linked into every Go backend binary
// ---------------------------------------------------------------------------

test("a pkg/ subtree Go backends link rebuilds Go images on both OSes", () => {
  // `go list -deps ./backend/go/...` puts pkg/grpc in every Go backend binary;
  // editing it changes the shipped binary but lives outside every backend
  // directory, so the prefix match never saw it.
  const { filtered, filteredDarwin, changedBackends } = run([
    "pkg/grpc/server.go",
  ]);

  assert.notEqual(filtered.length, 0, "expected a non-empty Linux matrix");
  assert.deepEqual(names(filtered), ["ced"]);
  // Darwin's bespoke builders (llama-cpp, ds4) are C++ and link no Go.
  assert.deepEqual(names(filteredDarwin), ["whisper"]);
  assert.ok(changedBackends.has("ced"));
});

test("pkg/grpc subpackages rebuild Go images too", () => {
  const { filtered, filteredDarwin } = run(["pkg/grpc/base/base.go"]);

  assert.deepEqual(names(filtered), ["ced"]);
  assert.deepEqual(names(filteredDarwin), ["whisper"]);
});

test("every enumerated Go-backend pkg subtree triggers a Go rebuild", () => {
  // Pins the enumeration itself: if a subtree is dropped from the rule but the
  // Go backends still import it, this fails rather than silently shipping to
  // nothing (the #10946 failure mode).
  for (const file of [
    "pkg/audio/convert.go",
    "pkg/grpc/client.go",
    "pkg/httpclient/client.go",
    "pkg/sound/sound.go",
    "pkg/store/client.go",
    "pkg/utils/path.go",
  ]) {
    const { filtered } = run([file]);
    assert.deepEqual(names(filtered), ["ced"], `expected ${file} to rebuild Go images`);
  }
});

test("a pkg/ subtree no Go backend imports rebuilds nothing", () => {
  // The negative pin that keeps this rule honest: including all of pkg/ would
  // fire a 199-entry Go matrix on ~9% of commits. pkg/model and friends are
  // core-server-only — no backend/go/ binary links them.
  for (const file of [
    "pkg/model/loader.go",
    "pkg/xio/reader.go",
    "pkg/downloader/download.go",
    "pkg/functions/parse.go",
    "pkg/vram/vram.go",
  ]) {
    const { filtered, filteredDarwin, changedBackends } = run([file]);
    assert.deepEqual(filtered, [], `${file} must not rebuild Linux images`);
    assert.deepEqual(filteredDarwin, [], `${file} must not rebuild Darwin images`);
    assert.equal(changedBackends.size, 0, `${file} must not mark any backend changed`);
  }
});

test("tests for the linked pkg subtrees do not rebuild anything", () => {
  // _test.go files never reach a backend binary, same reasoning as the
  // scripts/build/*_test.sh exclusion above.
  const { filtered, filteredDarwin } = run(["pkg/grpc/client_busy_test.go"]);

  assert.deepEqual(filtered, []);
  assert.deepEqual(filteredDarwin, []);
});

// ---------------------------------------------------------------------------
// Diff-aware .github/backend-matrix.yml handling
// ---------------------------------------------------------------------------

// A matrix file is only ever compared against a previous revision of itself,
// so build the "before" side by mutating a copy of the fixtures.
const previousOf = (linuxPatch = e => e, darwinPatch = e => e) => ({
  include: includes.map(e => linuxPatch({ ...e })),
  includeDarwin: includesDarwin.map(e => darwinPatch({ ...e })),
});

test("editing an existing entry's base-image rebuilds exactly that entry", () => {
  // Before this, backend-matrix.yml was excluded wholesale: retagging vllm onto
  // a different CUDA base image produced an empty matrix and shipped nothing.
  const previousMatrix = previousOf(e =>
    e.backend === "vllm" ? { ...e, "base-image": "nvidia/cuda:12.8.0-devel-ubuntu24.04" } : e
  );

  const { filtered, filteredDarwin, changedBackends } = run(
    [".github/backend-matrix.yml"],
    previousMatrix
  );

  assert.notEqual(filtered.length, 0, "expected a non-empty Linux matrix");
  assert.deepEqual(names(filtered), ["vllm"]);
  assert.deepEqual(filteredDarwin, []);
  assert.deepEqual([...changedBackends], ["vllm"]);
});

test("editing a non-base-image field of an entry rebuilds it too", () => {
  const previousMatrix = previousOf(e =>
    e.backend === "ced" ? { ...e, "build-type": "cublas" } : e
  );

  const { filtered } = run([".github/backend-matrix.yml"], previousMatrix);

  assert.deepEqual(names(filtered), ["ced"]);
});

test("a brand-new matrix entry for an existing backend rebuilds only itself", () => {
  // Adding a CUDA variant of an already-present backend touches no file under
  // that backend's directory, so its own-directory trigger never fires either.
  const previousMatrix = {
    include: includes.filter(e => e.backend !== "kokoros"),
    includeDarwin: includesDarwin,
  };

  const { filtered, filteredDarwin } = run(
    [".github/backend-matrix.yml"],
    previousMatrix
  );

  assert.deepEqual(names(filtered), ["kokoros"]);
  assert.deepEqual(filteredDarwin, []);
});

test("editing a Darwin entry rebuilds only that Darwin entry", () => {
  const previousMatrix = previousOf(
    e => e,
    e => (e.backend === "mlx" ? { ...e, "build-type": "cpu" } : e)
  );

  const { filtered, filteredDarwin } = run(
    [".github/backend-matrix.yml"],
    previousMatrix
  );

  assert.deepEqual(filtered, []);
  assert.deepEqual(names(filteredDarwin), ["mlx"]);
});

test("a backend-matrix.yml edit that changes no entry rebuilds nothing", () => {
  // The negative pin that makes the diff-aware rule worth having: comment and
  // whitespace edits (and the reordering a formatter does) must stay free. A
  // wholesale include of this file would rebuild all 417 entries here.
  const { filtered, filteredDarwin, changedBackends } = run(
    [".github/backend-matrix.yml"],
    previousOf()
  );

  assert.deepEqual(filtered, []);
  assert.deepEqual(filteredDarwin, []);
  assert.equal(changedBackends.size, 0);
});

test("removing an entry rebuilds nothing", () => {
  const previousMatrix = {
    include: [...includes, {
      backend: "vllm",
      dockerfile: "./backend/Dockerfile.python",
      "tag-suffix": "-nvidia-cuda-11-vllm",
      "base-image": "nvidia/cuda:11.8.0-devel-ubuntu22.04",
    }],
    includeDarwin: includesDarwin,
  };

  const { filtered } = run([".github/backend-matrix.yml"], previousMatrix);

  assert.deepEqual(filtered, []);
});

test("a previous matrix is ignored when backend-matrix.yml did not change", () => {
  // Guards against the diff leaking into unrelated runs: the previous revision
  // is only ever consulted for a changed-file list that names the matrix file.
  const { filtered, filteredDarwin } = run(
    ["README.md"],
    { include: [], includeDarwin: [] }
  );

  assert.deepEqual(filtered, []);
  assert.deepEqual(filteredDarwin, []);
});

test("an unavailable previous matrix conservatively rebuilds everything", () => {
  // Same posture as changed-backends.js's run-all fallbacks: if we cannot
  // resolve what the entries used to be, we must not claim nothing changed.
  const { filtered, filteredDarwin } = run([".github/backend-matrix.yml"], null);

  assert.equal(filtered.length, includes.length);
  assert.equal(filteredDarwin.length, includesDarwin.length);
});
