#!/bin/bash
# Patch the shared backend/cpp/llama-cpp/grpc-server.cpp *copy* used by the
# buun-llama-cpp build to account for three gaps between upstream and the fork:
#
#   1. Augment the kv_cache_types[] allow-list so `LoadModel` accepts the
#      fork-specific `turbo2` / `turbo3` / `turbo4` cache types plus the buun
#      additions `turbo2_tcq` / `turbo3_tcq`.
#
#   2. Adapt the post-refactor speculative-decoding fields and option handlers
#      to the fork's legacy flat common_params_speculative layout, while adding
#      buun-exclusive tree_budget / draft_topk support.
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

if grep -q 'buun-llama-cpp legacy speculative options' "$SRC"; then
    echo "==> $SRC already has legacy speculative option handlers, skipping"
else
    echo "==> replacing modern speculative option handlers with the fork-compatible set"

    # Replace the whole speculative option section. The fork predates chained
    # speculative types and the nested draft/ngram families, so retaining any
    # modern-only handler makes the copied server fail at compile time.
    awk '
        /} else if \(!strcmp\(optname, "spec_type"\)/ && !done {
            print "        // buun-llama-cpp legacy speculative options"
            print "        } else if (!strcmp(optname, \"spec_type\") || !strcmp(optname, \"speculative_type\")) {"
            print "            auto type = common_speculative_type_from_name(optval_str.substr(0, optval_str.find(\",\")));"
            print "            if (type != COMMON_SPECULATIVE_TYPE_COUNT) params.speculative.type = type;"
            print "        } else if (!strcmp(optname, \"spec_n_max\") || !strcmp(optname, \"draft_max\")) {"
            print "            if (optval != NULL) { try { params.speculative.n_max = std::stoi(optval_str); } catch (...) {} }"
            print "        } else if (!strcmp(optname, \"spec_n_min\") || !strcmp(optname, \"draft_min\")) {"
            print "            if (optval != NULL) { try { params.speculative.n_min = std::stoi(optval_str); } catch (...) {} }"
            print "        } else if (!strcmp(optname, \"spec_p_min\") || !strcmp(optname, \"draft_p_min\")) {"
            print "            if (optval != NULL) { try { params.speculative.p_min = std::stof(optval_str); } catch (...) {} }"
            print "        } else if (!strcmp(optname, \"spec_p_split\")) {"
            print "            if (optval != NULL) { try { params.speculative.p_split = std::stof(optval_str); } catch (...) {} }"
            print "        } else if (!strcmp(optname, \"spec_ngram_size_n\") || !strcmp(optname, \"ngram_size_n\")) {"
            print "            if (optval != NULL) { try { params.speculative.ngram_size_n = (uint16_t)std::stoi(optval_str); } catch (...) {} }"
            print "        } else if (!strcmp(optname, \"spec_ngram_size_m\") || !strcmp(optname, \"ngram_size_m\")) {"
            print "            if (optval != NULL) { try { params.speculative.ngram_size_m = (uint16_t)std::stoi(optval_str); } catch (...) {} }"
            print "        } else if (!strcmp(optname, \"spec_ngram_min_hits\") || !strcmp(optname, \"ngram_min_hits\")) {"
            print "            if (optval != NULL) { try { params.speculative.ngram_min_hits = (uint16_t)std::stoi(optval_str); } catch (...) {} }"
            print "        } else if (!strcmp(optname, \"draft_gpu_layers\")) {"
            print "            if (optval != NULL) { try { params.speculative.n_gpu_layers = std::stoi(optval_str); } catch (...) {} }"
            print "        } else if (!strcmp(optname, \"tree_budget\")) {"
            print "            if (optval != NULL) { try { params.speculative.tree_budget = std::stoi(optval_str); } catch (...) {} }"
            print "        } else if (!strcmp(optname, \"draft_topk\")) {"
            print "            if (optval != NULL) { try { params.speculative.draft_topk = std::stoi(optval_str); } catch (...) {} }"
            skipping = 1
            next
        }
        skipping && /^    }$/ { skipping = 0; done = 1; print; next }
        !skipping { print }
        END {
            if (!done) {
                print "patch-grpc-server.sh: speculative option section not found" > "/dev/stderr"
                exit 1
            }
        }
    ' "$SRC" > "$SRC.tmp"
    mv "$SRC.tmp" "$SRC"

    echo "==> legacy speculative option-handler patch OK"
fi

# The modern server initializes a vector of speculative types when DraftModel
# is present. The fork still exposes a single enum value.
awk '
    /const bool no_spec_type = params\.speculative\.types\.empty\(\)/ && !done {
        print "        if (params.speculative.type == COMMON_SPECULATIVE_TYPE_NONE) {"
        print "            params.speculative.type = COMMON_SPECULATIVE_TYPE_DRAFT;"
        print "        }"
        skipping = 1
        next
    }
    skipping && /^        }$/ { skipping = 0; done = 1; next }
    !skipping { print }
' "$SRC" > "$SRC.tmp"
mv "$SRC.tmp" "$SRC"

# Map supported post-refactor fields back to the names used by the pinned fork.
sed -E \
    -e 's/params\.speculative\.draft\.mparams\.path/params.speculative.mparams_dft.path/g' \
    -e 's/params\.speculative\.draft\.n_gpu_layers/params.speculative.n_gpu_layers/g' \
    -e 's/ctx_server\.impl->model_tgt/ctx_server.impl->model/g' \
    -e '/params\.cache_idle_slots =/d' \
    -e '/params\.split_mode = LLAMA_SPLIT_MODE_TENSOR;/d' \
    -e '/params\.speculative\.draft\.tensor_buft_overrides/d' \
    "$SRC" > "$SRC.tmp"
mv "$SRC.tmp" "$SRC"

if ! grep -q '^#define LOCALAI_TURBOQUANT_NO_CHECKPOINT_MIN_STEP' "$SRC"; then
    sed '0,/^#include/{s/^#include/#define LOCALAI_TURBOQUANT_NO_CHECKPOINT_MIN_STEP 1\n\n#include/}' "$SRC" > "$SRC.tmp"
    mv "$SRC.tmp" "$SRC"
fi

if grep -qE 'ctx_server\.get_meta\(\)\.logit_bias_eog|params_base\.sampling\.logit_bias_eog,' "$SRC"; then
    echo "==> patching $SRC to drop the logit_bias_eog arg from params_from_json_cmpl() callsites (buun still uses the pre-refactor 4-arg signature)"
    # Upstream llama.cpp refactored params_from_json_cmpl to take a precomputed
    # logit_bias_eog vector after buun's 2026-04-05 fork-point — simultaneously
    # adding server_context_meta::logit_bias_eog as the supplier. Buun carries
    # neither change: its params_from_json_cmpl is still 4-arg, and internally
    # derives logit_bias_eog from the common_params it's passed. So we just
    # delete the argument line entirely — the remaining 4 args match buun's
    # signature and the resulting behavior matches upstream bit-for-bit
    # (upstream's 5th arg is the same data buun derives internally).
    #
    # Guard is broad so this works whether the line has been run through this
    # block before (leaving params_base.sampling.logit_bias_eog,) or not
    # (leaving the original ctx_server.get_meta().logit_bias_eog,).
    sed -E '/^[[:space:]]+(ctx_server\.get_meta\(\)\.logit_bias_eog|params_base\.sampling\.logit_bias_eog),$/d' "$SRC" > "$SRC.tmp"
    mv "$SRC.tmp" "$SRC"
    echo "==> logit_bias_eog arg drop OK"
else
    echo "==> $SRC has no logit_bias_eog arg line, skipping"
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
