#!/usr/bin/env bash
set -euo pipefail

WORKFLOW="$(dirname "$(realpath "$0")")/../../.github/workflows/backend_merge.yml"

sign_commands=$(grep -Ec -- '^[[:space:]]+cosign sign([[:space:]]|$)' "$WORKFLOW" || true)
recursive_flags=$(grep -Ec -- '^[[:space:]]+cosign sign .*--recursive([[:space:]]|$)' "$WORKFLOW" || true)
referrer_flags=$(grep -Ec -- '^[[:space:]]+--registry-referrers-mode=oci-1-1([[:space:]]|$)' "$WORKFLOW" || true)
bundle_flags=$(grep -Ec -- '^[[:space:]]+--new-bundle-format([[:space:]]|$)' "$WORKFLOW" || true)
if [ "$sign_commands" -ne 2 ] ||
  [ "$recursive_flags" -ne "$sign_commands" ] ||
  [ "$referrer_flags" -ne "$sign_commands" ] ||
  [ "$bundle_flags" -ne "$sign_commands" ]; then
  echo "FAIL: backend signing must use recursive Sigstore bundles through OCI 1.1 referrers (commands=$sign_commands recursive=$recursive_flags referrers=$referrer_flags bundle_flags=$bundle_flags)"
  exit 1
fi

echo "PASS: backend signing uses recursive Sigstore bundles through OCI 1.1 referrers for both registries"
