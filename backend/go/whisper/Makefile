GOCMD=go
NATIVE?=false

BUILD_TYPE?=
CMAKE_ARGS?=

# whisper.cpp version
WHISPER_REPO?=https://github.com/ggml-org/whisper.cpp
WHISPER_CPP_VERSION?=d0a9d8c7f8f7b91c51d77bbaa394b915f79cde6b

export WHISPER_CMAKE_ARGS?=-DBUILD_SHARED_LIBS=OFF
export WHISPER_DIR=$(abspath ./sources/whisper.cpp)
export WHISPER_INCLUDE_PATH=$(WHISPER_DIR)/include:$(WHISPER_DIR)/ggml/include
export WHISPER_LIBRARY_PATH=$(WHISPER_DIR)/build/src/:$(WHISPER_DIR)/build/ggml/src

CGO_LDFLAGS_WHISPER?=
CGO_LDFLAGS_WHISPER+=-lggml
CMAKE_ARGS+=-DBUILD_SHARED_LIBS=OFF -DLLAMA_CURL=OFF
CUDA_LIBPATH?=/usr/local/cuda/lib64/

ONEAPI_VERSION?=2025.2

# IF native is false, we add -DGGML_NATIVE=OFF to CMAKE_ARGS
ifeq ($(NATIVE),false)
	CMAKE_ARGS+=-DGGML_NATIVE=OFF
	WHISPER_CMAKE_ARGS+=-DGGML_NATIVE=OFF
endif
CURRENT_MAKEFILE_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
ifeq ($(NATIVE),false)
	CMAKE_ARGS+=-DGGML_NATIVE=OFF
endif
# If build type is cublas, then we set -DGGML_CUDA=ON to CMAKE_ARGS automatically
ifeq ($(BUILD_TYPE),cublas)
	CGO_LDFLAGS+=-lcublas -lcudart -L$(CUDA_LIBPATH) -L$(CUDA_LIBPATH)/stubs/ -lcuda
	CMAKE_ARGS+=-DGGML_CUDA=ON
	CGO_LDFLAGS_WHISPER+=-lcufft -lggml-cuda
	export WHISPER_LIBRARY_PATH:=$(WHISPER_LIBRARY_PATH):$(WHISPER_DIR)/build/ggml/src/ggml-cuda/
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
	LD_LIBRARY_PATH ?= /opt/rocm/lib:/opt/rocm/llvm/lib
	export STABLE_BUILD_TYPE=
	export CXX=$(ROCM_HOME)/llvm/bin/clang++
	export CC=$(ROCM_HOME)/llvm/bin/clang
#	GPU_TARGETS ?= gfx803,gfx900,gfx906,gfx908,gfx90a,gfx942,gfx1010,gfx1030,gfx1032,gfx1100,gfx1101,gfx1102
#	AMDGPU_TARGETS ?= "$(GPU_TARGETS)"
	CMAKE_ARGS+=-DGGML_HIP=ON
	CGO_LDFLAGS += -O3 --rtlib=compiler-rt -unwindlib=libgcc -lhipblas -lrocblas --hip-link -L${ROCM_HOME}/lib/llvm/lib -L$(CURRENT_MAKEFILE_DIR)/sources/whisper.cpp/build/ggml/src/ggml-hip/ -lggml-hip
#	CMAKE_ARGS+=-DGGML_HIP=ON -DAMDGPU_TARGETS="$(AMDGPU_TARGETS)" -DGPU_TARGETS="$(GPU_TARGETS)"
else ifeq ($(BUILD_TYPE),vulkan)
	CMAKE_ARGS+=-DGGML_VULKAN=1
	CGO_LDFLAGS_WHISPER+=-lggml-vulkan -lvulkan
	export WHISPER_LIBRARY_PATH:=$(WHISPER_LIBRARY_PATH):$(WHISPER_DIR)/build/ggml/src/ggml-vulkan/
