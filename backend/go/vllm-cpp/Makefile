CMAKE_ARGS?=
BUILD_TYPE?=
NATIVE?=false

GOCMD?=go
GO_TAGS?=
JOBS?=$(shell nproc --ignore=1)

# vllm.cpp version
VLLM_CPP_REPO?=https://github.com/mudler/vllm.cpp
VLLM_CPP_VERSION?=61f3e85dcd5d889840f76c090054b95ad0d4c767

# The backend consumes only the stable C ABI (libvllm + include/vllm.h), so the
# server, examples and tests of the engine are never built here.
CMAKE_ARGS+=-DVLLM_CPP_SERVER=OFF -DVLLM_CPP_BUILD_TESTS=OFF -DVLLM_CPP_BUILD_EXAMPLES=OFF
CMAKE_ARGS+=-DCMAKE_BUILD_TYPE=Release

# vllm.cpp sets no global -march: SIMD tiers are per-file with runtime dispatch,
# so ONE portable library serves every CPU of the target arch (unlike the
# ggml-based backends and their avx/avx2/avx512 variant builds).
UNAME_M := $(shell uname -m)

ifeq ($(BUILD_TYPE),cublas)
	# Blackwell-family targets only: other CUDA arches are build-supported
	# upstream but have no runtime-proven fast path. amd64 gets the consumer
	# (120a) + GB10 (121a) fat binary; arm64 CUDA (l4t-style images, DGX
	# Spark) is GB10 only. Triton-AOT GDN cubins are vendored per-arch, no
	# Python needed to consume them.
	ifeq ($(UNAME_M),x86_64)
		# NO -DVLLM_CPP_TRITON on fat builds: the vendored Triton-AOT cubin
		# trees are per-arch and the engine refuses a multi-arch build unless
		# pinned to one tree (unsound for the other arch). The non-AOT GDN
		# path serves the fat binary; single-arch builds keep the cubins.
		#
		# CUDA 12 (12.8) nvcc does NOT know compute_121a (GB10 support landed
		# with CUDA 13), so the cuda-12 image targets consumer Blackwell only;
		# cuda-13 gets the 120a;121a fat binary.
		ifeq ($(CUDA_MAJOR_VERSION),12)
			CMAKE_ARGS+=-DVLLM_CPP_CUDA=ON -DVLLM_CPP_CUDA_ARCHITECTURES=120a
		else
			CMAKE_ARGS+=-DVLLM_CPP_CUDA=ON "-DVLLM_CPP_CUDA_ARCHITECTURES=120a;121a"
		endif
	else
		CMAKE_ARGS+=-DVLLM_CPP_CUDA=ON -DVLLM_CPP_CUDA_ARCHITECTURES=121a -DVLLM_CPP_TRITON=ON
	endif
else ifeq ($(BUILD_TYPE),vulkan)
	CMAKE_ARGS+=-DVLLM_CPP_VULKAN=ON -DVLLM_CPP_CUDA=OFF
else ifeq ($(BUILD_TYPE),metal)
	CMAKE_ARGS+=-DVLLM_CPP_METAL=ON
else
	CMAKE_ARGS+=-DVLLM_CPP_CUDA=OFF
endif

UNAME_S := $(shell uname -s)
ifeq ($(UNAME_S),Darwin)
	LIB=libvllm.dylib
else
	LIB=libvllm.so
endif

sources/vllm.cpp:
	mkdir -p sources/vllm.cpp
	cd sources/vllm.cpp && \
	git init && \
	git remote add origin $(VLLM_CPP_REPO) && \
	git fetch --depth 1 origin $(VLLM_CPP_VERSION) && \
	git checkout FETCH_HEAD

$(LIB): sources/vllm.cpp
	mkdir -p build && \
	cd build && \
	cmake ../sources/vllm.cpp $(CMAKE_ARGS) && \
	cmake --build . --config Release -j$(JOBS) --target vllm_shared
	cp -fL build/$(LIB) ./$(LIB)

vllm-cpp: main.go govllmcpp.go backend.go options.go $(LIB)
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o vllm-cpp ./

package: vllm-cpp
	bash package.sh

build: package

clean: purge
	rm -rf libvllm.so libvllm.dylib package sources/vllm.cpp vllm-cpp

purge:
	rm -rf build

.NOTPARALLEL:

# The unit specs are pure Go (struct mirrors, option mapping, load
# validation): no libvllm build is needed. The e2e specs skip unless
# VLLM_CPP_MODEL points at a real model (then build the lib first).
test:
	@echo "Running vllm-cpp tests..."
	bash test.sh
	@echo "vllm-cpp tests completed."

all: vllm-cpp package
