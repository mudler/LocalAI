#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail

# The full-tree fingerprint makes this effective on a clean CI checkout (where
# a worktree-only diff would always be empty). Most existing direct clients are
# loopback fixtures; changing the inventory requires an intentional baseline
# update after review.
expected_inventory=6b8a611b9d01ea1b58f446ccdd92efdbb01c34663ee5946194ccc0f00e8e877d
search_test_files() {
  local pattern=$1
  shift
  while IFS= read -r -d '' file; do
    [[ $file == *_test.go ]] && grep -hE "$pattern" "$file" || true
  done < <(git ls-files -co --exclude-standard -z -- "$@")
}

search_shell_files() {
  local pattern=$1
  shift
  while IFS= read -r -d '' file; do
    [[ $file == *.sh ]] && grep -hE "$pattern" "$file" || true
  done < <(git ls-files -co --exclude-standard -z -- "$@")
}

inventory=$(
  {
    search_test_files \
      '(http\.(Get|Post|Head)\(|http\.Default(Client|Transport)|net\.Dial\(|exec\.Command\([^,]+,[[:space:]]*"(curl|wget)")' \
      pkg core tests backend
    search_shell_files '(curl|wget)[[:space:]]' tests backend
  } | LC_ALL=C sort
)
if command -v sha256sum >/dev/null 2>&1; then
  actual_inventory=$(printf '%s\n' "$inventory" | sha256sum | awk '{print $1}')
else
  actual_inventory=$(printf '%s\n' "$inventory" | shasum -a 256 | awk '{print $1}')
fi
if [[ $actual_inventory != "$expected_inventory" ]]; then
  echo "Test network mechanism inventory changed (expected $expected_inventory, got $actual_inventory); remove the direct access or review and update the lint baseline:" >&2
  echo "$inventory" >&2
  exit 1
fi

# Also give contributors a focused diagnostic for newly introduced remote
# literals and direct mechanisms instead of only reporting the fingerprint.
base=${TEST_NETWORK_LINT_BASE:-HEAD}
violations=$(git diff --unified=0 "$base" -- api pkg core tests backend | \
  grep -E '^\+[^+].*(http\.(Get|Post|Head)\(|http\.Default(Client|Transport)|net\.Dial\(|exec\.Command\([^,]+,[[:space:]]*"(curl|wget)"|https?://)' | \
  grep -Ev 'test-network: fixture' || true)
if [[ -n "$violations" ]]; then
  echo 'Direct test network access is forbidden; use a fixture or guarded transport:' >&2
  echo "$violations" >&2
  exit 1
fi
