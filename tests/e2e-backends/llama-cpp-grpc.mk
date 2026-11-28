# CPU Docker targets run by test-extra.yml (tests-llama-cpp-grpc).
test-extra-backend-llama-cpp: docker-build-llama-cpp
	BACKEND_IMAGE=local-ai-backend:llama-cpp \
	BACKEND_TEST_CAPS=health,load,predict,stream,logprobs,logit_bias,context_overflow \
	$(MAKE) test-extra-backend

## Paged serving (paged_kv): admission, preemption with resume by recompute,
## the prefill budget and the prompt cache. The 1024-token pool holds about
## three of the overcommit requests, and the small output reserve makes the
## parity requests outgrow the pool, so both the waiting and the preemption
## paths run on CPU. Set prefill_long between the short chat prompts and
## the budget probe: even the long probe fits inside the 1024-token pool.
test-extra-backend-llama-cpp-paged: docker-build-llama-cpp
	BACKEND_IMAGE=local-ai-backend:llama-cpp \
	BACKEND_TEST_CTX_SIZE=1024 \
	BACKEND_TEST_OPTIONS=paged_kv,parallel:4,kv_output_reserve:16,prefill_budget:16,prefill_long:128,spec_type:none \
	BACKEND_TEST_CAPS=health,load,paged_overcommit,paged_parity,paged_budget,paged_cache,paged_attach,paged_resume \
	$(MAKE) test-extra-backend

## The same on a hybrid model (Qwen3.5, gated delta-net layers with a
## recurrent state): preempted requests resume by recompute instead of failing.
test-extra-backend-llama-cpp-paged-hybrid: docker-build-llama-cpp
	BACKEND_IMAGE=local-ai-backend:llama-cpp \
	BACKEND_TEST_MODEL_URL=https://huggingface.co/turintech/Qwen3.5-0.8B-Artemis-GGUF/resolve/98eb257fe3b2b165e5b4f0ecfa9f127a17823e06/Qwen3.5-0.8B-Artemis-Q4_K_M.gguf \
	BACKEND_TEST_CTX_SIZE=1024 \
	BACKEND_TEST_OPTIONS=paged_kv,parallel:4,kv_output_reserve:16,prefill_budget:16 \
	BACKEND_TEST_CAPS=health,load,paged_overcommit,paged_resume \
	$(MAKE) test-extra-backend

## Image requests under pool pressure (LFM2-VL-450M): they cannot be recomputed,
## so a preempted one is stashed in RAM and restored instead of failing.
test-extra-backend-llama-cpp-paged-vision: docker-build-llama-cpp
	BACKEND_IMAGE=local-ai-backend:llama-cpp \
	BACKEND_TEST_MODEL_URL=https://huggingface.co/LiquidAI/LFM2-VL-450M-GGUF/resolve/main/LFM2-VL-450M-Q8_0.gguf \
	BACKEND_TEST_MMPROJ_URL=https://huggingface.co/LiquidAI/LFM2-VL-450M-GGUF/resolve/main/mmproj-LFM2-VL-450M-Q8_0.gguf \
	BACKEND_TEST_CTX_SIZE=1024 \
	BACKEND_TEST_OPTIONS=paged_kv,parallel:4,kv_output_reserve:16,prefill_budget:16 \
	BACKEND_TEST_CAPS=health,load,paged_stash \
	$(MAKE) test-extra-backend

## Raw llama.cpp embeddings are required by Go-side pooling. This exercises the
## real C++ backend and verifies that it marks the flattened matrix per-token.
test-extra-backend-llama-cpp-embeddings: docker-build-llama-cpp
	BACKEND_IMAGE=local-ai-backend:llama-cpp \
	BACKEND_TEST_CAPS=health,load,embeddings \
	BACKEND_TEST_OPTIONS=pooling:none \
	BACKEND_TEST_EMBEDDING_LAYOUT=per_token \
	$(MAKE) test-extra-backend

## Grammars built from a JSON schema, once with each grammar engine. The image
## is built with llguidance, so grammar_engine:llguidance must sample with the
## llguidance form and grammar_engine:gbnf with the GBNF one.
test-extra-backend-llama-cpp-grammar: docker-build-llama-cpp
	for engine in gbnf llguidance; do \
		BACKEND_IMAGE=local-ai-backend:llama-cpp \
		BACKEND_TEST_CAPS=health,load,grammar,grammar_stress \
		BACKEND_TEST_CTX_SIZE=4096 \
		BACKEND_TEST_OPTIONS=grammar_engine:$$engine \
		BACKEND_TEST_GRAMMAR_ENGINE=$$engine \
		$(MAKE) test-extra-backend || exit 1; \
	done

## RPC mode: llama-cpp-grpc loads the model and offloads it to llama-cpp-rpc-server,
## both started from the image through run.sh, as a LocalAI worker does.
test-extra-backend-llama-cpp-rpc: docker-build-llama-cpp
	bash scripts/ci/llama-cpp-rpc-smoke.sh local-ai-backend:llama-cpp
