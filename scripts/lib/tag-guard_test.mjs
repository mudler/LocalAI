// Unit tests for the mutable-tag ordering guard (scripts/lib/tag-guard.mjs).
//
// Run with `make test-ci-scripts` (plain `node --test`, no dependencies), which
// is what .github/workflows/lint.yml runs. The registry and GitHub lookups take
// an injected fetch, so nothing here touches the network.

import test from "node:test";
import assert from "node:assert/strict";

import {
  classifyTags,
  compareCommits,
  decideMutableTag,
  guardTags,
  isImmutableTag,
  parseImageRef,
} from "./tag-guard.mjs";

const QUAY = "quay.io/go-skynet/local-ai-backends";
const HUB = "localai/localai-backends";
const SUFFIX = "-nvidia-l4t-cuda-13-arm64-longcat-video";

// The commits from the incident this guard exists to prevent.
const PRE_FIX = "10211948b5470d332a93c841a9c8fe2b9a737148";
const POST_FIX = "626ae4d51000000000000000000000000000000a";

test("parseImageRef splits a registry-qualified ref", () => {
  assert.deepEqual(parseImageRef(`${QUAY}:master${SUFFIX}`), {
    host: "quay.io",
    repository: "go-skynet/local-ai-backends",
    tag: `master${SUFFIX}`,
    ref: `${QUAY}:master${SUFFIX}`,
  });
});

test("parseImageRef treats a dotless first segment as Docker Hub", () => {
  const parsed = parseImageRef(`${HUB}:latest${SUFFIX}`);
  assert.equal(parsed.host, "docker.io");
  assert.equal(parsed.repository, "localai/localai-backends");
});

test("parseImageRef rejects a ref with no tag", () => {
  assert.throws(() => parseImageRef(QUAY), /no tag/);
});

test("sha- tags are immutable, rolling tags are not", () => {
  assert.equal(isImmutableTag(`${QUAY}:sha-626ae4d${SUFFIX}`), true);
  assert.equal(isImmutableTag(`${QUAY}:sha-626ae4d`), true);
  assert.equal(isImmutableTag(`${QUAY}:master${SUFFIX}`), false);
  assert.equal(isImmutableTag(`${QUAY}:latest${SUFFIX}`), false);
  assert.equal(isImmutableTag(`${QUAY}:v4.7.0${SUFFIX}`), false);
  // A tag that merely starts with the letters "sha" is not a commit tag.
  assert.equal(isImmutableTag(`${QUAY}:shazam${SUFFIX}`), false);
});

test("classifyTags partitions a metadata-action tag list", () => {
  const { immutable, mutable } = classifyTags([
    `${QUAY}:master${SUFFIX}`,
    `${QUAY}:sha-626ae4d${SUFFIX}`,
    `${HUB}:master${SUFFIX}`,
    `${HUB}:sha-626ae4d${SUFFIX}`,
  ]);
  assert.deepEqual(immutable, [
    `${QUAY}:sha-626ae4d${SUFFIX}`,
    `${HUB}:sha-626ae4d${SUFFIX}`,
  ]);
  assert.deepEqual(mutable, [`${QUAY}:master${SUFFIX}`, `${HUB}:master${SUFFIX}`]);
});

test("a descendant commit advances the tag", () => {
  const d = decideMutableTag({
    tag: "master",
    incomingSha: POST_FIX,
    current: { status: "ok", revision: PRE_FIX },
    comparison: { status: "ahead" },
  });
  assert.equal(d.push, true);
  assert.match(d.reason, /advancing the tag/);
});

test("the incident case is blocked: an older commit cannot move the tag back", () => {
  const d = decideMutableTag({
    tag: `master${SUFFIX}`,
    incomingSha: PRE_FIX,
    current: { status: "ok", revision: POST_FIX },
    comparison: { status: "behind" },
  });
  assert.equal(d.push, false);
  assert.equal(d.severity, "warning");
  assert.match(d.reason, /REFUSING to move the tag backwards/);
  // The workaround for the real incident was pinning the sha- tag; the message
  // has to point there.
  assert.match(d.reason, /Pin sha-1021194/);
});

test("unrelated histories are blocked", () => {
  const d = decideMutableTag({
    tag: "master",
    incomingSha: POST_FIX,
    current: { status: "ok", revision: PRE_FIX },
    comparison: { status: "diverged" },
  });
  assert.equal(d.push, false);
  assert.match(d.reason, /no ancestor relationship/);
});

test("a republish of the same commit is allowed", () => {
  const d = decideMutableTag({
    tag: "master",
    incomingSha: POST_FIX,
    current: { status: "ok", revision: POST_FIX },
    comparison: { status: "identical" },
  });
  assert.equal(d.push, true);
  assert.equal(d.severity, "info");
});

test("a tag that does not exist yet is published without a warning", () => {
  const d = decideMutableTag({
    tag: "v4.7.0",
    incomingSha: POST_FIX,
    current: { status: "absent" },
    comparison: { status: "skipped" },
  });
  assert.equal(d.push, true);
  assert.equal(d.severity, "info");
});

