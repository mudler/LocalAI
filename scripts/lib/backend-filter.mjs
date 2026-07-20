// Pure matrix-filtering logic for scripts/changed-backends.js.
//
// Split out of that script so it can be unit-tested: changed-backends.js reads
// GITHUB_EVENT_PATH and talks to the GitHub API at import time, and pulls in
// js-yaml + @octokit/core, none of which a test of "which backends does this
// diff invalidate?" should need. Everything here is dependency-free and
// side-effect-free.

// Infer backend path
export function inferBackendPath(item) {
  if (item.dockerfile.endsWith("python")) {
    return `backend/python/${item.backend}/`;
  }
  // parakeet-cpp is a Go backend (Dockerfile.golang) wrapping the parakeet.cpp
  // ggml port via purego. It lives in backend/go/parakeet-cpp/; this explicit
  // branch (placed before the generic golang one, which would also resolve it
  // correctly) documents the mapping and guards against a future
  // dockerfile-suffix change.
  if (item.backend === "parakeet-cpp") {
    return `backend/go/parakeet-cpp/`;
  }
  // ced is a Go backend (Dockerfile.golang) wrapping the ced.cpp ggml port via
  // purego, living in backend/go/ced/. Same explicit-branch rationale as
  // parakeet-cpp above: the generic golang fallthrough would also resolve it,
  // but this documents the mapping and guards a future dockerfile-suffix change.
  if (item.backend === "ced") {
    return `backend/go/ced/`;
  }
  // moss-transcribe-cpp is a Go backend (Dockerfile.golang) wrapping the
  // moss-transcribe.cpp ggml port via purego, living in
  // backend/go/moss-transcribe-cpp/. Same explicit-branch rationale as
  // parakeet-cpp / ced: the generic golang fallthrough would also resolve it,
  // but this documents the mapping and guards a future dockerfile-suffix change.
  if (item.backend === "moss-transcribe-cpp") {
    return `backend/go/moss-transcribe-cpp/`;
  }
  // moss-tts-cpp is a Go backend (Dockerfile.golang) wrapping the moss-tts.cpp
  // ggml port via purego, living in backend/go/moss-tts-cpp/. Same
  // explicit-branch rationale as parakeet-cpp / ced / moss-transcribe-cpp: the
  // generic golang fallthrough would also resolve it, but this documents the
  // mapping and guards a future dockerfile-suffix change.
  if (item.backend === "moss-tts-cpp") {
    return `backend/go/moss-tts-cpp/`;
  }
  if (item.dockerfile.endsWith("golang")) {
    return `backend/go/${item.backend}/`;
  }
  if (item.dockerfile.endsWith("rust")) {
    return `backend/rust/${item.backend}/`;
  }
  if (item.dockerfile.endsWith("ik-llama-cpp")) {
    return `backend/cpp/ik-llama-cpp/`;
  }
  if (item.dockerfile.endsWith("turboquant")) {
    // turboquant is a llama.cpp fork that reuses backend/cpp/llama-cpp sources
    // via a thin wrapper Makefile. Changes to either dir should retrigger it.
    return `backend/cpp/turboquant/`;
  }
  if (item.dockerfile.endsWith("bonsai")) {
    // bonsai is a llama.cpp fork that reuses backend/cpp/llama-cpp sources
    // via a thin wrapper Makefile. Changes to either dir should retrigger it.
    return `backend/cpp/bonsai/`;
  }
  if (item.dockerfile.endsWith("privacy-filter")) {
    return `backend/cpp/privacy-filter/`;
  }
  if (item.dockerfile.endsWith("ds4")) {
    return `backend/cpp/ds4/`;
  }
  if (item.dockerfile.endsWith("llama-cpp")) {
    return `backend/cpp/llama-cpp/`;
  }
  return null;
}

