# audio.cpp backend Makefile.
#
# Upstream pin lives below in the AUDIO_CPP_VERSION variable, so
# .github/bump_deps.sh can find and update it, matching the llama-cpp / ds4
# convention. That script seds every line matching the variable name followed by
# an assignment, so this comment deliberately spells the name on its own: a
# comment repeating the full assignment token gets rewritten and mangled by the
# first auto-bump (backend/cpp/ds4/Makefile shows the damage). The clone
# recipe is a make target (not a prepare.sh) so 'make purge && make' is a clean
# rebuild and so the bump bot can see the pin.

AUDIO_CPP_VERSION?=3407559d7c83b543bd60ca8fa2b630374e71083f
AUDIO_CPP_REPO?=https://github.com/0xShug0/audio.cpp

CURRENT_MAKEFILE_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
BUILD_DIR := build

BUILD_TYPE ?=
NATIVE ?= false
JOBS ?= $(shell nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)

UNAME_S := $(shell uname -s)

# AUDIOCPP_DEPLOYMENT_BUILD compiles the model_specs/*.json catalog into
# engine_runtime, so the shipped package needs no model_specs directory and a
# safetensors model tree still resolves its family spec.
CMAKE_ARGS ?= -DCMAKE_BUILD_TYPE=Release -DAUDIOCPP_DEPLOYMENT_BUILD=ON

# CMAKE_CUDA_ARCHITECTURES must be set explicitly for a cublas build, and this
# is not a tuning knob: upstream's CMakeLists sets CUDA_ARCHITECTURES to
# `native` on the engine_runtime target whenever the root-scope variable is
# unset (audio.cpp/CMakeLists.txt, the `if (CMAKE_CUDA_ARCHITECTURES)` branch
# next to the istft/torch_random .cu sources), and docs/build/linux.md says so
# outright: "Leave CMAKE_CUDA_ARCHITECTURES unset to build for the GPUs present
# at build time (native)". No CI runner has a GPU, so `native` has nothing to
# enumerate. ggml's own default (external/ggml/src/ggml-cuda/CMakeLists.txt)
# does not rescue this: it list(APPEND)s in the ggml subdirectory scope, which
# never reaches the root scope where the engine_runtime property is decided.
#
# The values below are ggml's list for the matching toolkit, copied rather than
# invented, so the two targets compile for exactly the same set:
#   - CUDA 13 drops the Maxwell/Pascal/Volta virtual archs (50/61/70).
#   - 121a-real needs CUDA >= 12.9, so the CUDA 12 list (built against 12.8)
#     stops at 120a-real.
#   - `a`-suffixed archs are used rather than ggml's rejected 120f-virtual: the
#     `f` suffix needs CMake >= 3.31.8, and Ubuntu Noble ships 3.28.3. The
#     3.28 validator (Modules/Internal/CMakeCUDAArchitecturesValidate.cmake)
#     accepts `[0-9]+a?(-real|-virtual)?`.
#
# Setting it here also pins ggml's copy, since its default is guarded by
# `if (NOT DEFINED CMAKE_CUDA_ARCHITECTURES)`. CUDA_MAJOR_VERSION is the CI
# build-arg, forwarded by Dockerfile.audio-cpp.
#
# An EMPTY major maps to `native`, NOT to the CUDA 12 list. Only CI declares a
# major; a developer running `BUILD_TYPE=cublas make` locally declares none, and
# the CUDA 12 list contains 120a-real, which needs nvcc >= 12.8. Falling through
# to it turned every local build on a CUDA 12.0-12.7 host into a compile error,
# where upstream's documented behaviour ("Leave CMAKE_CUDA_ARCHITECTURES unset
# to build for the GPUs present at build time") worked. `native` restores that.
# It does require a GPU to enumerate, so the escape hatch for a GPU-less local
# cross-build is to set CUDA_ARCHITECTURES on the command line, which the ?=
# assignments below leave untouched.
CUDA_MAJOR_VERSION ?=
ifeq ($(CUDA_MAJOR_VERSION),13)
    CUDA_ARCHITECTURES ?= 75-virtual;80-virtual;86-real;89-real;120a-real;121a-real
else ifeq ($(CUDA_MAJOR_VERSION),12)
    CUDA_ARCHITECTURES ?= 50-virtual;61-virtual;70-virtual;75-virtual;80-virtual;86-real;89-real;120a-real
else ifeq ($(CUDA_MAJOR_VERSION),)
    CUDA_ARCHITECTURES ?= native
else ifeq ($(BUILD_TYPE),cublas)
    # Gated on cublas because the variable means nothing to any other build, so a
    # stray CUDA_MAJOR_VERSION in the environment must not break `make clean` or
    # a CPU build. It does still error for `BUILD_TYPE=cublas make clean`, which
    # is the right trade: that invocation is asking about a CUDA build tree.
    $(error CUDA_MAJOR_VERSION=$(CUDA_MAJOR_VERSION) has no architecture list here (12 and 13 do). Leave it empty for a native build, or pass CUDA_ARCHITECTURES explicitly.)
endif

ifeq ($(BUILD_TYPE),cublas)
    CMAKE_ARGS += -DENGINE_ENABLE_CUDA=ON "-DCMAKE_CUDA_ARCHITECTURES=$(CUDA_ARCHITECTURES)"
