CXX ?= g++
CXXFLAGS ?= -std=c++17 -O2 -Wall -Wextra -I.

TESTS = test_free_block_queue test_block_pool test_paged_kv_manager test_prefix_cache
BINS  = $(addprefix tests/,$(TESTS))

all: $(BINS)

tests/%: tests/%.cpp paged_kv_manager.cpp paged_kv_manager.h
	$(CXX) $(CXXFLAGS) -o $@ $< paged_kv_manager.cpp

check: all
	@for t in $(BINS); do echo "== $$t =="; ./$$t || exit 1; done

clean:
	rm -f $(BINS)

.PHONY: all check clean
