#!/usr/bin/env bash
set -euo pipefail

WORKFLOW="$(dirname "$(realpath "$0")")/../../.github/workflows/backend_merge.yml"

sign_commands=$(grep -Ec -- '^[[:space:]]+cosign sign([[:space:]]|$)' "$WORKFLOW" || true)
bundle_flags=$(grep -Ec -- '^[[:space:]]+--new-bundle-format([[:space:]]|$)' "$WORKFLOW" || true)
if [ "$sign_commands" -ne 2 ] || [ "$bundle_flags" -ne "$sign_commands" ]; then
  echo "FAIL: every backend signing command must request the new bundle format (commands=$sign_commands flags=$bundle_flags)"
  exit 1
fi

echo "PASS: backend signing emits Sigstore bundles for both registries"
