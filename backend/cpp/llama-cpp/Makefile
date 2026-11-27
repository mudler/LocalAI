
LLAMA_VERSION?=9c003a97ab34a69cc49243f9b02014038205ffae
LLAMA_REPO?=https://github.com/ggerganov/llama.cpp

CMAKE_ARGS?=
BUILD_TYPE?=
NATIVE?=false
ONEAPI_VARS?=/opt/intel/oneapi/setvars.sh
TARGET?=--target grpc-server
JOBS?=$(shell nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 1)
ARCH?=$(shell uname -m)

# Shared libs default to OFF: we link static gRPC and the avx/avx2/avx512/fallback
# variants are fully static. The CPU_ALL_VARIANTS build flips SHARED_LIBS=ON (ggml/llama
# become shared so the dynamic CPU backends work; gRPC stays static via its imported
# targets). SHARED_LIBS is a make variable, not an appended -D, so it survives the
# recursive sub-make into the VARIANT build dir (which re-parses this Makefile) instead
# of being re-clobbered by a second -DBUILD_SHARED_LIBS=OFF. EXTRA_CMAKE_ARGS is the hook
# the CPU_ALL_VARIANTS target uses to inject -DGGML_BACKEND_DL/-DGGML_CPU_ALL_VARIANTS.
SHARED_LIBS?=OFF
EXTRA_CMAKE_ARGS?=
CMAKE_ARGS+=-DBUILD_SHARED_LIBS=$(SHARED_LIBS) -DLLAMA_CURL=OFF $(EXTRA_CMAKE_ARGS)

# LLGUIDANCE=ON links llguidance (-DLLAMA_LLGUIDANCE=ON), the grammar engine that
# the grammar_engine model option selects. It needs cargo on PATH: llama.cpp builds
# the library with cargo at configure time. Off by default, so a build without a
# Rust toolchain is unchanged. The image builds turn it on in .docker/llama-cpp-compile.sh.
LLGUIDANCE?=OFF
ifeq ($(LLGUIDANCE),ON)
	CMAKE_ARGS+=-DLLAMA_LLGUIDANCE=ON
endif

CURRENT_MAKEFILE_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
ifeq ($(NATIVE),false)
	CMAKE_ARGS+=-DGGML_NATIVE=OFF -DLLAMA_OPENSSL=OFF
endif
# If build type is cublas, then we set -DGGML_CUDA=ON to CMAKE_ARGS automatically
ifeq ($(BUILD_TYPE),cublas)
	CMAKE_ARGS+=-DGGML_CUDA=ON
# If build type is openblas then we set -DGGML_BLAS=ON -DGGML_BLAS_VENDOR=OpenBLAS
# to CMAKE_ARGS automatically
else ifeq ($(BUILD_TYPE),openblas)
	CMAKE_ARGS+=-DGGML_BLAS=ON -DGGML_BLAS_VENDOR=OpenBLAS
# If build type is clblas (openCL) we set -DGGML_CLBLAST=ON -DCLBlast_DIR=/some/path
else ifeq ($(BUILD_TYPE),clblas)
	CMAKE_ARGS+=-DGGML_CLBLAST=ON -DCLBlast_DIR=/some/path
# If it's hipblas we do have also to set CC=/opt/rocm/llvm/bin/clang CXX=/opt/rocm/llvm/bin/clang++
else ifeq ($(BUILD_TYPE),hipblas)
	ROCM_HOME ?= /opt/rocm
	ROCM_PATH ?= /opt/rocm
	export CXX=$(ROCM_HOME)/llvm/bin/clang++
	export CC=$(ROCM_HOME)/llvm/bin/clang
	AMDGPU_TARGETS?=gfx908,gfx90a,gfx942,gfx950,gfx1030,gfx1100,gfx1101,gfx1102,gfx1103,gfx1150,gfx1151,gfx1152,gfx1153,gfx1200,gfx1201
ifeq ($(strip $(AMDGPU_TARGETS)),)
$(error AMDGPU_TARGETS is empty — set it to a comma-separated list of gfx targets e.g. gfx1100,gfx1101)
endif
	CMAKE_ARGS+=-DGGML_HIP=ON -DAMDGPU_TARGETS=$(AMDGPU_TARGETS)
else ifeq ($(BUILD_TYPE),vulkan)
	CMAKE_ARGS+=-DGGML_VULKAN=1
