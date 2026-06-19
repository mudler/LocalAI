CXX ?= g++
CXXFLAGS ?= -std=c++17 -O2 -Wall -Wextra -I.

TESTS = test_free_block_queue test_block_pool test_paged_kv_manager test_prefix_cache
BINS  = $(addprefix tests/,$(TESTS))

all: $(BINS)

tests/%: tests/%.cpp paged_kv_manager.cpp paged_kv_manager.h
	$(CXX) $(CXXFLAGS) -o $@ $< paged_kv_manager.cpp

check: all
	@for t in $(BINS); do echo "== $$t =="; ./$$t || exit 1; done

paged-bench: paged-bench.cpp paged_kv_manager.cpp paged_kv_manager.h
	$(CXX) $(CXXFLAGS) -o $@ paged-bench.cpp paged_kv_manager.cpp

bench: paged-bench
	./paged-bench

# --- Optional ggml integration test (Phase 1: paged write/gather mechanism) ---
# Requires a built ggml. Override these to point at your checkout / build:
#   make ggml-check GGML_SRC=<llama.cpp>/ggml GGML_BUILD=<ggml-build>
GGML_SRC   ?= ../../llama-cpp-fallback-build/llama.cpp/ggml
GGML_BUILD ?= /tmp/ggml-build
GGML_LIBDIR = $(GGML_BUILD)/src

GGML_TESTS = test_ggml_paged_rw test_ggml_paged_attn
GGML_BINS  = $(addprefix tests/,$(GGML_TESTS))

tests/test_ggml_%: tests/test_ggml_%.cpp paged_kv_manager.cpp paged_kv_manager.h
	$(CXX) $(CXXFLAGS) -I$(GGML_SRC)/include -o $@ $< paged_kv_manager.cpp \
		-L$(GGML_LIBDIR) -lggml -lggml-base -lggml-cpu -Wl,-rpath,$(GGML_LIBDIR)

ggml-check: $(GGML_BINS)
	@for t in $(GGML_BINS); do echo "== $$t =="; ./$$t || exit 1; done

clean:
	rm -f $(BINS) $(GGML_BINS) paged-bench

.PHONY: all check ggml-check clean
