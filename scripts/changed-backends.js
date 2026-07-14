import fs from "fs";
import * as yaml from "js-yaml";
import { Octokit } from "@octokit/core";

// Matrix data lives in a small data-only YAML so both backend.yml (master push)
// and backend_pr.yml (pull_request) can use a dynamic `matrix: ${{ fromJson(...) }}`
// for the live job, while this script remains the single source of truth for
// "what backends does the project know about".
const matrixYml = yaml.load(fs.readFileSync(".github/backend-matrix.yml", "utf8"));
const includes = matrixYml.include;
const includesDarwin = matrixYml.includeDarwin;

const eventPath = process.env.GITHUB_EVENT_PATH;
const event = JSON.parse(fs.readFileSync(eventPath, "utf8"));

// Infer backend path
function inferBackendPath(item) {
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

function inferBackendPathDarwin(item) {
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
function getAllBackendPaths() {
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

const allBackendPaths = getAllBackendPaths();

const token = process.env.GITHUB_TOKEN;
const octokit = new Octokit({ auth: token });

// PR file list — paginated.
async function getChangedFilesForPR(event) {
  const prNumber = event.pull_request.number;
  const repo = event.repository.name;
  const owner = event.repository.owner.login;
  let files = [];
  let page = 1;
  while (true) {
    const res = await octokit.request('GET /repos/{owner}/{repo}/pulls/{pull_number}/files', {
      owner,
      repo,
      pull_number: prNumber,
      per_page: 100,
      page
    });
    files = files.concat(res.data.map(f => f.filename));
    if (res.data.length < 100) break;
    page++;
  }
  return files;
}

// Branch-push file list — uses the Compare API so it works in shallow clones.
// Returns null to signal "we cannot compute a reliable diff; run everything".
async function getChangedFilesForPush(event) {
  const before = event.before;
  const after = event.after;
  // First push to a branch carries an all-zero `before` SHA and there's no
  // base to diff against. Run everything in that case.
  if (!before || !after || /^0+$/.test(before)) return null;
  const owner = event.repository.owner.login;
  const repo = event.repository.name;
  let res;
  try {
    res = await octokit.request('GET /repos/{owner}/{repo}/compare/{basehead}', {
      owner,
      repo,
      basehead: `${before}...${after}`,
    });
  } catch (err) {
    console.log("compare API failed, falling back to run-all:", err.message);
    return null;
  }
  if (!res.data || !Array.isArray(res.data.files)) return null;
  // The compare endpoint caps the file list at 300. If we hit the cap we may
  // be missing changes — be conservative and run everything.
  if (res.data.files.length >= 300) {
    console.log("compare API returned 300+ files (truncated), falling back to run-all");
    return null;
  }
  return res.data.files.map(f => f.filename);
}

// Group matrix entries by tag-suffix and emit a merge-matrix entry per group.
// Both multi-leg groups (per-arch fan-out) and singletons get one entry each:
// the build job pushes by digest only with no tags applied, so every backend
// needs a downstream merge step to apply its tags via `imagetools create`,
// regardless of how many per-arch legs feed it. Callers split entries by
// arch class first (see splitByArch) and call this once per class so the
// resulting matrices can be wired to merge jobs that `needs:` only their
// corresponding build matrix — preventing slow single-arch builds from
// gating multi-arch merges (the bug fixed in PR #9746).
function computeMergeMatrix(entries) {
  const groups = new Map();
  for (const item of entries) {
    if (!item['tag-suffix']) continue;
    const key = item['tag-suffix'];
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push(item);
  }
  const include = [];
  for (const [tagSuffix, group] of groups) {
    // tag-latest must agree across legs — they're going to publish under
    // the same final tag, so disagreeing on whether it's also the :latest
    // tag is an authoring bug. Warn loudly so a Task 2.5 fan-out typo is
    // visible in CI logs instead of silently shipping the leg-0 value.
    const first = group[0]['tag-latest'] || '';
    for (const m of group) {
      if ((m['tag-latest'] || '') !== first) {
        console.warn(`tag-latest mismatch in group ${tagSuffix}: legs disagree (using ${first})`);
        break;
      }
    }
    include.push({
      'tag-suffix': tagSuffix,
      'tag-latest': first,
    });
  }
  return { include };
}

// Split a list of linux matrix entries into single-arch (no platform-tag) and
// multi-arch (platform-tag set, paired with a sibling entry sharing the same
// tag-suffix). The two are run as separate matrix jobs so backend-merge-jobs
// can `needs:` only the multi-arch one — slow single-arch builds (CUDA, ROCm,
// vLLM, etc.) don't block manifest assembly while their per-arch counterparts'
// untagged digests sit on quay long enough to be GC'd.
function splitByArch(entries) {
  const multiarch = entries.filter(e => e['platform-tag']);
  const singlearch = entries.filter(e => !e['platform-tag']);
  return { multiarch, singlearch };
}

// GitHub Actions refuses to instantiate a matrix with more than 256 jobs. When
// it happens the job doesn't error visibly — it hangs forever at "Waiting for
// pending jobs" and the whole run is marked `failure` while every *other* job
// stays green (seen on the v4.6.1 tag build, run 28786533892: 268 single-arch
// entries, zero single-arch jobs ever created). The single-arch list is the
// one that grows unbounded as backends are added, so we shard it across a
// fixed number of matrix jobs instead of feeding one oversized matrix.
//
// SINGLEARCH_SHARDS MUST equal the number of backend-jobs-singlearch-<n>
// (and backend-merge-jobs-singlearch-<n>) blocks defined in backend.yml and
// backend_pr.yml. Bump all three together.
const SINGLEARCH_SHARDS = 4;
const GHA_MATRIX_LIMIT = 256;

// Split `arr` into exactly `shards` balanced, contiguous chunks. Earlier chunks
// absorb the remainder when the length doesn't divide evenly; trailing chunks
// may be empty when there are fewer entries than shards (those emit a
// has-backends-singlearch-<n>=false flag so their job is skipped).
function chunkEqually(arr, shards) {
  const out = [];
  const base = Math.floor(arr.length / shards);
  const rem = arr.length % shards;
  let idx = 0;
  for (let i = 0; i < shards; i++) {
    const size = base + (i < rem ? 1 : 0);
    out.push(arr.slice(idx, idx + size));
    idx += size;
  }
  return out;
}

// Emit the sharded single-arch build + merge matrices and their has-* gates.
// Called with the full or filtered single-arch entry list.
function emitSinglearchShards(singlearch) {
  const shards = chunkEqually(singlearch, SINGLEARCH_SHARDS);
  for (let i = 0; i < SINGLEARCH_SHARDS; i++) {
    const shard = shards[i];
    // Fail loudly rather than let GitHub silently drop the overflow: a shard at
    // or above the limit means SINGLEARCH_SHARDS (and the matching job blocks in
    // both workflows) need to grow.
    if (shard.length >= GHA_MATRIX_LIMIT) {
      throw new Error(
        `single-arch shard ${i + 1} has ${shard.length} entries (>= ${GHA_MATRIX_LIMIT}, ` +
        `GitHub's per-matrix job limit). Increase SINGLEARCH_SHARDS in ` +
        `scripts/changed-backends.js and add matching backend-jobs-singlearch-<n> / ` +
        `backend-merge-jobs-singlearch-<n> blocks to backend.yml and backend_pr.yml.`
      );
    }
    const merge = computeMergeMatrix(shard);
    const n = i + 1;
    fs.appendFileSync(process.env.GITHUB_OUTPUT, `has-backends-singlearch-${n}=${shard.length > 0 ? 'true' : 'false'}\n`);
    fs.appendFileSync(process.env.GITHUB_OUTPUT, `has-merges-singlearch-${n}=${merge.include.length > 0 ? 'true' : 'false'}\n`);
    fs.appendFileSync(process.env.GITHUB_OUTPUT, `matrix-singlearch-${n}=${JSON.stringify({ include: shard })}\n`);
    fs.appendFileSync(process.env.GITHUB_OUTPUT, `merge-matrix-singlearch-${n}=${JSON.stringify(merge)}\n`);
  }
}

function emitFullMatrix() {
  const { multiarch, singlearch } = splitByArch(includes);
  const mergeMatrixMultiarch = computeMergeMatrix(multiarch);
  const hasMergesMultiarch = mergeMatrixMultiarch.include.length > 0 ? 'true' : 'false';
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `run-all=true\n`);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `has-backends-multiarch=${multiarch.length > 0 ? 'true' : 'false'}\n`);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `has-backends-darwin=true\n`);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `has-merges-multiarch=${hasMergesMultiarch}\n`);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `matrix-multiarch=${JSON.stringify({ include: multiarch })}\n`);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `matrix-darwin=${JSON.stringify({ include: includesDarwin })}\n`);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `merge-matrix-multiarch=${JSON.stringify(mergeMatrixMultiarch)}\n`);
  emitSinglearchShards(singlearch);
  for (const backend of allBackendPaths.keys()) {
    fs.appendFileSync(process.env.GITHUB_OUTPUT, `${backend}=true\n`);
  }
}

