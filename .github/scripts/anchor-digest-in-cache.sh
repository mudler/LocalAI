#!/usr/bin/env bash
# Anchor a backend per-arch digest in quay.io/go-skynet/ci-cache so quay's
# garbage collector won't reap the manifest before backend_merge.yml runs.
#
# Context: backend_build.yml pushes by canonical digest only
# (push-by-digest=true). Unreferenced manifests on quay can be reaped within
# ~1-2h, but backend-merge-jobs runs only after the *entire* per-arch build
# matrix drains (max-parallel: 8 × dozens of entries → ~2h+). Without an
# anchoring tag, the earliest digests are gone by the time `imagetools create`
# tries to read them, producing "manifest not found" merge failures.
#
# We tag the digest under our internal ci-cache image; quay does not GC tagged
# manifests. The user-facing manifest list still references the original
# digest in local-ai-backends. backend_merge.yml deletes the anchor tag after
# the user-facing manifest is published — see cleanup-keepalive-tags.sh.
#
# Required env:
#   GITHUB_RUN_ID  - current workflow run id (set automatically by GHA)
#   TAG_SUFFIX     - matrix entry's tag-suffix (e.g. -gpu-nvidia-cuda-12-vllm)
#   PLATFORM_TAG   - amd64 / arm64 / single (single = singleton matrix entry)
#   DIGEST         - canonical content digest from build step (sha256:...)
#
# Optional env:
#   ANCHOR_IMAGE   - target image (default: quay.io/go-skynet/ci-cache)
#   SOURCE_IMAGE   - source image (default: quay.io/go-skynet/local-ai-backends)
#   GITHUB_STEP_SUMMARY - if set, an anchored-by line is appended to it
set -euo pipefail

: "${GITHUB_RUN_ID:?}"
: "${TAG_SUFFIX:?}"
: "${PLATFORM_TAG:?}"
: "${DIGEST:?}"

anchor_image="${ANCHOR_IMAGE:-quay.io/go-skynet/ci-cache}"
source_image="${SOURCE_IMAGE:-quay.io/go-skynet/local-ai-backends}"

tag="keepalive-${GITHUB_RUN_ID}${TAG_SUFFIX}-${PLATFORM_TAG}"

docker buildx imagetools create \
  -t "${anchor_image}:${tag}" \
  "${source_image}@${DIGEST}"

echo "anchored ${DIGEST} as ${anchor_image}:${tag}"
if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  echo "anchored \`${DIGEST}\` as \`${anchor_image}:${tag}\`" >> "${GITHUB_STEP_SUMMARY}"
fi