else ifeq ($(OS),Darwin)
	ifeq ($(BUILD_TYPE),)
		BUILD_TYPE=metal
	endif
	ifneq ($(BUILD_TYPE),metal)
		CMAKE_ARGS+=-DGGML_METAL=OFF
	else
		CMAKE_ARGS+=-DGGML_METAL=ON
		CMAKE_ARGS+=-DGGML_METAL_EMBED_LIBRARY=ON
		CMAKE_ARGS+=-DGGML_METAL_USE_BF16=ON
		CMAKE_ARGS+=-DGGML_OPENMP=OFF
	endif
	TARGET+=--target ggml-metal
endif

ifeq ($(BUILD_TYPE),sycl_f16)
	CMAKE_ARGS+=-DGGML_SYCL=ON \
		-DCMAKE_C_COMPILER=icx \
		-DCMAKE_CXX_COMPILER=icpx \
		-DCMAKE_DISABLE_PRECOMPILE_HEADERS=ON \
		-DCMAKE_CXX_FLAGS="-fsycl" \
		-DGGML_SYCL_F16=ON
endif

ifeq ($(BUILD_TYPE),sycl_f32)
	CMAKE_ARGS+=-DGGML_SYCL=ON \
		-DCMAKE_C_COMPILER=icx \
		-DCMAKE_CXX_COMPILER=icpx \
		-DCMAKE_DISABLE_PRECOMPILE_HEADERS=ON \
		-DCMAKE_CXX_FLAGS="-fsycl"
endif

INSTALLED_PACKAGES=$(CURDIR)/../grpc/installed_packages
INSTALLED_LIB_CMAKE=$(INSTALLED_PACKAGES)/lib/cmake
ADDED_CMAKE_ARGS=-Dabsl_DIR=${INSTALLED_LIB_CMAKE}/absl \
				 -DProtobuf_DIR=${INSTALLED_LIB_CMAKE}/protobuf \
				 -Dutf8_range_DIR=${INSTALLED_LIB_CMAKE}/utf8_range \
				 -DgRPC_DIR=${INSTALLED_LIB_CMAKE}/grpc \
				 -DCMAKE_CXX_STANDARD_INCLUDE_DIRECTORIES=${INSTALLED_PACKAGES}/include
