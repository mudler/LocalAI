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

AUDIO_CPP_VERSION?=e800d435d130dc776baf6f3e6129bb62b1495c89
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

ifeq ($(BUILD_TYPE),cublas)
    CMAKE_ARGS += -DENGINE_ENABLE_CUDA=ON
else ifeq ($(BUILD_TYPE),vulkan)
    CMAKE_ARGS += -DENGINE_ENABLE_VULKAN=ON
else ifeq ($(UNAME_S),Darwin)
    CMAKE_ARGS += -DENGINE_ENABLE_METAL=ON
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
