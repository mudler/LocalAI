# SPDX-License-Identifier: MIT

GUFO_VERSION?=fd1710b5fd090880722e0681a868df2006595c73
GUFO_REPO?=https://github.com/gufo-org/gufo
GUFO_STAMP=gufo/.localai-$(GUFO_VERSION)

CURRENT_MAKEFILE_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
JOBS ?= $(shell nproc 2>/dev/null || echo 4)
# Extra cmake arguments for machines without the dev packages gufo needs (see cpu-check).
CPU_CHECK_CMAKE_ARGS ?=

.PHONY: all gufo-src cpu-check grpc-server package test clean purge
all: grpc-server

# gufo has no library API, so LocalAI carries three small patches on top of the
# pinned commit (see patches/). The stamp makes a changed pin re-fetch.
$(GUFO_STAMP): $(wildcard patches/*.patch)
	rm -rf gufo
	mkdir -p gufo
	cd gufo && git init -q && \
		git remote add origin $(GUFO_REPO) && \
		git fetch --depth 1 origin $(GUFO_VERSION) && \
		git checkout --detach FETCH_HEAD && \
		for p in $(CURRENT_MAKEFILE_DIR)patches/*.patch; do git apply "$$p" || exit 1; done && \
		touch .localai-$(GUFO_VERSION)

gufo-src: $(GUFO_STAMP)

# Compile-checks the gRPC-free engine wrapper against gufo's own headers on a
# host with no ROCm. It cannot run inference: without HIP, InferenceBackend::load
# returns an error. The real build is the hipblas image.
cpu-check: gufo-src
	cd gufo && cmake --preset cpu-test \
		-DGUFO_LOCALAI_BACKEND_DIR=$(CURRENT_MAKEFILE_DIR) \
		-DGUFO_BACKEND_BUILD_GRPC=OFF $(CPU_CHECK_CMAKE_ARGS) && \
		cmake --build build/cpu-test --target gufo_engine -j $(JOBS)

grpc-server: gufo-src
	cd gufo && cmake --preset release \
		-DGUFO_LOCALAI_BACKEND_DIR=$(CURRENT_MAKEFILE_DIR) \
		-DGUFO_BACKEND_BUILD_GRPC=ON && \
		cmake --build --preset release --target grpc-server -j $(JOBS)
	cp gufo/build/release/localai-backend/grpc-server grpc-server

package: grpc-server
	bash package.sh

test:
	@echo "gufo backend: pure-logic unit tests run through 'make test-backend-cpp' at the repo root"

clean:
	rm -rf build package grpc-server

purge: clean
	rm -rf gufo
