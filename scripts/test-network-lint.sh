#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail

# Enforce the policy on additions while the existing loopback-only test client
# call sites are migrated. The normal lint baseline must not make unrelated
# changes responsible for historical debt.
base=${TEST_NETWORK_LINT_BASE:-HEAD}
violations=$(git diff --unified=0 "$base" -- api pkg core tests backend | \
	  rg '^\+[^+].*(http\.(Get|Post|Head)\(|http\.Default(Client|Transport)|net\.Dial\(|exec\.Command\([^,]+,[[:space:]]*"(curl|wget)"|https?://)' | \
	  rg -v 'test-network: fixture' || true)
if [[ -n "$violations" ]]; then
  echo 'Direct test network access is forbidden; use a fixture or guarded transport:' >&2
  echo "$violations" >&2
  exit 1
fi