export function inferBackendPathDarwin(item) {
  // llama-cpp on Darwin builds from the C++ sources, not a backend/go/llama-cpp
  // tree (which doesn't exist). The Darwin job is matrix-driven with lang=go
  // for runner/toolchain selection, but the source path is C++.
  if (item.backend === "llama-cpp") {
    return `backend/cpp/llama-cpp/`;
  }
  // ds4 is C++ too (built via `make backends/ds4-darwin`); the matrix entry
  // carries lang=go for runner/toolchain selection, but the source is C++.
  if (item.backend === "ds4") {
    return `backend/cpp/ds4/`;
  }
  // privacy-filter is C++ too (built via `make backends/privacy-filter-darwin`);
  // same lang=go-for-runner convention, source under backend/cpp.
  if (item.backend === "privacy-filter") {
    return `backend/cpp/privacy-filter/`;
  }
  if (!item.lang) {
    return `backend/python/${item.backend}/`;
  }

  return `backend/${item.lang}/${item.backend}/`;
}

// Build a deduplicated map of backend name -> path prefix from all matrix entries
export function getAllBackendPaths(includes, includesDarwin) {
  const paths = new Map();
  for (const item of includes) {
    const p = inferBackendPath(item);
    if (p && !paths.has(item.backend)) {
      paths.set(item.backend, p);
    }
  }
  for (const item of includesDarwin) {
    const p = inferBackendPathDarwin(item);
    if (p && !paths.has(item.backend)) {
      paths.set(item.backend, p);
    }
  }
  return paths;
}

export function backendChanged(backend, pathPrefix, changedFiles) {
  if (changedFiles.some(file => file.startsWith(pathPrefix))) return true;

  // Fork backends reuse backend/cpp/llama-cpp sources via thin wrappers;
  // changes to either directory must retrigger their pipelines.
  return (backend === "turboquant" || backend === "bonsai") &&
    changedFiles.some(file => file.startsWith("backend/cpp/llama-cpp/"));
}

// Darwin entries carry `lang` only for runner/toolchain selection; an entry
// without it is a Python backend (see .github/backend-matrix.yml).
const isDarwinPython = item => !item.lang;

// backend_build_darwin.yml routes llama-cpp, ds4 and privacy-filter to their
// own bespoke make targets; every other lang=go entry goes through
// `make build-darwin-go-backend` -> scripts/build/golang-darwin.sh.
const DARWIN_BESPOKE_BUILDERS = new Set(["llama-cpp", "ds4", "privacy-filter"]);
const isDarwinGenericGo = item =>
  !!item.lang && !DARWIN_BESPOKE_BUILDERS.has(item.backend);

const isLinuxPython = item => item.dockerfile.endsWith("python");

const never = () => false;
const always = () => true;

