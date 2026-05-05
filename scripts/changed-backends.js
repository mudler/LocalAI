// Compute the CI build pipeline from backend.yml's matrix:
//   - matrix:           filtered (PR mode) or full (master mode) backend
//                       matrix entries, with base-image-prebuilt annotated
//                       for langs that have a prebuilt base recipe under
//                       .docker/bases/.
//   - matrix-darwin:    same idea for the darwin matrix.
//   - bases-matrix:     deduplicated set of base images needed by the
//                       filtered matrix, in the shape consumed by
//                       .github/workflows/base_images.yml.
//   - has-{backends,backends-darwin,bases}: gating booleans.
//   - <backend>=true/false:  per-backend booleans for test-extra.yml.
//
// On PR events the matrix is filtered to backends whose source dirs
// changed; if .docker/bases/Dockerfile.<lang> (or its workflow scaffolding)
// changed, a canary entry per (lang × build-type × arch × cuda × ubuntu)
// is added so the prebuilt-base path gets exercised end-to-end before
// merge. See .agents/ci-caching.md.

import fs from "fs";
import yaml from "js-yaml";
import { Octokit } from "@octokit/core";

// Backend matrix lives in a sibling data file so the workflow can switch
// to fromJSON without needing two copies of the same matrix. See
// .github/backend-matrix.yaml.
const matrixData = yaml.load(fs.readFileSync(".github/backend-matrix.yaml", "utf8"));
const includes = matrixData.linux;
const includesDarwin = matrixData.darwin;

const eventPath = process.env.GITHUB_EVENT_PATH;
const event = JSON.parse(fs.readFileSync(eventPath, "utf8"));
const isPR = !!event.pull_request;
const prNumber = isPR ? event.pull_request.number : null;

// Langs with a prebuilt base recipe under .docker/bases/Dockerfile.<lang>.
// Discovered at runtime so adding a new language tier (e.g. golang) only
// requires creating that file + slimming the consumer Dockerfile; no
// orchestration changes needed.
const baseRecipeDir = ".docker/bases";
const langsWithBase = new Set(
  fs.existsSync(baseRecipeDir)
    ? fs.readdirSync(baseRecipeDir)
        .filter(f => f.startsWith("Dockerfile."))
        .map(f => f.slice("Dockerfile.".length))
    : []
);

// Files that, when changed in a PR, should fan out to canary backend
// matrix entries for the affected lang. Keeps PR validation honest when a
// PR only touches base scaffolding. Per-lang recipe paths
// (.docker/bases/Dockerfile.<lang>) trigger only their own lang; the
// shared scaffolding entries trigger every lang.
const baseTriggerFiles = new Set([
  ".docker/bases/Dockerfile.python",
  ".docker/bases/Dockerfile.golang",
  ".docker/bases/Dockerfile.cpp",
  ".docker/bases/Dockerfile.rust",
  ".docker/apt-mirror.sh",
  ".github/workflows/base_images.yml",
  ".github/actions/configure-apt-mirror/action.yml",
  "scripts/changed-backends.js",
]);
// Maps a base lang back to the consumer Dockerfiles that build on top of
// it. The cpp base is shared by the llama-cpp / ik-llama-cpp / turboquant
// trio; everything else is 1:1 with the file suffix.
const langTriggerSelector = {
  python: (item) => item.dockerfile && item.dockerfile.endsWith("python"),
  golang: (item) => item.dockerfile && item.dockerfile.endsWith("golang"),
  rust: (item) => item.dockerfile && item.dockerfile.endsWith("rust"),
  cpp: (item) =>
    !!item.dockerfile && /Dockerfile\.(llama-cpp|ik-llama-cpp|turboquant)$/.test(item.dockerfile),
};

// ---------- helpers ----------

function langOf(item) {
  if (!item.dockerfile) return null;
  // dockerfile is like "./backend/Dockerfile.python"
  const m = item.dockerfile.match(/Dockerfile\.([\w-]+)$/);
  if (!m) return null;
  // The C++ trio (llama-cpp, ik-llama-cpp, turboquant) consume a shared
  // cpp base image — they only differ in their per-backend make targets.
  if (m[1] === "llama-cpp" || m[1] === "ik-llama-cpp" || m[1] === "turboquant") {
    return "cpp";
  }
  return m[1];
}

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
  if (!item.lang) {
    return `backend/python/${item.backend}/`;
  }
  return `backend/${item.lang}/${item.backend}/`;
}

function platformsOf(item) {
  // matrix.platforms can be "linux/amd64", "linux/arm64", or
  // "linux/amd64,linux/arm64". Always return a normalized array.
  if (!item.platforms) return ["linux/amd64"];
  return item.platforms.split(",").map(p => p.trim()).filter(Boolean);
}

