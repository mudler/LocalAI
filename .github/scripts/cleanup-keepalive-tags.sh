#!/usr/bin/env bash
# Best-effort cleanup of the keepalive anchor tags written by
# anchor-digest-in-cache.sh. Called from backend_merge.yml after the
# user-facing manifest list has been published.
#
# Quay's docker registry v2 doesn't allow tag deletes — only digest deletes.
# The proper delete is the quay REST API, which requires an OAuth-scoped
# token. We try QUAY_TOKEN as a bearer token: if the secret is an OAuth app
# token (typical for service accounts) the delete succeeds; otherwise this
# is a soft no-op and the tag persists until manually pruned.
#
# Cleanup failure MUST NOT fail the merge — the merge has already produced
# the user-facing manifest list at this point and the keepalive tags are
# pure overhead. We always exit 0.
#
# Required env:
#   GITHUB_RUN_ID  - current workflow run id (set automatically by GHA)
#   TAG_SUFFIX     - matrix entry's tag-suffix (e.g. -gpu-nvidia-cuda-12-vllm)
#   QUAY_TOKEN     - bearer token for quay's REST API
#
# Optional env:
#   QUAY_REPO      - target repo (default: go-skynet/ci-cache)
#   PLATFORM_TAGS  - space-separated list of platform-tag values to try
#                    (default: "amd64 arm64 single")
#                    We don't know which platform-tag(s) exist for this
#                    tag-suffix without an extra API call, so we just try
#                    all three and ignore 404s for the ones that don't.
set -uo pipefail

: "${GITHUB_RUN_ID:?}"
: "${TAG_SUFFIX:?}"
: "${QUAY_TOKEN:?}"

quay_repo="${QUAY_REPO:-go-skynet/ci-cache}"
platform_tags="${PLATFORM_TAGS:-amd64 arm64 single}"

for plat in $platform_tags; do
  tag="keepalive-${GITHUB_RUN_ID}${TAG_SUFFIX}-${plat}"
  url="https://quay.io/api/v1/repository/${quay_repo}/tag/${tag}"
  http=$(curl -sS -o /dev/null -w '%{http_code}' \
    -X DELETE -H "Authorization: Bearer ${QUAY_TOKEN}" "$url" || echo "000")
  case "$http" in
    204|200) echo "deleted $tag" ;;
    404)     echo "not present: $tag" ;;
    401|403) echo "auth not OAuth-scoped (http $http) for $tag - skipping; orphan tag will persist" ;;
    *)       echo "unexpected http $http deleting $tag - skipping" ;;
  esac
done
exit 0