// Shared build inputs: files that end up in, or decide the contents of, images
// belonging to backends whose own directory they do not live under. The
// per-backend prefix match in filterMatrix() structurally cannot see these, so
// before this table a change to any of them rebuilt *nothing at all*.
//
// That is not hypothetical. PR #10946 fixed scripts/build/package-gpu-libs.sh
// shipping a partial 4-of-8 cuDNN library set (which mixed versions with the
// venv's pip cuDNN and produced CUDNN_STATUS_SUBLIBRARY_VERSION_MISMATCH at
// inference time). It merged 1h48m after the weekly full-matrix cron had
// already run, so no backend image ever received the fix and nothing signalled
// that it had been un-shipped.
//
// Each rule names the narrowest set of matrix entries it can honestly
// invalidate, because a full backend matrix is a large CI spend. Rules are
// first-match-wins per changed file, so specific entries must precede the
// scripts/build/ catch-all at the bottom.
export const SHARED_BUILD_INPUTS = [
  {
    // Every language consumes backend.proto: Dockerfile.python COPYs it, Go
    // backends regenerate their stubs from it via `make protogen-go`, the C++
    // CMakeLists compile backend.pb.cc from it, and the Rust crate Makefile
    // copies it in before build.rs runs.
    matches: file => file === "backend/backend.proto",
    linux: always,
    darwin: always,
  },
  {
    // COPY'd into every Python image (Dockerfile.python) and into every Darwin
    // Python build (scripts/build/python-darwin.sh).
    matches: file => file.startsWith("backend/python/common/"),
    linux: isLinuxPython,
    darwin: isDarwinPython,
  },
  {
    // The reusable build workflows own build-args, packaging and push for
    // their whole OS family, so a change there can alter what lands in every
    // image that family builds.
    matches: file => file === ".github/workflows/backend_build.yml",
    linux: always,
    darwin: never,
  },
  {
    matches: file => file === ".github/workflows/backend_build_darwin.yml",
    linux: never,
    darwin: always,
  },
  {
    // Stages the CUDA/ROCm runtime libraries into every Python image's lib/.
    // COPY'd and run by Dockerfile.python only. This is the #10946 case.
    matches: file => file === "scripts/build/package-gpu-libs.sh",
    linux: isLinuxPython,
    darwin: never,
  },
  {
    matches: file => file === "scripts/build/python-darwin.sh",
    linux: never,
    darwin: isDarwinPython,
  },
  {
    matches: file => file === "scripts/build/golang-darwin.sh",
    linux: never,
    darwin: isDarwinGenericGo,
  },
  {
    matches: file => file === "scripts/build/llama-cpp-darwin.sh",
    linux: never,
    darwin: item => item.backend === "llama-cpp",
  },
  {
    matches: file => file === "scripts/build/ds4-darwin.sh",
    linux: never,
    darwin: item => item.backend === "ds4",
  },
  {
    matches: file => file === "scripts/build/privacy-filter-darwin.sh",
    linux: never,
    darwin: item => item.backend === "privacy-filter",
  },
  {
    // Catch-all, deliberately last and deliberately broad: anything else under
    // scripts/build/ is an unclassified image-packaging input. Rebuild
    // everything until someone narrows it with a rule above. Erring toward an
    // expensive full matrix beats the failure mode that hid #10946 — a new
    // shared script silently shipping to nothing. `*_test.sh` are the tests
    // for those scripts and never reach an image.
    matches: file =>
      file.startsWith("scripts/build/") && !file.endsWith("_test.sh"),
    linux: always,
    darwin: always,
  },
];

// The matrix stores dockerfiles as "./backend/Dockerfile.python"; changed-file
// lists are repo-relative.
function dockerfilePath(item) {
  return (item.dockerfile || "").replace(/^\.\//, "");
}

// A backend/Dockerfile.<x> edit invalidates exactly the Linux entries built
// from it. These files sit outside every backend directory, so the prefix match
// never sees them — same blind spot as SHARED_BUILD_INPUTS, but expressible
// precisely because each entry already names its dockerfile.
function dockerfileChanged(item, changedFiles) {
  const df = dockerfilePath(item);
  return df !== "" && changedFiles.includes(df);
}

function matchedSharedRules(changedFiles) {
  const rules = [];
  for (const file of changedFiles) {
    const rule = SHARED_BUILD_INPUTS.find(r => r.matches(file));
    if (rule && !rules.includes(rule)) rules.push(rule);
  }
  return rules;
}

// Filter both matrices against a changed-file list. Returns the surviving
// entries plus the set of backend names considered changed, which drives the
// per-backend boolean outputs consumed by test-extra.yml.
export function filterMatrix({ includes, includesDarwin, changedFiles }) {
  const sharedRules = matchedSharedRules(changedFiles);

  const filtered = includes.filter(item => {
    const backendPath = inferBackendPath(item);
    if (!backendPath) return false;
    if (backendChanged(item.backend, backendPath, changedFiles)) return true;
    if (dockerfileChanged(item, changedFiles)) return true;
    return sharedRules.some(rule => rule.linux(item));
  });

  const filteredDarwin = includesDarwin.filter(item => {
    const backendPath = inferBackendPathDarwin(item);
    if (changedFiles.some(file => file.startsWith(backendPath))) return true;
    return sharedRules.some(rule => rule.darwin(item));
  });

  const changedBackends = new Set();
  for (const item of filtered) changedBackends.add(item.backend);
  for (const item of filteredDarwin) changedBackends.add(item.backend);
  for (const [backend, pathPrefix] of getAllBackendPaths(includes, includesDarwin)) {
    if (backendChanged(backend, pathPrefix, changedFiles)) changedBackends.add(backend);
  }

  return { filtered, filteredDarwin, changedBackends };
}