// Slug a base image reference for inclusion in a tag-stem. Returns "" for
// the default ubuntu:24.04 (which is the implicit BASE_IMAGE) so that case
// keeps a clean stem. Other base images get a short, parseable suffix.
function baseImageSlug(img) {
  if (!img || img === "ubuntu:24.04") return "";
  if (img.includes("l4t-jetpack")) {
    const m = img.match(/r\d+(?:\.\d+)+/);
    return `jetpack-${m ? m[0] : "x"}`;
  }
  if (img.includes("rocm/dev-ubuntu")) {
    const m = img.match(/:([\d.]+)/);
    return `rocm-${m ? m[1] : "x"}`;
  }
  if (img.includes("intel/oneapi-basekit")) {
    const m = img.match(/:([\d.]+)/);
    return `oneapi-${m ? m[1] : "x"}`;
  }
  return img.replace(/.*\//, "").replace(/:/g, "-").replace(/[^A-Za-z0-9.-]/g, "");
}

// Tag stem for the prebuilt base. Arch is intentionally NOT in the stem:
// the base is built multi-arch when any consumer needs multi-arch, and
// single-arch otherwise.
function tagStem(item) {
  const lang = langOf(item);
  if (!lang || !langsWithBase.has(lang)) return null;
  const ubuntu = item["ubuntu-version"] || "2404";
  const buildType = item["build-type"] || "cpu";
  let stem = `${lang}-${buildType}-${ubuntu}`;
  if (buildType === "cublas" || buildType === "l4t") {
    stem += `-cuda${item["cuda-major-version"]}.${item["cuda-minor-version"]}`;
  }
  const slug = baseImageSlug(item["base-image"]);
  if (slug) stem += `-${slug}`;
  return stem;
}

function prebuiltRef(stem) {
  if (!stem) return "";
  const suffix = isPR ? `-pr${prNumber}` : "";
  return `quay.io/go-skynet/localai-base:${stem}${suffix}`;
}

// Build-types that actually exercise the SKIP_DRIVERS branch in the base
// Dockerfile. For everything else (cpu, intel, sycl_*, mps, metal),
// skip-drivers is a no-op and disagreeing values across consumers are
// safe to merge.
const driverBuildTypes = new Set(["vulkan", "cublas", "l4t", "clblas", "hipblas"]);

function effectiveSkipDrivers(item) {
  if (!driverBuildTypes.has(item["build-type"] || "")) return "false";
  return String(item["skip-drivers"] ?? "false");
}

// Build a base entry consumed by base_images.yml. Platforms is the union
// across all consumers of this stem (multi-arch when any consumer needs
// it). runs-on is derived from the platforms: arm-native when arm64 is
// the only arch, ubuntu-latest (with QEMU) otherwise.
function baseEntryFor(stem, items) {
  const first = items[0];
  const platformSet = new Set();
  for (const it of items) for (const p of platformsOf(it)) platformSet.add(p);
  const platforms = [...platformSet].sort().join(",");
  const armOnly = platforms === "linux/arm64";
  return {
    "tag-stem": stem,
    lang: langOf(first),
    "base-image": first["base-image"],
    "build-type": first["build-type"] || "",
    "cuda-major-version": String(first["cuda-major-version"] ?? ""),
    "cuda-minor-version": String(first["cuda-minor-version"] ?? ""),
    "ubuntu-version": String(first["ubuntu-version"] ?? "2404"),
    platforms,
    "runs-on": armOnly ? "ubuntu-24.04-arm" : "ubuntu-latest",
    "skip-drivers": effectiveSkipDrivers(first),
  };
}

function dedupBases(items) {
  // Group consumers by tag-stem.
  const groups = new Map();
  for (const item of items) {
    const stem = tagStem(item);
    if (!stem) continue;
    if (!groups.has(stem)) groups.set(stem, []);
    groups.get(stem).push(item);
  }
  // Inputs that MUST agree across all consumers of a stem. If they don't,
  // the script picks one arbitrarily and the others get a wrong base — fail
  // loudly so the matrix is reconciled.
  const collisionChecks = [
    ["base-image", (it) => it["base-image"]],
    ["skip-drivers", effectiveSkipDrivers],
  ];
  const out = [];
  for (const [stem, consumers] of groups) {
    for (const [name, getter] of collisionChecks) {
      const v0 = getter(consumers[0]);
      for (const c of consumers.slice(1)) {
        const v = getter(c);
        if (v !== v0) {
          throw new Error(
            `Tag-stem collision for ${stem}: ${name} differs ` +
            `(${JSON.stringify(v0)} for ${consumers[0]["tag-suffix"]} vs ` +
            `${JSON.stringify(v)} for ${c["tag-suffix"]}). ` +
            `Disambiguate by encoding ${name} in tagStem(), or reconcile the matrix entries.`,
          );
        }
      }
    }
    out.push(baseEntryFor(stem, consumers));
  }
  return out;
}

// Annotate a backend matrix entry with `base-image-prebuilt` for langs
// with a prebuilt base recipe; leave others untouched (their Dockerfile
// runs the inline bootstrap).
function annotate(item) {
  const stem = tagStem(item);
  if (!stem) return item;
  return { ...item, "base-image-prebuilt": prebuiltRef(stem) };
}

// Build the deduplicated list of backend names → path prefixes from all
// matrix entries (linux + darwin). Used for per-backend boolean outputs
// consumed by test-extra.yml.
function getAllBackendPaths() {
  const paths = new Map();
  for (const item of includes) {
    const p = inferBackendPath(item);
    if (p && !paths.has(item.backend)) paths.set(item.backend, p);
  }
  for (const item of includesDarwin) {
    const p = inferBackendPathDarwin(item);
    if (p && !paths.has(item.backend)) paths.set(item.backend, p);
  }
  return paths;
}

const allBackendPaths = getAllBackendPaths();

function writeOutput(key, value) {
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `${key}=${value}\n`);
}