else ifeq ($(BUILD_TYPE),vulkan)
    CMAKE_ARGS += -DENGINE_ENABLE_VULKAN=ON
else ifeq ($(UNAME_S),Darwin)
    # Metal. ggml embeds the shader library by default (GGML_METAL_EMBED_LIBRARY
    # defaults to GGML_METAL), so the package needs no .metallib beside the
    # binary. Darwin builds go through scripts/build/audio-cpp-darwin.sh.
    CMAKE_ARGS += -DENGINE_ENABLE_METAL=ON
    # AppleClang ships no OpenMP runtime and Homebrew's libomp is keg-only, so
    # neither libomp.dylib nor omp.h is symlinked into /opt/homebrew and CMake's
    # FindOpenMP cannot find them on its own (the workflow's `brew link libomp`
    # is a no-op for a keg-only formula, and its failure is swallowed).
    # audio.cpp calls find_package(OpenMP REQUIRED COMPONENTS CXX) whenever
    # ENGINE_ENABLE_OPENMP is ON, so with no hint the macOS build dies at
    # configure time before compiling anything. OpenMP_ROOT is honoured by the
    # find_library/find_path calls inside FindOpenMP under CMP0074, which is NEW
    # here because audio.cpp requires CMake 3.20.
    #
    # If the keg is absent, turn OpenMP off rather than fail: the tree's only
    # <omp.h> include is guarded by #ifdef _OPENMP and a #pragma omp without
    # -fopenmp is simply ignored, so an OpenMP-less build is CORRECT. It is not
    # cheap, though: 108 `#pragma omp` directives across ~30 files (roformer,
    # demucs, chatterbox, moss, supertonic, seed_vc, framework/audio/dsp) are
    # compiled out, and clang says nothing about an ignored omp pragma unless
    # -Wsource-uses-openmp is on. A green package that is quietly single-threaded
    # in every host DSP loop gets blamed on Metal, not on packaging, so the
    # fallback announces itself.
    ifeq ($(origin LIBOMP_PREFIX),undefined)
        LIBOMP_PREFIX := $(shell brew --prefix libomp 2>/dev/null)
    endif
    # Nested ifneq rather than $(and ...): $(and) needs GNU make 3.81, and while
    # that is what Apple ships, an older make expands it to empty and would take
    # the OpenMP-OFF branch with no way to tell that from a genuinely missing
    # keg. Two plain conditionals cannot fail that way.
    LIBOMP_USABLE :=
    ifneq ($(wildcard $(LIBOMP_PREFIX)/lib/libomp.dylib),)
        ifneq ($(wildcard $(LIBOMP_PREFIX)/include/omp.h),)
            LIBOMP_USABLE := yes
        endif
    endif
    ifeq ($(LIBOMP_USABLE),yes)
        CMAKE_ARGS += "-DOpenMP_ROOT=$(LIBOMP_PREFIX)"
    else
        $(warning audio-cpp: libomp not found at '$(LIBOMP_PREFIX)'; building without OpenMP (single-threaded host DSP). Install it with `brew install libomp`, or set LIBOMP_PREFIX.)
        CMAKE_ARGS += -DENGINE_ENABLE_OPENMP=OFF
    endif
else
    # Portable Linux CPU. Upstream wires this to GGML_BACKEND_DL +
    # GGML_CPU_ALL_VARIANTS + $ORIGIN rpath, so one build serves every CPU
    # tier instead of an AVX-tier image fan-out.
    CMAKE_ARGS += -DENGINE_ENABLE_CPU_ALL_VARIANTS=ON
endif

ifneq ($(NATIVE),true)
    CMAKE_ARGS += -DENGINE_ENABLE_NATIVE_CPU=OFF
endif

.PHONY: all grpc-server package test test-engine clean purge
all: grpc-server

# Clone the upstream source at the pinned commit. The directory is the target
# so make only re-clones when it is missing. After bumping AUDIO_CPP_VERSION,
# run 'make purge && make' to refetch.
audio.cpp:
	mkdir -p audio.cpp
	cd audio.cpp && \
	git init -q && \
	git remote add origin $(AUDIO_CPP_REPO) && \
	git fetch --depth 1 origin $(AUDIO_CPP_VERSION) && \
	git checkout FETCH_HEAD

grpc-server: audio.cpp
	mkdir -p $(BUILD_DIR)
	cd $(BUILD_DIR) && cmake $(CMAKE_ARGS) $(CURRENT_MAKEFILE_DIR) && \
	cmake --build . --config Release -j $(JOBS)
	cp $(BUILD_DIR)/grpc-server grpc-server

package: grpc-server
	bash package.sh

test:
	@echo "audio-cpp: standalone unit tests run from the repo root via 'make test-backend-cpp'"

# Engine-linked tests. Needs the upstream checkout and a full engine build.
test-engine: audio.cpp
	mkdir -p $(BUILD_DIR)
	cd $(BUILD_DIR) && cmake $(CMAKE_ARGS) -DAUDIO_CPP_GRPC_BUILD_TESTS=ON $(CURRENT_MAKEFILE_DIR) && \
	cmake --build . --config Release -j $(JOBS) && ctest --output-on-failure --no-tests=error

clean:
	rm -rf $(BUILD_DIR) grpc-server package

purge: clean
	rm -rf audio.cpp
