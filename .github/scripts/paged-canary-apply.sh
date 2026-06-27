#!/usr/bin/env bash
#
# paged-canary-apply.sh - apply the vendored paged-attention patch series
# (backend/cpp/llama-cpp-localai-paged/patches/paged/0001-0030) to a llama.cpp checkout, the
# same way the build does, but tolerating the ONE known-benign pre-existing
# quirk in the series. Used by the early-warning canary
# (.github/workflows/llama-cpp-paged-canary.yml) so it only goes red on a REAL
# upstream break, never on that quirk.
#
# Usage: paged-canary-apply.sh <llama.cpp-checkout-dir> <patches-dir>
#   <patches-dir> is normally backend/cpp/llama-cpp-localai-paged/patches (it holds the
#   top-level base series 0*.patch, currently empty, and the paged/ subseries).
#
# Exit 0  = the whole series applied -> patches still fit upstream.
# Exit !=0 = a patch failed to apply  = the red signal: an upstream change moved
#            the tree out from under the patches, so it is time to run a PIN_SYNC.
#
# Apply method MIRRORS backend/cpp/llama-cpp/Makefile's `llama.cpp` target:
# plain `git apply --verbose`, which natively tolerates @@ line-number offsets
# but NOT context-line changes. Matching the build's method is the point - the
# canary's apply result is exactly what the real build's apply would do.
#
# The ONLY tolerance, and it is path-scoped (not a blanket `|| true`): patch
# 0019 carries a stray *modify* hunk against the dev-only doc
# SSM_DECODE_FIX_RESULTS.md, a file that exists only on the DGX dev tree and is
# absent from any clean upstream checkout. `git apply` is atomic, so that single
# missing-file hunk rejects the whole patch - and because 0021/0022/0026/0028
# build on 0019's code, the rejection cascades to them too. This is a
# PRE-EXISTING shipped-series defect, present identically on every pin, NOT an
# upstream break (see backend/cpp/llama-cpp-localai-paged/patches/paged/PIN_SYNC_c299a92c.md
# and README.md). We exclude ONLY that dev-doc path and still
# apply 0019's real code hunks atomically, so a genuine code-hunk break in 0019
# still fails the canary. prepare.sh tolerates the same hunk via
# `patch ... || true`; this mirrors that tolerance precisely.

set -euo pipefail

CHECKOUT="${1:?usage: paged-canary-apply.sh <llama.cpp-checkout> <patches-dir>}"
PATCHES="${2:?usage: paged-canary-apply.sh <llama.cpp-checkout> <patches-dir>}"

# The lone tolerated dev-doc, and the only patch allowed to carry it.
DEVDOC_GLOB='*SSM_DECODE_FIX_RESULTS.md'
DEVDOC_PATCH='0019-qwen35-ssm-decode-fused-gather.patch'

# Resolve to absolute paths so the apply works after we cd into the checkout.
PATCHES="$(cd "$PATCHES" && pwd)"
cd "$CHECKOUT"

shopt -s nullglob

apply_one() {
  local p="$1"; shift
  echo "paged-canary: applying $(basename "$p")"
  if ! git apply --verbose "$@" "$p"; then
    echo "::error::paged patch no longer applies to the upstream llama.cpp tip: $(basename "$p")"
    echo "::error::upstream drifted past the vendored paged series - run a PIN_SYNC (backend/cpp/llama-cpp-localai-paged/patches/paged/PIN_SYNC_c299a92c.md), do NOT bump the pin blindly"
    exit 1
  fi
}

# Base series first (parity with the build: patches/0*.patch before
# patches/paged/0*.patch). Currently empty; nullglob makes this a no-op.
for p in "$PATCHES"/0*.patch; do
  apply_one "$p"
done

# Paged series, in order.
for p in "$PATCHES"/paged/0*.patch; do
  if [ "$(basename "$p")" = "$DEVDOC_PATCH" ]; then
    # Apply 0019's real code hunks; exclude ONLY the benign dev-doc hunk.
    apply_one "$p" --exclude="$DEVDOC_GLOB"
  else
    apply_one "$p"
  fi
done

echo "paged-canary: the full paged patch series applied cleanly to the upstream tip"