build-llama-cpp-grpc-server:
# Conditionally build grpc for the llama backend to use if needed
ifdef BUILD_GRPC_FOR_BACKEND_LLAMA
	$(MAKE) -C ../../grpc build
	_PROTOBUF_PROTOC=${INSTALLED_PACKAGES}/bin/proto \
	_GRPC_CPP_PLUGIN_EXECUTABLE=${INSTALLED_PACKAGES}/bin/grpc_cpp_plugin \
	PATH="${INSTALLED_PACKAGES}/bin:${PATH}" \
	CMAKE_ARGS="${CMAKE_ARGS} ${ADDED_CMAKE_ARGS}" \
	LLAMA_VERSION=$(LLAMA_VERSION) \
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../$(VARIANT) grpc-server
else
	echo "BUILD_GRPC_FOR_BACKEND_LLAMA is not defined."
	LLAMA_VERSION=$(LLAMA_VERSION) $(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../$(VARIANT) grpc-server
endif

llama-cpp-avx2: llama.cpp
	cp -rf $(CURRENT_MAKEFILE_DIR)/../llama-cpp $(CURRENT_MAKEFILE_DIR)/../llama-cpp-avx2-build
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../llama-cpp-avx2-build purge
	$(info ${GREEN}I llama-cpp build info:avx2${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=off -DGGML_FMA=on -DGGML_F16C=on" $(MAKE) VARIANT="llama-cpp-avx2-build" build-llama-cpp-grpc-server
	cp -rfv $(CURRENT_MAKEFILE_DIR)/../llama-cpp-avx2-build/grpc-server llama-cpp-avx2

llama-cpp-avx512: llama.cpp
	cp -rf $(CURRENT_MAKEFILE_DIR)/../llama-cpp $(CURRENT_MAKEFILE_DIR)/../llama-cpp-avx512-build
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../llama-cpp-avx512-build purge
	$(info ${GREEN}I llama-cpp build info:avx512${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=on -DGGML_FMA=on -DGGML_F16C=on" $(MAKE) VARIANT="llama-cpp-avx512-build" build-llama-cpp-grpc-server
	cp -rfv $(CURRENT_MAKEFILE_DIR)/../llama-cpp-avx512-build/grpc-server llama-cpp-avx512

llama-cpp-avx: llama.cpp
	cp -rf $(CURRENT_MAKEFILE_DIR)/../llama-cpp $(CURRENT_MAKEFILE_DIR)/../llama-cpp-avx-build
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../llama-cpp-avx-build purge
	$(info ${GREEN}I llama-cpp build info:avx${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) VARIANT="llama-cpp-avx-build" build-llama-cpp-grpc-server
	cp -rfv $(CURRENT_MAKEFILE_DIR)/../llama-cpp-avx-build/grpc-server llama-cpp-avx

llama-cpp-fallback: llama.cpp
	cp -rf $(CURRENT_MAKEFILE_DIR)/../llama-cpp $(CURRENT_MAKEFILE_DIR)/../llama-cpp-fallback-build
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../llama-cpp-fallback-build purge
	$(info ${GREEN}I llama-cpp build info:fallback${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) VARIANT="llama-cpp-fallback-build" build-llama-cpp-grpc-server
	cp -rfv $(CURRENT_MAKEFILE_DIR)/../llama-cpp-fallback-build/grpc-server llama-cpp-fallback

# CPU_VARIANTS_DIR (absolute path) holds libggml-cpu-*.so built elsewhere from the same
# llama.cpp commit (.docker/llama-cpp-cpu-variants.sh). When set, this tree is built
# with -DGGML_CPU=OFF, so only the loader and the GPU backend, if any, are compiled
# here, and the variants are copied into ggml-shared-libs. The variants link only
# libggml-base, so they load next to this tree's own copy of it. Unset, the target
# builds the variants in the same tree (local builds, macOS).
CPU_VARIANTS_DIR?=

# CPU_ALL_RPC=ON also builds the RPC backend (libggml-rpc.so) and ggml-rpc-server in the
# cpu-all tree, and ships that tree's grpc-server as llama-cpp-grpc and its
# ggml-rpc-server as llama-cpp-rpc-server. Both find the RPC backend through the backend
# registry (add_rpc_devices and rpc-server.cpp call ggml_backend_reg_by_name("RPC")), so
# a GGML_BACKEND_DL tree serves them as it serves the CPU and GPU backends. The image
# build sets it: without it the llama-cpp-grpc target compiles the GPU backend a second
# time (statically, for every CUDA or HIP arch), which was half of each GPU build.
# Off by default for the Darwin and Windows builds, which call llama-cpp-grpc directly.
CPU_ALL_RPC?=OFF
ifeq ($(CPU_ALL_RPC),ON)
CPU_ALL_RPC_CMAKE_ARGS:=-DGGML_RPC=ON
CPU_ALL_RPC_TARGET:=--target ggml-rpc-server
endif

# Single-build CPU backend using ggml's CPU_ALL_VARIANTS. Produces ONE grpc-server
# plus a set of dlopen-able libggml-cpu-*.so (sandybridge/haswell/skylakex/...) that
# ggml's backend registry selects from at runtime by probing host CPU features.
# Replaces the avx/avx2/avx512/fallback multi-binary build on x86.
#
# CPU_ALL_VARIANTS requires GGML_BACKEND_DL, which requires BUILD_SHARED_LIBS=ON, so we
# pass SHARED_LIBS=ON and the DL flags as make variables (NOT pre-expanded into the
# CMAKE_ARGS env string): command-line make variables propagate through every recursive
# sub-make, so the deepest VARIANT-dir build computes BUILD_SHARED_LIBS=ON consistently.
# Only ggml/llama go shared - gRPC is found via its static imported targets, so the
# grpc-server binary keeps static gRPC and only dynamically links ggml.
#
# TARGET adds "ggml": the per-microarch backends are runtime-dlopened, not link deps of
# grpc-server, so they only build because each is an add_dependencies() of the ggml target.
llama-cpp-cpu-all: llama.cpp
	cp -rf $(CURRENT_MAKEFILE_DIR)/../llama-cpp $(CURRENT_MAKEFILE_DIR)/../llama-cpp-cpu-all-build
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../llama-cpp-cpu-all-build purge
	$(info ${GREEN}I llama-cpp build info:cpu-all-variants${RESET})
ifneq ($(strip $(CPU_VARIANTS_DIR)),)
	test "$$(cat $(CPU_VARIANTS_DIR)/LLAMA_VERSION)" = "$(LLAMA_VERSION)" || { echo "CPU variants in $(CPU_VARIANTS_DIR) were built for another llama.cpp commit than $(LLAMA_VERSION)" >&2; exit 1; }
	$(MAKE) SHARED_LIBS=ON EXTRA_CMAKE_ARGS="-DGGML_BACKEND_DL=ON -DGGML_CPU=OFF $(CPU_ALL_RPC_CMAKE_ARGS)" TARGET="--target grpc-server --target ggml $(CPU_ALL_RPC_TARGET)" VARIANT="llama-cpp-cpu-all-build" build-llama-cpp-grpc-server
else
	$(MAKE) SHARED_LIBS=ON EXTRA_CMAKE_ARGS="-DGGML_BACKEND_DL=ON -DGGML_CPU_ALL_VARIANTS=ON $(CPU_ALL_RPC_CMAKE_ARGS)" TARGET="--target grpc-server --target ggml $(CPU_ALL_RPC_TARGET)" VARIANT="llama-cpp-cpu-all-build" build-llama-cpp-grpc-server
endif
	cp -rfv $(CURRENT_MAKEFILE_DIR)/../llama-cpp-cpu-all-build/grpc-server llama-cpp-cpu-all
ifeq ($(CPU_ALL_RPC),ON)
	cp -fv $(CURRENT_MAKEFILE_DIR)/../llama-cpp-cpu-all-build/grpc-server llama-cpp-grpc
	cp -fv $(CURRENT_MAKEFILE_DIR)/../llama-cpp-cpu-all-build/llama.cpp/build/bin/ggml-rpc-server llama-cpp-rpc-server
endif
	rm -rf ggml-shared-libs && mkdir -p ggml-shared-libs
	find $(CURRENT_MAKEFILE_DIR)/../llama-cpp-cpu-all-build/llama.cpp/build \( -name '*.so*' -o -name '*.dylib' \) -exec cp -av {} ggml-shared-libs/ \;
ifneq ($(strip $(CPU_VARIANTS_DIR)),)
	cp -av $(CPU_VARIANTS_DIR)/libggml-cpu-*.so ggml-shared-libs/
endif
	@echo "Collected ggml shared backends:" && ls -la ggml-shared-libs/

llama-cpp-grpc: llama.cpp
	cp -rf $(CURRENT_MAKEFILE_DIR)/../llama-cpp $(CURRENT_MAKEFILE_DIR)/../llama-cpp-grpc-build
	$(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../llama-cpp-grpc-build purge
	$(info ${GREEN}I llama-cpp build info:grpc${RESET})
	CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_RPC=ON -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" TARGET="--target grpc-server --target ggml-rpc-server" $(MAKE) VARIANT="llama-cpp-grpc-build" build-llama-cpp-grpc-server
	cp -rfv $(CURRENT_MAKEFILE_DIR)/../llama-cpp-grpc-build/grpc-server llama-cpp-grpc

llama-cpp-rpc-server: llama-cpp-grpc
	cp -rf $(CURRENT_MAKEFILE_DIR)/../llama-cpp-grpc-build/llama.cpp/build/bin/ggml-rpc-server llama-cpp-rpc-server

llama.cpp:
	mkdir -p llama.cpp
	cd llama.cpp && \
	git init && \
	git remote add origin $(LLAMA_REPO)  && \
	( \
		attempt=1; \
		until git fetch --all --tags; do \
			if [ "$$attempt" -ge 3 ]; then \
				echo "git fetch failed after $$attempt attempts" >&2; \
				exit 1; \
			fi; \
			delay=$$((attempt * 3)); \
			echo "git fetch attempt $$attempt failed; retrying in $${delay}s" >&2; \
			sleep "$$delay" || exit 1; \
			attempt=$$((attempt + 1)); \
		done \
	) && \
	git checkout -b build $(LLAMA_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

llama.cpp/tools/grpc-server: llama.cpp
	mkdir -p llama.cpp/tools/grpc-server
	bash prepare.sh

rebuild:
	bash prepare.sh
	rm -rf grpc-server
	$(MAKE) grpc-server

package:
	bash package.sh

purge:
	rm -rf llama.cpp/build
	rm -rf llama.cpp/tools/grpc-server
	rm -rf grpc-server

clean: purge
	rm -rf llama.cpp

grpc-server: llama.cpp llama.cpp/tools/grpc-server
	@echo "Building grpc-server with $(BUILD_TYPE) build type and $(CMAKE_ARGS)"
ifneq (,$(findstring sycl,$(BUILD_TYPE)))
	+bash -c "source $(ONEAPI_VARS); \
	cd llama.cpp && mkdir -p build && cd build && cmake .. $(CMAKE_ARGS) && cmake --build . --config Release -j $(JOBS) $(TARGET)"
else
	+cd llama.cpp && mkdir -p build && cd build && cmake .. $(CMAKE_ARGS) && cmake --build . --config Release -j $(JOBS) $(TARGET)
endif
	cp llama.cpp/build/bin/grpc-server .