test("fail-open paths push but always warn", () => {
  for (const [current, comparison, pattern] of [
    [{ status: "unlabeled" }, { status: "skipped" }, /no org\.opencontainers\.image\.revision label/],
    [{ status: "error", detail: "manifest 503" }, { status: "skipped" }, /manifest 503/],
    [{ status: "ok", revision: PRE_FIX }, { status: "unresolvable" }, /no longer reachable/],
    [{ status: "ok", revision: PRE_FIX }, { status: "error", detail: "compare 502" }, /compare 502/],
  ]) {
    const d = decideMutableTag({
      tag: "master",
      incomingSha: POST_FIX,
      current,
      comparison,
    });
    assert.equal(d.push, true);
    assert.equal(d.severity, "warning");
    assert.match(d.reason, pattern);
  }
});

// --- fetch-backed helpers -------------------------------------------------

function jsonResponse(body, status = 200) {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
  };
}

test("compareCommits short-circuits identical commits without a request", async () => {
  let called = false;
  const res = await compareCommits({
    repository: "mudler/LocalAI",
    base: POST_FIX,
    head: POST_FIX,
    fetchImpl: async () => {
      called = true;
      return jsonResponse({});
    },
  });
  assert.deepEqual(res, { status: "identical" });
  assert.equal(called, false);
});

test("compareCommits maps a 404 to unresolvable", async () => {
  const res = await compareCommits({
    repository: "mudler/LocalAI",
    base: PRE_FIX,
    head: POST_FIX,
    fetchImpl: async () => jsonResponse({}, 404),
  });
  assert.deepEqual(res, { status: "unresolvable" });
});

test("compareCommits surfaces the GitHub status verbatim", async () => {
  const res = await compareCommits({
    repository: "mudler/LocalAI",
    base: POST_FIX,
    head: PRE_FIX,
    fetchImpl: async () => jsonResponse({ status: "behind" }),
  });
  assert.deepEqual(res, { status: "behind" });
});

test("compareCommits turns a thrown fetch into a non-fatal error", async () => {
  const res = await compareCommits({
    repository: "mudler/LocalAI",
    base: PRE_FIX,
    head: POST_FIX,
    fetchImpl: async () => {
      throw new Error("ECONNRESET");
    },
  });
  assert.equal(res.status, "error");
  assert.match(res.detail, /ECONNRESET/);
});

// A fake registry serving one multi-arch tag stamped with `revision`.
function fakeRegistry(revision) {
  return async (url) => {
    if (url.includes("auth.docker.io")) {
      return jsonResponse({ token: "t" });
    }
    if (url.endsWith("/manifests/master-x")) {
      return jsonResponse({ manifests: [{ digest: "sha256:child" }] });
    }
    if (url.endsWith("/manifests/sha256:child")) {
      return jsonResponse({ config: { digest: "sha256:cfg" } });
    }
    if (url.endsWith("/blobs/sha256:cfg")) {
      return jsonResponse({
        config: { Labels: { "org.opencontainers.image.revision": revision } },
      });
    }
    return jsonResponse({}, 404);
  };
}

test("guardTags always keeps immutable tags and drops a backwards mutable tag", async () => {
  const fetchImpl = async (url, init) => {
    if (url.startsWith("https://api.github.com/")) {
      return jsonResponse({ status: "behind" });
    }
    return fakeRegistry(POST_FIX)(url, init);
  };

  const result = await guardTags({
    refs: [`${QUAY}:sha-1021194-x`, `${QUAY}:master-x`],
    incomingSha: PRE_FIX,
    repository: "mudler/LocalAI",
    fetchImpl,
  });

  assert.deepEqual(result.allowed, [`${QUAY}:sha-1021194-x`]);
  assert.deepEqual(result.blocked, [`${QUAY}:master-x`]);
});

test("guardTags advances a mutable tag when the incoming commit is newer", async () => {
  const fetchImpl = async (url, init) => {
    if (url.startsWith("https://api.github.com/")) {
      return jsonResponse({ status: "ahead" });
    }
    return fakeRegistry(PRE_FIX)(url, init);
  };

  const result = await guardTags({
    refs: [`${QUAY}:sha-626ae4d-x`, `${QUAY}:master-x`],
    incomingSha: POST_FIX,
    repository: "mudler/LocalAI",
    fetchImpl,
  });

  assert.deepEqual(result.allowed, [`${QUAY}:sha-626ae4d-x`, `${QUAY}:master-x`]);
  assert.deepEqual(result.blocked, []);
});

test("guardTags publishes an unlabeled tag rather than stalling on it", async () => {
  const fetchImpl = async (url) => {
    if (url.endsWith("/manifests/master-x")) {
      return jsonResponse({ config: { digest: "sha256:cfg" } });
    }
    if (url.endsWith("/blobs/sha256:cfg")) {
      return jsonResponse({ config: {} });
    }
    return jsonResponse({}, 404);
  };

  const result = await guardTags({
    refs: [`${QUAY}:master-x`],
    incomingSha: POST_FIX,
    repository: "mudler/LocalAI",
    fetchImpl,
  });

  assert.deepEqual(result.allowed, [`${QUAY}:master-x`]);
  assert.equal(result.decisions[0].severity, "warning");
});
