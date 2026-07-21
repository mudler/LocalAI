#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail

# The full-tree fingerprint makes this effective on a clean CI checkout (where
# a worktree-only diff would always be empty). Most existing direct clients are
# loopback fixtures; changing the inventory requires an intentional baseline
# update after review.
expected_inventory=2885a428cdab55eea357dae3ec47b3d44f9999b59542d06cd3d792cf491c76b3
inventory=$(
  {
    rg --no-heading --no-line-number --glob '*_test.go' \
      '(http\.(Get|Post|Head)\(|http\.Default(Client|Transport)|net\.Dial\(|exec\.Command\([^,]+,[[:space:]]*"(curl|wget)")' \
      pkg core tests backend || true
    rg --no-heading --no-line-number --glob '*.sh' '(curl|wget)[[:space:]]' tests backend || true
  } | LC_ALL=C sort
)
if command -v sha256sum >/dev/null 2>&1; then
  actual_inventory=$(printf '%s\n' "$inventory" | sha256sum | awk '{print $1}')
else
  actual_inventory=$(printf '%s\n' "$inventory" | shasum -a 256 | awk '{print $1}')
fi
if [[ $actual_inventory != "$expected_inventory" ]]; then
  echo 'Test network mechanism inventory changed; remove the direct access or review and update the lint baseline:' >&2
  echo "$inventory" >&2
  exit 1
fi

# Also give contributors a focused diagnostic for newly introduced remote
# literals and direct mechanisms instead of only reporting the fingerprint.
base=${TEST_NETWORK_LINT_BASE:-HEAD}
violations=$(git diff --unified=0 "$base" -- api pkg core tests backend | \
	  rg '^\+[^+].*(http\.(Get|Post|Head)\(|http\.Default(Client|Transport)|net\.Dial\(|exec\.Command\([^,]+,[[:space:]]*"(curl|wget)"|https?://)' | \
	  rg -v 'test-network: fixture' || true)
if [[ -n "$violations" ]]; then
  echo 'Direct test network access is forbidden; use a fixture or guarded transport:' >&2
  echo "$violations" >&2
  exit 1
fi