else ifeq ($(OS),Darwin)
	ifeq ($(BUILD_TYPE),)
		BUILD_TYPE=metal
	endif
	ifneq ($(BUILD_TYPE),metal)
		CMAKE_ARGS+=-DGGML_METAL=OFF
		CGO_LDFLAGS_WHISPER+=-lggml-blas
		export WHISPER_LIBRARY_PATH:=$(WHISPER_LIBRARY_PATH):$(WHISPER_DIR)/build/ggml/src/ggml-blas
	else
		CMAKE_ARGS+=-DGGML_METAL=ON
		CMAKE_ARGS+=-DGGML_METAL_EMBED_LIBRARY=ON
		CMAKE_ARGS+=-DGGML_METAL_USE_BF16=ON
		CMAKE_ARGS+=-DGGML_OPENMP=OFF
		CMAKE_ARGS+=-DWHISPER_BUILD_EXAMPLES=OFF
		CMAKE_ARGS+=-DWHISPER_BUILD_TESTS=OFF
		CMAKE_ARGS+=-DWHISPER_BUILD_SERVER=OFF
		CGO_LDFLAGS += -framework Accelerate
		CGO_LDFLAGS_WHISPER+=-lggml-metal -lggml-blas
		export WHISPER_LIBRARY_PATH:=$(WHISPER_LIBRARY_PATH):$(WHISPER_DIR)/build/ggml/src/ggml-metal/:$(WHISPER_DIR)/build/ggml/src/ggml-blas
	endif
	TARGET+=--target ggml-metal
endif

ifneq (,$(findstring sycl,$(BUILD_TYPE)))
	export CC=icx
	export CXX=icpx
	CGO_LDFLAGS_WHISPER += -fsycl -L${DNNLROOT}/lib -rpath ${ONEAPI_ROOT}/${ONEAPI_VERSION}/lib -ldnnl ${MKLROOT}/lib/intel64/libmkl_sycl.a -fiopenmp -fopenmp-targets=spir64 -lOpenCL -lggml-sycl
	CGO_LDFLAGS_WHISPER += $(shell pkg-config --libs mkl-static-lp64-gomp)
	CGO_CXXFLAGS_WHISPER += -fiopenmp -fopenmp-targets=spir64
	CGO_CXXFLAGS_WHISPER += $(shell pkg-config --cflags mkl-static-lp64-gomp )
	export WHISPER_LIBRARY_PATH:=$(WHISPER_LIBRARY_PATH):$(WHISPER_DIR)/build/ggml/src/ggml-sycl/
	CMAKE_ARGS+=-DGGML_SYCL=ON \
		-DCMAKE_C_COMPILER=icx \
		-DCMAKE_CXX_COMPILER=icpx \
		-DCMAKE_CXX_FLAGS="-fsycl"
endif

ifeq ($(BUILD_TYPE),sycl_f16)
	CMAKE_ARGS+=-DGGML_SYCL_F16=ON
endif

ifneq ($(OS),Darwin)
	CGO_LDFLAGS_WHISPER+=-lgomp
endif

## whisper
sources/whisper.cpp:
	mkdir -p sources/whisper.cpp
	cd sources/whisper.cpp && \
	git init && \
	git remote add origin $(WHISPER_REPO) && \
	git fetch origin && \
	git checkout $(WHISPER_CPP_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

sources/whisper.cpp/build/src/libwhisper.a: sources/whisper.cpp
	cd sources/whisper.cpp && cmake $(CMAKE_ARGS) $(WHISPER_CMAKE_ARGS) . -B ./build
	cd sources/whisper.cpp/build && cmake --build . --config Release

whisper: sources/whisper.cpp sources/whisper.cpp/build/src/libwhisper.a
	$(GOCMD) mod edit -replace github.com/ggerganov/whisper.cpp=$(CURDIR)/sources/whisper.cpp
	$(GOCMD) mod edit -replace github.com/ggerganov/whisper.cpp/bindings/go=$(CURDIR)/sources/whisper.cpp/bindings/go
	CGO_LDFLAGS="$(CGO_LDFLAGS) $(CGO_LDFLAGS_WHISPER)" C_INCLUDE_PATH="${WHISPER_INCLUDE_PATH}" LIBRARY_PATH="${WHISPER_LIBRARY_PATH}" LD_LIBRARY_PATH="${WHISPER_LIBRARY_PATH}" \
	CGO_CXXFLAGS="$(CGO_CXXFLAGS_WHISPER)" \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o whisper ./

package:
	bash package.sh

build: whisper package
