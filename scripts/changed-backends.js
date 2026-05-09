import fs from "fs";
import yaml from "js-yaml";
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

// Group filtered linux matrix entries by tag-suffix and emit a merge-matrix
// entry for any tag-suffix that appears 2+ times. That's the trigger for
// "this backend has multiple per-arch legs and we need a manifest list".
// Singletons aren't merged — single-arch backends push by digest and don't
// need a manifest list assembled across legs.
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
    if (group.length < 2) continue;
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

function emitFullMatrix() {
  const mergeMatrix = computeMergeMatrix(includes);
  const hasMerges = mergeMatrix.include.length > 0 ? 'true' : 'false';
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `run-all=true\n`);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `has-backends=true\n`);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `has-backends-darwin=true\n`);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `has-merges=${hasMerges}\n`);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `matrix=${JSON.stringify({ include: includes })}\n`);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `matrix-darwin=${JSON.stringify({ include: includesDarwin })}\n`);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `merge-matrix=${JSON.stringify(mergeMatrix)}\n`);
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

  const hasBackends = filtered.length > 0 ? 'true' : 'false';
  const hasBackendsDarwin = filteredDarwin.length > 0 ? 'true' : 'false';
  console.log("Has backends?:", hasBackends);
  console.log("Has Darwin backends?:", hasBackendsDarwin);

  const mergeMatrix = computeMergeMatrix(filtered);
  const hasMerges = mergeMatrix.include.length > 0 ? 'true' : 'false';

  fs.appendFileSync(process.env.GITHUB_OUTPUT, `run-all=false\n`);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `has-backends=${hasBackends}\n`);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `has-backends-darwin=${hasBackendsDarwin}\n`);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `has-merges=${hasMerges}\n`);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `matrix=${JSON.stringify({ include: filtered })}\n`);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `matrix-darwin=${JSON.stringify({ include: filteredDarwin })}\n`);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `merge-matrix=${JSON.stringify(mergeMatrix)}\n`);

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
