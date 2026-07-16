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
  // trellis2cpp is a Go backend (Dockerfile.golang) wrapping the trellis2.cpp
  // ggml port via purego, living in backend/go/trellis2cpp/. Keep the mapping
  // explicit so a future dockerfile-suffix change cannot break path filtering.
  if (item.backend === "trellis2cpp") {
    return `backend/go/trellis2cpp/`;
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

// Dockerfile.golang is the only Linux dockerfile that compiles Go (it runs
// `make protogen-go && make -C backend/go/<backend> build`); every other one
// builds C++, Rust or a Python venv and links no Go code at all.
const isLinuxGo = item => item.dockerfile.endsWith("golang");

const never = () => false;
const always = () => true;

// The pkg/ subtrees that end up inside a Go backend binary. Not a guess:
//
//   go list -deps ./backend/go/... | grep LocalAI/pkg
//
// returns exactly pkg/audio, pkg/grpc (+ base, grpcerrors, proto), pkg/httpclient,
// pkg/sound, pkg/store and pkg/utils — identical for GOOS/GOARCH in
// {linux,darwin} x {amd64,arm64}, so one list covers both matrices. Notably
// absent: pkg/model, pkg/downloader, pkg/functions and the other ~21 pkg
// subtrees, which are core-server-only.
//
// Enumerating rather than taking all of pkg/ is the whole point. All of pkg/
// changes in ~8.6% of commits and would fire a 199-entry Go matrix that often;
// these six subtrees change in 2.0%, which is the same order as the already
// accepted scripts/build/ rule (1.9%). If the enumeration ever drifts from the
// `go list` output the tests pin both directions — a listed subtree must
// trigger, an unlisted one must not.
const GO_BACKEND_PKG_PREFIXES = [
  "pkg/audio/",
  "pkg/grpc/", // covers base/, grpcerrors/ and the generated proto/
  "pkg/httpclient/",
  "pkg/sound/",
  "pkg/store/",
  "pkg/utils/",
];

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
    // Compiled into every Go backend binary (see GO_BACKEND_PKG_PREFIXES).
    // `_test.go` files never reach a binary, same carve-out as the
    // `*_test.sh` one on the scripts/build/ catch-all below.
    //
    // Darwin's bespoke builders (llama-cpp, ds4, privacy-filter) carry
    // lang=go for runner selection only — their sources are C++ and link no
    // Go, so isDarwinGenericGo is the correct predicate here, exactly as it
    // is for scripts/build/golang-darwin.sh.
    matches: file =>
      GO_BACKEND_PKG_PREFIXES.some(prefix => file.startsWith(prefix)) &&
      !file.endsWith("_test.go"),
    linux: isLinuxGo,
    darwin: isDarwinGenericGo,
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

// .github/backend-matrix.yml is a shared build input like the ones above, but
// it cannot be handled by a path rule: every entry lives in it, so matching the
// path would rebuild all 417 Linux entries on every PR that adds a backend —
// and new backends already trigger via their own directory. Excluding it
// wholesale is what the rest of this file does, and that leaves a real hole:
// editing an existing entry's base-image / build-type / cuda version changes
// the image that entry produces while touching no file the prefix match can
// see, so it rebuilt nothing.
//
// The fix needs the file's *previous* contents, which a changed-path list
// cannot carry. changed-backends.js fetches the base revision via the GitHub
// contents API and hands it in as `previousMatrix`; everything below stays
// pure. When the file did not change, `previousMatrix` is never consulted.
export const BACKEND_MATRIX_FILE = ".github/backend-matrix.yml";

// Identity of a matrix entry across revisions. tag-suffix names the image;
// per-arch legs of the same image are distinguished by platform-tag. Verified
// unique across all 417 Linux and 56 Darwin entries.
export function matrixEntryKey(item) {
  return JSON.stringify([item["tag-suffix"] || "", item["platform-tag"] || ""]);
}

// Full field set, order-independent, so a reordered or reindented YAML entry
// compares equal and only a real value change counts.
function entryFingerprint(item) {
  return JSON.stringify(
    Object.keys(item).sort().map(key => [key, item[key]])
  );
}

// Keys of entries that are new or whose fields differ from the previous
// revision. Removed entries produce nothing to build.
function changedEntryKeys(current, previous) {
  const before = new Map();
  for (const item of previous) {
    before.set(matrixEntryKey(item), entryFingerprint(item));
  }
  const keys = new Set();
  for (const item of current) {
    const key = matrixEntryKey(item);
    if (before.get(key) !== entryFingerprint(item)) keys.add(key);
  }
  return keys;
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
export function filterMatrix({
  includes,
  includesDarwin,
  changedFiles,
  previousMatrix,
}) {
  const sharedRules = matchedSharedRules(changedFiles);

  const matrixFileChanged = changedFiles.includes(BACKEND_MATRIX_FILE);
  // The matrix file changed but we could not resolve what it used to say (API
  // failure, shallow history, first push). Claiming "no entry changed" would
  // reintroduce exactly the silent-empty-matrix failure this file exists to
  // prevent, so fall back to rebuilding everything — the same posture
  // changed-backends.js takes for a truncated or unavailable diff.
  const matrixDiffUnavailable = matrixFileChanged && !previousMatrix;

  const changedLinuxKeys =
    matrixFileChanged && previousMatrix
      ? changedEntryKeys(includes, previousMatrix.include || [])
      : null;
  const changedDarwinKeys =
    matrixFileChanged && previousMatrix
      ? changedEntryKeys(includesDarwin, previousMatrix.includeDarwin || [])
      : null;

  const filtered = includes.filter(item => {
    const backendPath = inferBackendPath(item);
    if (!backendPath) return false;
    if (backendChanged(item.backend, backendPath, changedFiles)) return true;
    if (dockerfileChanged(item, changedFiles)) return true;
    if (matrixDiffUnavailable) return true;
    if (changedLinuxKeys && changedLinuxKeys.has(matrixEntryKey(item))) return true;
    return sharedRules.some(rule => rule.linux(item));
  });

  const filteredDarwin = includesDarwin.filter(item => {
    const backendPath = inferBackendPathDarwin(item);
    if (changedFiles.some(file => file.startsWith(backendPath))) return true;
    if (matrixDiffUnavailable) return true;
    if (changedDarwinKeys && changedDarwinKeys.has(matrixEntryKey(item))) return true;
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