function emitFilteredMatrix(changedFiles) {
  console.log("Changed files:", changedFiles);

  const filtered = includes.filter(item => {
    const backendPath = inferBackendPath(item);
    if (!backendPath) return false;
    return changedFiles.some(file => file.startsWith(backendPath));
  });

  const filteredDarwin = includesDarwin.filter(item => {
    const backendPath = inferBackendPathDarwin(item);
    return changedFiles.some(file => file.startsWith(backendPath));
  });

  console.log("Filtered files:", filtered);
  console.log("Filtered files Darwin:", filteredDarwin);

  const { multiarch, singlearch } = splitByArch(filtered);
  const hasBackendsMultiarch = multiarch.length > 0 ? 'true' : 'false';
  const hasBackendsDarwin = filteredDarwin.length > 0 ? 'true' : 'false';
  console.log("Has single-arch backends?:", singlearch.length > 0 ? 'true' : 'false');
  console.log("Has multi-arch backends?:", hasBackendsMultiarch);
  console.log("Has Darwin backends?:", hasBackendsDarwin);

  const mergeMatrixMultiarch = computeMergeMatrix(multiarch);
  const hasMergesMultiarch = mergeMatrixMultiarch.include.length > 0 ? 'true' : 'false';

  fs.appendFileSync(process.env.GITHUB_OUTPUT, `run-all=false\n`);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `has-backends-multiarch=${hasBackendsMultiarch}\n`);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `has-backends-darwin=${hasBackendsDarwin}\n`);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `has-merges-multiarch=${hasMergesMultiarch}\n`);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `matrix-multiarch=${JSON.stringify({ include: multiarch })}\n`);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `matrix-darwin=${JSON.stringify({ include: filteredDarwin })}\n`);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `merge-matrix-multiarch=${JSON.stringify(mergeMatrixMultiarch)}\n`);
  emitSinglearchShards(singlearch);

  // Per-backend boolean outputs
  for (const [backend, pathPrefix] of allBackendPaths) {
    let changed = changedFiles.some(file => file.startsWith(pathPrefix));
    // turboquant reuses backend/cpp/llama-cpp sources via a thin wrapper;
    // changes to either directory should retrigger its pipeline.
    if (backend === "turboquant" && !changed) {
      changed = changedFiles.some(file => file.startsWith("backend/cpp/llama-cpp/"));
    }
    fs.appendFileSync(process.env.GITHUB_OUTPUT, `${backend}=${changed ? 'true' : 'false'}\n`);
  }
}

(async () => {
  // Tag pushes and an explicit FORCE_ALL escape hatch always rebuild everything.
  // FORCE_ALL is set from backend.yml whenever github.ref starts with refs/tags/.
  const forceAll = process.env.FORCE_ALL === 'true';
  const isTagPush = typeof event.ref === 'string' && event.ref.startsWith('refs/tags/');
  const isBranchPush = !!event.ref && !event.pull_request && !isTagPush;

  let changedFiles = null;
  if (event.pull_request) {
    changedFiles = await getChangedFilesForPR(event);
  } else if (isBranchPush && !forceAll) {
    changedFiles = await getChangedFilesForPush(event);
    // null -> fall through to the full matrix (e.g. first push, API truncated,
    // network failure).
  }
  // All other event types (workflow_dispatch, schedule, tag pushes, FORCE_ALL)
  // leave changedFiles === null and run everything.

  if (changedFiles === null) {
    emitFullMatrix();
    return;
  }
  emitFilteredMatrix(changedFiles);
})();
