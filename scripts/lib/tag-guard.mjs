// Ordering guard for mutable backend image tags.
//
// Backend images are published to quay.io/go-skynet/local-ai-backends and
// localai/localai-backends under two kinds of tag:
//
//   immutable  sha-<short>-<suffix>          one per commit, never reused
//   mutable    master-<suffix>               moved on every master build
//              latest-<suffix>
//              v<version>-<suffix>
//
// Nothing used to check whether an incoming build was newer than whatever a
// mutable tag already pointed at, so the last writer won. That is not a
// theoretical race: backend CI queues run hours deep (measured ~260 min
// average queue wait against ~19 min average execution) and master pushes get
// a concurrency group keyed by github.sha, so no run supersedes another.
// Completion order does not track commit order. On 19 Jul 2026 a build of a
// commit from 06:45 UTC finished at 15:40 UTC and moved
// master-nvidia-l4t-cuda-13-arm64-longcat-video back onto a pre-fix image,
// silently un-shipping a merged cuDNN packaging fix for two days.
//
// This module decides, per tag, whether a push may proceed. Everything here is
// dependency-free; the registry and GitHub lookups take an injected fetch so
// the decision logic is unit-testable without network access
// (scripts/lib/tag-guard_test.mjs, run by `make test-ci-scripts`).

// docker/metadata-action's `type=sha` emits `sha-<7+ hex>`, and our flavor
// appends the backend tag-suffix. Anything matching this is a per-commit
// artifact: it is how a human pins a known-good build, so it must always push.
const IMMUTABLE_TAG_RE = /^sha-[0-9a-f]{7,40}(?:-|$)/;

const REVISION_LABEL = "org.opencontainers.image.revision";

const MANIFEST_ACCEPT = [
  "application/vnd.oci.image.index.v1+json",
  "application/vnd.docker.distribution.manifest.list.v2+json",
  "application/vnd.oci.image.manifest.v1+json",
  "application/vnd.docker.distribution.manifest.v2+json",
].join(",");

// Split "quay.io/go-skynet/local-ai-backends:master-foo" into its parts.
// Docker Hub refs carry no registry host ("localai/localai-backends:tag"), so
// a first segment without a dot or colon means Docker Hub.
export function parseImageRef(ref) {
  const lastColon = ref.lastIndexOf(":");
  const lastSlash = ref.lastIndexOf("/");
  if (lastColon === -1 || lastColon < lastSlash) {
    throw new Error(`image ref has no tag: ${ref}`);
  }
  const name = ref.slice(0, lastColon);
  const tag = ref.slice(lastColon + 1);

  const segments = name.split("/");
  let host = "docker.io";
  let repository = name;
  if (segments.length > 1 && /[.:]/.test(segments[0])) {
    host = segments[0];
    repository = segments.slice(1).join("/");
  } else if (segments.length === 1) {
    repository = `library/${name}`;
  }
  return { host, repository, tag, ref };
}

export function isImmutableTag(ref) {
  return IMMUTABLE_TAG_RE.test(parseImageRef(ref).tag);
}

// Partition a metadata-action tag list into the tags that always push and the
// tags that need an ordering check.
export function classifyTags(refs) {
  const immutable = [];
  const mutable = [];
  for (const ref of refs) {
    (isImmutableTag(ref) ? immutable : mutable).push(ref);
  }
  return { immutable, mutable };
}

// Decide a single mutable tag.
//
// `current` is the outcome of resolving the tag's present revision:
//   { status: "absent" }                      tag has never been pushed
//   { status: "unlabeled" }                   image carries no revision label
//   { status: "error", detail }               registry lookup failed
//   { status: "ok", revision }                revision is known
//
// `comparison` is the outcome of comparing that revision to the incoming one:
//   { status: "ahead" | "identical" }         incoming descends from current
//   { status: "behind" | "diverged" }         incoming is older or unrelated
//   { status: "unresolvable" }                a commit is no longer in the repo
//   { status: "error", detail }               compare API failed
//
// Fail-open cases (unlabeled, unresolvable, lookup errors) push with a loud
// warning rather than blocking. A guard that fails closed on a registry or API
// blip would itself stop shipping merged fixes, which is the bug we are fixing,
// only louder. Every fail-open path is annotated so it is visible in the run.
export function decideMutableTag({ tag, incomingSha, current, comparison }) {
  const short = (incomingSha || "").slice(0, 7);
  switch (current.status) {
    case "absent":
      return {
        push: true,
        severity: "info",
        reason: `${tag}: tag does not exist yet, publishing ${short}`,
      };
    case "unlabeled":
      return {
        push: true,
        severity: "warning",
        reason:
          `${tag}: current image carries no ${REVISION_LABEL} label, ` +
          `cannot verify ordering, publishing ${short} anyway`,
      };
    case "error":
      return {
        push: true,
        severity: "warning",
        reason:
          `${tag}: could not read the current image (${current.detail}), ` +
          `cannot verify ordering, publishing ${short} anyway`,
      };
  }

  const from = (current.revision || "").slice(0, 7);
  switch (comparison.status) {
    case "identical":
      return {
        push: true,
        severity: "info",
        reason: `${tag}: already at ${short}, republishing the same commit`,
      };
    case "ahead":
      return {
        push: true,
        severity: "info",
        reason: `${tag}: ${short} descends from ${from}, advancing the tag`,
      };
    case "behind":
      return {
        push: false,
        severity: "warning",
        reason:
          `${tag}: REFUSING to move the tag backwards: ${short} is an ` +
          `ancestor of the published ${from}. A newer build already won this ` +
          `race; this straggler would un-ship it. Pin sha-${short} if you ` +
          `need this exact build.`,
      };
    case "diverged":
      return {
        push: false,
        severity: "warning",
        reason:
          `${tag}: REFUSING to move the tag: ${short} and the published ` +
          `${from} have no ancestor relationship. Pin sha-${short} if you ` +
          `need this exact build.`,
      };
    case "unresolvable":
      return {
        push: true,
        severity: "warning",
        reason:
          `${tag}: the published revision ${from} is no longer reachable in ` +
          `the repository, cannot verify ordering, publishing ${short} anyway`,
      };
    default:
      return {
        push: true,
        severity: "warning",
        reason:
          `${tag}: commit comparison failed (${comparison.detail}), ` +
          `cannot verify ordering, publishing ${short} anyway`,
      };
  }
}