function emit(filtered, filteredDarwin, runAll) {
  const annotated = filtered.map(annotate);
  const bases = dedupBases(filtered);
  writeOutput("run-all", runAll);
  writeOutput("has-backends", annotated.length > 0 ? "true" : "false");
  writeOutput("has-backends-darwin", filteredDarwin.length > 0 ? "true" : "false");
  writeOutput("has-bases", bases.length > 0 ? "true" : "false");
  writeOutput("matrix", JSON.stringify({ include: annotated }));
  writeOutput("matrix-darwin", JSON.stringify({ include: filteredDarwin }));
  writeOutput("bases-matrix", JSON.stringify({ include: bases }));
}

// ---------- master mode (push events) ----------

if (!isPR) {
  emit(includes, includesDarwin, "true");
  for (const backend of allBackendPaths.keys()) {
    writeOutput(backend, "true");
  }
  process.exit(0);
}

// ---------- PR mode ----------

const repo = event.repository.name;
const owner = event.repository.owner.login;
const octokit = new Octokit({ auth: process.env.GITHUB_TOKEN });

async function getChangedFiles() {
  let files = [];
  let page = 1;
  while (true) {
    const res = await octokit.request("GET /repos/{owner}/{repo}/pulls/{pull_number}/files", {
      owner, repo, pull_number: prNumber, per_page: 100, page,
    });
    files = files.concat(res.data.map(f => f.filename));
    if (res.data.length < 100) break;
    page++;
  }
  return files;
}

(async () => {
  const changedFiles = await getChangedFiles();
  console.log("Changed files:", changedFiles);

  // Source-driven filter: backend dir touched.
  const sourceTriggered = new Set();
  for (const item of includes) {
    const p = inferBackendPath(item);
    if (p && changedFiles.some(f => f.startsWith(p))) {
      sourceTriggered.add(item);
    }
  }

  // Base-driven filter: any matrix entry whose lang has a prebuilt base
  // recipe AND that recipe (or its scaffolding) was touched. We want one
  // canary per (lang × build-type × arch × cuda × ubuntu) so all bases get
  // exercised, not 234 entries.
  const baseTriggered = new Set();
  const baseTriggerHits = new Set(changedFiles.filter(f => baseTriggerFiles.has(f)));
  if (baseTriggerHits.size > 0) {
    const seenStems = new Set();
    for (const item of includes) {
      const stem = tagStem(item);
      if (!stem) continue;
      const select = langTriggerSelector[langOf(item)];
      if (select && !select(item)) continue;
      // Only canary entries for langs whose recipe/scaffolding actually changed.
      const hits = [...baseTriggerHits];
      const recipePath = `.docker/bases/Dockerfile.${langOf(item)}`;
      const langTouched =
        hits.includes(recipePath) ||
        // any non-recipe trigger touches all langs
        hits.some(h => h !== recipePath && !h.startsWith(".docker/bases/Dockerfile."));
      if (!langTouched) continue;
      if (seenStems.has(stem)) continue;
      seenStems.add(stem);
      baseTriggered.add(item);
    }
  }

  const filtered = includes.filter(item => sourceTriggered.has(item) || baseTriggered.has(item));
  const filteredDarwin = includesDarwin.filter(item => {
    const p = inferBackendPathDarwin(item);
    return changedFiles.some(f => f.startsWith(p));
  });

  console.log("Filtered linux:", filtered.length, "(source:", sourceTriggered.size, "base canaries:", baseTriggered.size, ")");
  console.log("Filtered darwin:", filteredDarwin.length);

  emit(filtered, filteredDarwin, "false");

  for (const [backend, pathPrefix] of allBackendPaths) {
    let changed = changedFiles.some(file => file.startsWith(pathPrefix));
    // turboquant reuses backend/cpp/llama-cpp sources via a thin wrapper;
    // changes to either directory should retrigger its pipeline.
    if (backend === "turboquant" && !changed) {
      changed = changedFiles.some(file => file.startsWith("backend/cpp/llama-cpp/"));
    }
    writeOutput(backend, changed ? "true" : "false");
  }
})();
