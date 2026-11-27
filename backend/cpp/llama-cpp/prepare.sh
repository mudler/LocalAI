#!/bin/bash

set -e


## Patches

## Apply the `*.patch` files of the `patches` directory, in name order. Runs
## under set -e so a rejected patch aborts the build here, loudly, instead of
## surfacing later as a confusing compile error. A missing or empty patches
## dir is a no-op. Other files in the directory are not applied. See PATCHES.md.
if [ -d "patches" ]; then
    for patch in patches/*.patch; do
        [ -e "$patch" ] || continue
        echo "Applying patch $patch"
        patch -d llama.cpp/ -p1 < "$patch"
    done
fi

## Paged serving policy (serving_policy.h). patches/0002-paged-serving.patch
## includes it from tools/server/server-context.cpp, so it goes next to that
## file before the copy below. Forks that drop the patches still get the
## header: grpc-server.cpp includes it for the option checks.
cp -r serving_policy.h llama.cpp/tools/server/

## Apple RDMA link fixup.

## ggml-rpc hands Apple's librdma to the linker with
## target_link_options(ggml-rpc PRIVATE "LINKER:-weak_library,..."). Link options are not
## a usage requirement of a static library, so in our BUILD_SHARED_LIBS=OFF build the flag
## dies with libggml-rpc.a and every ibv_* symbol transport-apple.cpp reaches for comes out
## undefined when grpc-server and ggml-rpc-server link. Re-declare the same weak link as
## INTERFACE so it travels to whoever links the static library.
##
## Guarded on the marker so a second prepare.sh over the same checkout is a no-op, and on
## GGML_RPC_RDMA_APPLE so forks that branched before the Apple RDMA transport (turboquant,
## bonsai) are left alone.
RPC_CMAKE=llama.cpp/ggml/src/ggml-rpc/CMakeLists.txt
if [ -f "$RPC_CMAKE" ] && grep -q "GGML_RPC_RDMA_APPLE" "$RPC_CMAKE" && ! grep -q "LOCALAI_RDMA_IFACE" "$RPC_CMAKE"; then
    echo "==> ggml-rpc carries the Apple RDMA transport, re-declaring its weak librdma link as INTERFACE"
    cat >> "$RPC_CMAKE" <<'EOF'

# LOCALAI_RDMA_IFACE: added by backend/cpp/llama-cpp/prepare.sh
if (GGML_RPC_RDMA AND APPLE AND NOT BUILD_SHARED_LIBS)
    target_link_options(ggml-rpc INTERFACE "LINKER:-weak_library,${RDMA_LIB}")
endif()
EOF
fi

for file in $(ls llama.cpp/tools/server/); do
    cp -rfv llama.cpp/tools/server/$file llama.cpp/tools/grpc-server/
done

cp -r CMakeLists.txt llama.cpp/tools/grpc-server/
cp -r grpc-server.cpp llama.cpp/tools/grpc-server/
cp -r decision_compat.h llama.cpp/tools/grpc-server/
cp -r decision_images.h llama.cpp/tools/grpc-server/
# Model-load diagnostics (included by grpc-server.cpp) and their standalone
# regression test.
cp -r model_load_error.h llama.cpp/tools/grpc-server/
cp -r model_load_error_test.cpp llama.cpp/tools/grpc-server/
# Shared message-reconstruction helpers (included by grpc-server.cpp) and their
# unit test (compiled only when -DLLAMA_GRPC_BUILD_TESTS=ON).
cp -r message_content.h llama.cpp/tools/grpc-server/
cp -r message_content_test.cpp llama.cpp/tools/grpc-server/
# Generic passthrough parser staging and its standalone regression test.
cp -r passthrough_options.h llama.cpp/tools/grpc-server/
cp -r passthrough_options_test.cpp llama.cpp/tools/grpc-server/
# TTS request validation (included by grpc-server.cpp) and its standalone
# regression test.
cp -r tts_request_options.h llama.cpp/tools/grpc-server/
cp -r tts_request_options_test.cpp llama.cpp/tools/grpc-server/
# Transcription prompt construction (included by grpc-server.cpp) and its
# standalone regression test.
cp -r transcription_prompt.h llama.cpp/tools/grpc-server/
cp -r transcription_prompt_test.cpp llama.cpp/tools/grpc-server/
# Thread-count default normalization and its standalone regression test.
cp -r thread_params.h llama.cpp/tools/grpc-server/
cp -r thread_params_test.cpp llama.cpp/tools/grpc-server/
# Slot-count resolution (option over LLAMACPP_PARALLEL) and its standalone
# regression test.
cp -r parallel_params.h llama.cpp/tools/grpc-server/
cp -r parallel_params_test.cpp llama.cpp/tools/grpc-server/
# backend_sampling option parsing and the auto decision, and their standalone
# regression test.
cp -r backend_sampling.h llama.cpp/tools/grpc-server/
cp -r backend_sampling_test.cpp llama.cpp/tools/grpc-server/
# grammar_engine option parsing and the grammar choice, and their standalone
# regression test.
cp -r grammar_engine.h llama.cpp/tools/grpc-server/
cp -r grammar_engine_test.cpp llama.cpp/tools/grpc-server/
# moe_cache_mib option parsing and its standalone regression test.
cp -r moe_cache_option.h llama.cpp/tools/grpc-server/
# Value parsers for the reasoning, n_cpu_ffn and speculative draft options, and their test.
cp -r option_parse.h llama.cpp/tools/grpc-server/
cp -r option_parse_test.cpp llama.cpp/tools/grpc-server/
cp -r moe_cache_option_test.cpp llama.cpp/tools/grpc-server/
# Parent-death watcher (included by grpc-server.cpp) and its standalone unit
# test (run via backend/cpp/run-unit-tests.sh; also buildable under ctest).
cp -r parent_watch.h llama.cpp/tools/grpc-server/
cp -r parent_watch_test.cpp llama.cpp/tools/grpc-server/
# Dead-stream tracker (included by grpc-server.cpp) and its standalone unit
# test (run via backend/cpp/run-unit-tests.sh; also buildable under ctest).
cp -r stream_peer.h llama.cpp/tools/grpc-server/
cp -r stream_peer_test.cpp llama.cpp/tools/grpc-server/
# Conversation logic of AudioToAudioStream (included by grpc-server.cpp), its
# standalone unit test, and the check against the real LFM3 chat template
# (ctest only, see CMakeLists.txt).
cp -r realtime_session.h llama.cpp/tools/grpc-server/
cp -r realtime_session_test.cpp llama.cpp/tools/grpc-server/
cp -r realtime_chat_check.cpp llama.cpp/tools/grpc-server/
# Slot cache save/restore helpers (included by grpc-server.cpp) and their
# unit test (compiled only when -DLLAMA_GRPC_BUILD_TESTS=ON).
cp -r slot_cache.h llama.cpp/tools/grpc-server/
# Paged serving policy and its standalone unit test (run-unit-tests.sh).
cp -r serving_policy_test.cpp llama.cpp/tools/grpc-server/
cp -r slot_cache_test.cpp llama.cpp/tools/grpc-server/
cp -rfv llama.cpp/vendor/nlohmann/json.hpp llama.cpp/tools/grpc-server/
cp -rfv llama.cpp/vendor/cpp-httplib/httplib.h llama.cpp/tools/grpc-server/

## Fork-skew probe. Upstream folded common_params::use_mmap / use_mlock /
## use_direct_io into a single `load_mode` enum (ggml-org/llama.cpp#20834).
## turboquant and bonsai compile this very same grpc-server.cpp against forks
## that branched before that change, so the field set is decided from the
## checkout in front of us rather than from a per-fork build flag: the flavor
## targets disagree on whether they forward CMAKE_ARGS or EXTRA_CMAKE_ARGS, and
## probing heals itself the moment a fork rebases past the refactor.
if grep -q "LLAMA_LOAD_MODE_MMAP" llama.cpp/include/llama.h; then
    echo "==> llama.cpp carries the load-mode enum, using common_params::load_mode"
    LEGACY_LOAD_MODE=0
else
    echo "==> llama.cpp predates the load-mode enum, using the legacy mmap/mlock/direct-io booleans"
    LEGACY_LOAD_MODE=1
fi
# Device-aware load mode (LLAMA_LOAD_MODE_AUTO, ggml-org/llama.cpp#26081).
# Forks that have the enum but branched before AUTO fall back to MMAP.
if grep -q "LLAMA_LOAD_MODE_AUTO" llama.cpp/include/llama.h; then
    HAS_LOAD_MODE_AUTO=1
else
    HAS_LOAD_MODE_AUTO=0
fi
if grep -q "server_metrics metrics;" llama.cpp/tools/server/server-task.h; then
    HAS_SERVER_METRICS=1
else
    HAS_SERVER_METRICS=0
fi
if grep -q "mtmd_helper_init_opt" llama.cpp/tools/mtmd/mtmd-helper.h; then
    HAS_MTMD_INIT_OPT=1
else
    HAS_MTMD_INIT_OPT=0
fi
if grep -q "llm_add_n_cpu_ffn_overrides" llama.cpp/common/common.h; then
    HAS_N_CPU_FFN_HELPER=1
else
    HAS_N_CPU_FFN_HELPER=0
fi
# Dense FFN spill (--n-cpu-ffn, LLM_FFN_DENSE_REGEX). Forks that branched earlier lack the regex.
if grep -q "LLM_FFN_DENSE_REGEX" llama.cpp/common/common.h; then
    HAS_N_CPU_FFN_DENSE=1
else
    HAS_N_CPU_FFN_DENSE=0
fi
# Per-slot context limit (--kv-unified-per-slot).
if grep -q "kv_unified_per_slot" llama.cpp/common/common.h; then
    HAS_KV_UNIFIED_PER_SLOT=1
else
    HAS_KV_UNIFIED_PER_SLOT=0
fi
# Draft context micro-batch (--spec-draft-n-ubatch), draft sampling mode
# (--spec-draft-sampling) and draft devices (--spec-draft-device).
if grep -q "speculative.draft.n_ubatch" llama.cpp/common/arg.cpp; then
    HAS_SPEC_DRAFT_N_UBATCH=1
else
    HAS_SPEC_DRAFT_N_UBATCH=0
fi
if grep -q "speculative.draft.probabilistic" llama.cpp/common/arg.cpp; then
    HAS_SPEC_DRAFT_SAMPLING=1
else
    HAS_SPEC_DRAFT_SAMPLING=0
fi
if grep -q '"--spec-draft-device"' llama.cpp/common/arg.cpp; then
    HAS_SPEC_DRAFT_DEVICE=1
else
    HAS_SPEC_DRAFT_DEVICE=0
fi
# Draft type auto-detection from the draft GGUF (common_speculative_types_from_gguf).
if grep -q "common_speculative_types_from_gguf" llama.cpp/common/speculative.h; then
    HAS_SPEC_TYPES_FROM_GGUF=1
else
    HAS_SPEC_TYPES_FROM_GGUF=0
fi
# MoE expert GPU cache (--moe-cache-mib). Forks that branched earlier lack the field.
if grep -q "moe_cache_size" llama.cpp/common/common.h; then
    HAS_MOE_CACHE=1
else
    HAS_MOE_CACHE=0
fi
# Share of MoE cache misses computed on the CPU (--moe-cache-cpu-share). Older forks lack the field.
if grep -q "moe_cache_cpu_share" llama.cpp/common/common.h; then
    HAS_MOE_CACHE_CPU_SHARE=1
else
    HAS_MOE_CACHE_CPU_SHARE=0
fi
# On-demand tensor reading (--lazy-mode). Forks that branched earlier lack the field.
if grep -q "llama_lazy_mode lazy_mode" llama.cpp/common/common.h; then
    HAS_LAZY_MODE=1
else
    HAS_LAZY_MODE=0
fi
# Realtime conversations (SERVER_TASK_TYPE_REALTIME, server-realtime.cpp).
# Forks that branched before the upstream realtime split lack the task type.
if grep -q "SERVER_TASK_TYPE_REALTIME" llama.cpp/tools/server/server-task.h; then
    HAS_REALTIME=1
else
    HAS_REALTIME=0
fi
# Per-session TTS voice (mtmd_helper_gen_audio_inp::set_voice). Older pins have
# no voice selection for gen-audio models.
if grep -q "set_voice" llama.cpp/tools/mtmd/mtmd-helper.h; then
    HAS_TTS_VOICE=1
else
    HAS_TTS_VOICE=0
fi
# Per-task ASR preset (common_chat_get_asr_prompt takes the requested language,
# ggml-org/llama.cpp#31257). Older pins have the one-argument form, or none.
if grep -qF "common_chat_get_asr_prompt(const common_chat_templates * chat_templates, const std::string & language" llama.cpp/common/chat.h; then
    HAS_ASR_PRESET=1
else
    HAS_ASR_PRESET=0
fi
# Newer servers return template state separately from the request JSON.
if grep -q "common_chat_session & out_session" llama.cpp/tools/server/server-common.h; then
    HAS_CHAT_SESSION=1
else
    HAS_CHAT_SESSION=0
fi
# Multimodal rerank (query_files/doc_files on format_prompt_rerank,
# ggml-org/llama.cpp#29625). Forks that branched earlier keep the old signature.
if grep -q "query_files" llama.cpp/tools/server/server-common.h; then
    HAS_RERANK_FILES=1
else
    HAS_RERANK_FILES=0
fi
# Decision questions take an audio-input flag (ggml-org/llama.cpp#32578).
# Older pins and forks take the body only, or have no decision server at all.
if grep -q "bool support_audio_input" llama.cpp/tools/server/server-decision.h 2>/dev/null; then
    HAS_DECISION_AUDIO_INPUT=1
else
    HAS_DECISION_AUDIO_INPUT=0
fi
# Backend (GPU) sampling that the server honours per task
# (ggml-org/llama.cpp#25532). Forks that branched earlier lack it. The grammar
# accessor and the on-demand reasoning budget came later than the plain fields;
# probe them so backend_sampling:auto mirrors the exclusions the fork has.
if grep -q "sampling.backend_sampling" llama.cpp/tools/server/server-context.cpp; then
    HAS_BACKEND_SAMPLING=1
else
    HAS_BACKEND_SAMPLING=0
fi
if grep -q "common_grammar_value" llama.cpp/common/common.h; then
    HAS_GRAMMAR_VALUE=1
else
    HAS_GRAMMAR_VALUE=0
fi
if grep -q "bool *reasoning_control" llama.cpp/common/common.h; then
    HAS_REASONING_CONTROL=1
else
    HAS_REASONING_CONTROL=0
fi
# Paged serving hooks (patches/0002-paged-serving.patch adds
# common_params::paged_serving). Forks that drop the patches build without
# them and only warn when a model asks for paged_kv or prefill_budget.
if grep -q "paged_serving" llama.cpp/common/common.h; then
    HAS_SERVING_POLICY=1
else
    HAS_SERVING_POLICY=0
fi
# Request priority (patches/0003, upstream #32219) and admission ordering (patches/0004).
if grep -q "int priority" llama.cpp/tools/server/server-task.h; then
    HAS_TASK_PRIORITY=1
else
    HAS_TASK_PRIORITY=0
fi
if grep -q "paged_order" llama.cpp/common/common.h; then
    HAS_ADMISSION_ORDER=1
else
    HAS_ADMISSION_ORDER=0
fi
cat > llama.cpp/tools/grpc-server/llama_compat.h <<EOF
// Generated by backend/cpp/llama-cpp/prepare.sh. Do not edit.
#pragma once
#define LOCALAI_LEGACY_LOAD_MODE ${LEGACY_LOAD_MODE}
#define LOCALAI_HAS_LOAD_MODE_AUTO ${HAS_LOAD_MODE_AUTO}
#define LOCALAI_HAS_CHAT_SESSION ${HAS_CHAT_SESSION}
#define LOCALAI_HAS_SERVER_METRICS ${HAS_SERVER_METRICS}
#define LOCALAI_HAS_MTMD_INIT_OPT ${HAS_MTMD_INIT_OPT}
#define LOCALAI_HAS_N_CPU_FFN_HELPER ${HAS_N_CPU_FFN_HELPER}
#define LOCALAI_HAS_N_CPU_FFN_DENSE ${HAS_N_CPU_FFN_DENSE}
#define LOCALAI_HAS_KV_UNIFIED_PER_SLOT ${HAS_KV_UNIFIED_PER_SLOT}
#define LOCALAI_HAS_SPEC_DRAFT_N_UBATCH ${HAS_SPEC_DRAFT_N_UBATCH}
#define LOCALAI_HAS_SPEC_DRAFT_SAMPLING ${HAS_SPEC_DRAFT_SAMPLING}
#define LOCALAI_HAS_SPEC_DRAFT_DEVICE ${HAS_SPEC_DRAFT_DEVICE}
#define LOCALAI_HAS_SPEC_TYPES_FROM_GGUF ${HAS_SPEC_TYPES_FROM_GGUF}
#define LOCALAI_HAS_MOE_CACHE ${HAS_MOE_CACHE}
#define LOCALAI_HAS_MOE_CACHE_CPU_SHARE ${HAS_MOE_CACHE_CPU_SHARE}
#define LOCALAI_HAS_LAZY_MODE ${HAS_LAZY_MODE}
#define LOCALAI_HAS_RERANK_FILES ${HAS_RERANK_FILES}
#define LOCALAI_HAS_REALTIME ${HAS_REALTIME}
#define LOCALAI_HAS_TTS_VOICE ${HAS_TTS_VOICE}
#define LOCALAI_HAS_ASR_PRESET ${HAS_ASR_PRESET}
#define LOCALAI_HAS_DECISION_AUDIO_INPUT ${HAS_DECISION_AUDIO_INPUT}
#define LOCALAI_HAS_BACKEND_SAMPLING ${HAS_BACKEND_SAMPLING}
#define LOCALAI_HAS_GRAMMAR_VALUE ${HAS_GRAMMAR_VALUE}
#define LOCALAI_HAS_REASONING_CONTROL ${HAS_REASONING_CONTROL}
#define LOCALAI_HAS_SERVING_POLICY ${HAS_SERVING_POLICY}
#define LOCALAI_HAS_TASK_PRIORITY ${HAS_TASK_PRIORITY}
#define LOCALAI_HAS_ADMISSION_ORDER ${HAS_ADMISSION_ORDER}
EOF

set +e
if grep -q "grpc-server" llama.cpp/tools/CMakeLists.txt; then
    echo "grpc-server already added"
else
    echo "add_subdirectory(grpc-server)" >> llama.cpp/tools/CMakeLists.txt
fi
set -e
