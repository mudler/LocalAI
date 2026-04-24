#!/bin/bash
# Patch the shared backend/cpp/llama-cpp/grpc-server.cpp *copy* used by the
# buun-llama-cpp build to account for three gaps between upstream and the fork:
#
#   1. Augment the kv_cache_types[] allow-list so `LoadModel` accepts the
#      fork-specific `turbo2` / `turbo3` / `turbo4` cache types plus the buun
#      additions `turbo2_tcq` / `turbo3_tcq`.
#
#   2. Wire up buun-exclusive speculative-decoding option handlers
#      (tree_budget / draft_topk) alongside the existing spec_* handlers.
#      These reference struct fields (common_params.speculative.tree_budget
#      and .draft_topk) that only exist in buun's common/common.h — adding
#      them to the shared backend/cpp/llama-cpp/grpc-server.cpp would break
#      the stock llama-cpp build, so we inject them only into the buun copy.
#
#   3. Replace `get_media_marker()` (added upstream in ggml-org/llama.cpp#21962,
#      server-side random per-instance marker) with the legacy "<__media__>"
#      literal. The fork branched before that PR, so server-common.cpp has no
#      get_media_marker symbol. The fork's mtmd_default_marker() still returns
#      "<__media__>", and Go-side tooling falls back to that sentinel when the
#      backend does not expose media_marker, so substituting the literal keeps
#      behavior identical on the buun path.
#
# We patch the *copy* sitting in buun-llama-cpp-<flavor>-build/, never the
# original under backend/cpp/llama-cpp/, so the stock llama-cpp build keeps
# compiling against vanilla upstream.
#
# Idempotent: skips each insertion if its marker is already present (so re-runs
# of the same build dir don't double-insert).

set -euo pipefail

if [[ $# -ne 1 ]]; then
    echo "usage: $0 <grpc-server.cpp>" >&2
    exit 2
fi

SRC=$1

if [[ ! -f "$SRC" ]]; then
    echo "grpc-server.cpp not found at $SRC" >&2
    exit 2
fi

if grep -q 'GGML_TYPE_TURBO2_TCQ' "$SRC"; then
    echo "==> $SRC already has buun cache types, skipping KV allow-list patch"
else
    echo "==> patching $SRC to allow turbo2/turbo3/turbo4/turbo2_tcq/turbo3_tcq KV-cache types"

    # Insert the five TURBO entries right after the first `    GGML_TYPE_Q5_1,`
    # line (the kv_cache_types[] allow-list). Using awk because the builder
    # image does not ship python3, and GNU sed's multi-line `a\` quoting is
    # awkward.
    awk '
        /^    GGML_TYPE_Q5_1,$/ && !done {
            print
            print "    // buun-llama-cpp fork extras — added by patch-grpc-server.sh"
            print "    GGML_TYPE_TURBO2_0,"
            print "    GGML_TYPE_TURBO3_0,"
            print "    GGML_TYPE_TURBO4_0,"
            print "    GGML_TYPE_TURBO2_TCQ,"
            print "    GGML_TYPE_TURBO3_TCQ,"
            done = 1
            next
        }
        { print }
        END {
            if (!done) {
                print "patch-grpc-server.sh: anchor `    GGML_TYPE_Q5_1,` not found" > "/dev/stderr"
                exit 1
            }
        }
    ' "$SRC" > "$SRC.tmp"
    mv "$SRC.tmp" "$SRC"

    echo "==> KV allow-list patch OK"
fi

if grep -q 'optname, "tree_budget"' "$SRC"; then
    echo "==> $SRC already has DFlash option handlers, skipping"
else
    echo "==> patching $SRC to add tree_budget / draft_topk option handlers"

    # Insert two new `else if` handlers between the inner close-brace of the
    # `spec_p_split` block and the next `} else if (…spec_ngram_size_n…)` line.
    # Upstream writes each `} else if` as a single physical line, so we don't
    # emit an outer `}` ourselves — the existing next line provides both the
    # close of our `draft_topk` block and the open of `spec_ngram_size_n`.
    # Anchor on the exact 3-line body of spec_p_split so we can't drift.
    awk '
        prev2 == "        } else if (!strcmp(optname, \"spec_p_split\")) {" &&
        prev1 ~ /^ +if \(optval != NULL\) \{$/ &&
        $0    ~ /^ +try \{ params\.speculative\.p_split = std::stof\(optval_str\); \} catch \(\.\.\.\) \{\}$/ &&
        !done {
            print                        # print the try-line itself
            getline inner_close          # read "            }" closing the inner if
            print inner_close            # print it — this closes spec_p_split body
            print "        // buun-llama-cpp DFlash options — added by patch-grpc-server.sh"
            print "        } else if (!strcmp(optname, \"tree_budget\")) {"
            print "            if (optval != NULL) {"
            print "                try { params.speculative.tree_budget = std::stoi(optval_str); } catch (...) {}"
            print "            }"
            print "        } else if (!strcmp(optname, \"draft_topk\")) {"
            print "            if (optval != NULL) {"
            print "                try { params.speculative.draft_topk = std::stoi(optval_str); } catch (...) {}"
            print "            }"
            # The next source line (`} else if (…spec_ngram_size_n…) {`) closes
            # our draft_topk block and continues the chain naturally; fall back
            # into the main loop to emit it and everything after.
            done = 1
            prev2 = prev1
            prev1 = inner_close
            next
        }
        { print; prev2 = prev1; prev1 = $0 }
        END {
            if (!done) {
                print "patch-grpc-server.sh: spec_p_split anchor not found" > "/dev/stderr"
                exit 1
            }
        }
    ' "$SRC" > "$SRC.tmp"
    mv "$SRC.tmp" "$SRC"

    echo "==> DFlash option-handler patch OK"
fi

if grep -q 'ctx_server\.get_meta()\.logit_bias_eog' "$SRC"; then
    echo "==> patching $SRC to source logit_bias_eog from params_base.sampling (buun predates server_context_meta::logit_bias_eog accessor)"
    # Upstream llama.cpp exposes logit_bias_eog through server_context_meta
    # after buun's 2026-04-05 fork-point. Buun still carries the underlying
    # data on common_params_sampling::logit_bias_eog (the struct field the
    # meta accessor eventually returns). Rewriting the call site to read
    # params_base.sampling.logit_bias_eog works against both trees — upstream
    # still populates that same vector the newer accessor returns.
    sed 's/ctx_server\.get_meta()\.logit_bias_eog/params_base.sampling.logit_bias_eog/g' "$SRC" > "$SRC.tmp"
    mv "$SRC.tmp" "$SRC"
    echo "==> logit_bias_eog substitution OK"
else
    echo "==> $SRC has no ctx_server.get_meta().logit_bias_eog call, skipping logit_bias_eog patch"
fi

if grep -q 'get_media_marker()' "$SRC"; then
    echo "==> patching $SRC to replace get_media_marker() with legacy \"<__media__>\" literal"
    # Only one call site today (ModelMetadata), but replace all occurrences to
    # stay robust if upstream adds more. Use a temp file to avoid relying on
    # sed -i portability (the builder image uses GNU sed, but keeping this
    # consistent with the awk block above).
    sed 's/get_media_marker()/"<__media__>"/g' "$SRC" > "$SRC.tmp"
    mv "$SRC.tmp" "$SRC"
    echo "==> get_media_marker() substitution OK"
else
    echo "==> $SRC has no get_media_marker() call, skipping media-marker patch"
fi

echo "==> all patches applied"