// Read the org.opencontainers.image.revision label off whatever a tag
// currently points at. Both repositories are public, so anonymous pull tokens
// are enough and the guard needs no registry credentials of its own.
export async function resolveTagRevision(ref, { fetchImpl = fetch } = {}) {
  const { host, repository, tag } = parseImageRef(ref);
  const registry = host === "docker.io" ? "registry-1.docker.io" : host;
  const base = `https://${registry}/v2/${repository}`;

  let auth = {};
  try {
    if (host === "docker.io") {
      const tokenRes = await fetchImpl(
        `https://auth.docker.io/token?service=registry.docker.io&scope=repository:${repository}:pull`,
      );
      if (!tokenRes.ok) {
        return { status: "error", detail: `docker hub token ${tokenRes.status}` };
      }
      const { token } = await tokenRes.json();
      auth = { Authorization: `Bearer ${token}` };
    }

    const manifestRes = await fetchImpl(`${base}/manifests/${tag}`, {
      headers: { Accept: MANIFEST_ACCEPT, ...auth },
    });
    if (manifestRes.status === 404) {
      return { status: "absent" };
    }
    if (!manifestRes.ok) {
      return { status: "error", detail: `manifest ${manifestRes.status}` };
    }
    let manifest = await manifestRes.json();

    // Multi-arch: every leg of one build is stamped with the same revision, so
    // the first child is representative.
    if (Array.isArray(manifest.manifests)) {
      const child = manifest.manifests.find((m) => m.digest);
      if (!child) {
        return { status: "unlabeled" };
      }
      const childRes = await fetchImpl(`${base}/manifests/${child.digest}`, {
        headers: { Accept: MANIFEST_ACCEPT, ...auth },
      });
      if (!childRes.ok) {
        return { status: "error", detail: `child manifest ${childRes.status}` };
      }
      manifest = await childRes.json();
    }

    const configDigest = manifest.config && manifest.config.digest;
    if (!configDigest) {
      return { status: "unlabeled" };
    }
    const configRes = await fetchImpl(`${base}/blobs/${configDigest}`, {
      headers: auth,
    });
    if (!configRes.ok) {
      return { status: "error", detail: `config blob ${configRes.status}` };
    }
    const config = await configRes.json();
    const labels =
      (config.config && config.config.Labels) ||
      (config.container_config && config.container_config.Labels) ||
      {};
    const revision = labels[REVISION_LABEL];
    return revision ? { status: "ok", revision } : { status: "unlabeled" };
  } catch (err) {
    return { status: "error", detail: String(err && err.message ? err.message : err) };
  }
}

// Ask GitHub how `head` relates to `base`. The compare API answers this
// directly, which keeps the guard working in the shallow sparse checkouts the
// merge and publish jobs use. `git merge-base --is-ancestor` would need the
// full history fetched into every one of the ~200 merge jobs per push.
export async function compareCommits({
  repository,
  base,
  head,
  token,
  fetchImpl = fetch,
}) {
  if (base === head) {
    return { status: "identical" };
  }
  try {
    const headers = {
      Accept: "application/vnd.github+json",
      "X-GitHub-Api-Version": "2022-11-28",
    };
    if (token) {
      headers.Authorization = `Bearer ${token}`;
    }
    const res = await fetchImpl(
      `https://api.github.com/repos/${repository}/compare/${base}...${head}`,
      { headers },
    );
    if (res.status === 404) {
      return { status: "unresolvable" };
    }
    if (!res.ok) {
      return { status: "error", detail: `compare ${res.status}` };
    }
    const body = await res.json();
    // GitHub reports ahead/behind/identical/diverged. Unrelated histories in
    // the same repository come back as "diverged" with no merge base.
    const known = ["ahead", "behind", "identical", "diverged"];
    return known.includes(body.status)
      ? { status: body.status }
      : { status: "error", detail: `unexpected compare status ${body.status}` };
  } catch (err) {
    return { status: "error", detail: String(err && err.message ? err.message : err) };
  }
}

// Full pass over a metadata-action tag list. Returns the tags that may be
// pushed plus a decision log for the job summary.
export async function guardTags({
  refs,
  incomingSha,
  repository,
  token,
  fetchImpl = fetch,
}) {
  const { immutable, mutable } = classifyTags(refs);
  const decisions = immutable.map((tag) => ({
    tag,
    push: true,
    severity: "info",
    reason: `${tag}: immutable per-commit tag, always published`,
  }));

  for (const tag of mutable) {
    const current = await resolveTagRevision(tag, { fetchImpl });
    let comparison = { status: "skipped" };
    if (current.status === "ok") {
      comparison = await compareCommits({
        repository,
        base: current.revision,
        head: incomingSha,
        token,
        fetchImpl,
      });
    }
    decisions.push({
      tag,
      ...decideMutableTag({ tag, incomingSha, current, comparison }),
    });
  }

  return {
    allowed: decisions.filter((d) => d.push).map((d) => d.tag),
    blocked: decisions.filter((d) => !d.push).map((d) => d.tag),
    decisions,
  };
}
