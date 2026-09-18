KIMODO_REPO?=https://github.com/localai-org/kimodo.cpp
KIMODO_VERSION?=5679ff19ba0a522c0b0516e9a9d402fe1af2c027
BUILD_TYPE?=
GOCMD?=go
GO_TAGS?=
JOBS?=4
CMAKE_ARGS?=
BUILD_DIR?=build-$(if $(filter vulkan,$(BUILD_TYPE)),vulkan,cpu)
ifeq ($(shell uname -sm),Linux x86_64)
CPU_VARIANTS=$(BUILD_DIR)-avx2/.built
endif

CMAKE_ARGS+=-DCMAKE_BUILD_TYPE=Release -DGGML_NATIVE=OFF -DGGML_METAL=OFF
ifeq ($(shell uname -s),Darwin)
CMAKE_ARGS+=-DCMAKE_BUILD_WITH_INSTALL_RPATH=ON -DCMAKE_INSTALL_RPATH=@loader_path
endif
ifeq ($(BUILD_TYPE),vulkan)
CMAKE_ARGS+=-DKIMODO_ENABLE_VULKAN=ON
else
CMAKE_ARGS+=-DKIMODO_ENABLE_VULKAN=OFF
endif

.PHONY: all build package test clean purge FORCE
all: build

FORCE:

sources/kimodo.cpp/.localai-version: FORCE
	@test -d sources/kimodo.cpp/.git || git clone $(KIMODO_REPO) sources/kimodo.cpp
	@if test -f $@ && test "$$(git -C sources/kimodo.cpp rev-parse HEAD)" = "$(KIMODO_VERSION)"; then exit 0; fi; \
		cd sources/kimodo.cpp && (git diff --quiet && git diff --cached --quiet || \
		{ echo "kimodo.cpp has local source changes; preserve them and run make clean before updating the pin" >&2; exit 1; }) && \
		git fetch origin $(KIMODO_VERSION) && git checkout $(KIMODO_VERSION) && \
		git submodule update --init --recursive --depth 1 && touch .localai-version

$(BUILD_DIR)/.built: sources/kimodo.cpp/.localai-version CMakeLists.txt native/bridge.cpp Makefile
	cmake -S . -B $(BUILD_DIR) $(CMAKE_ARGS)
	cmake --build $(BUILD_DIR) --target kimodo -j$(JOBS)
	touch $@

$(BUILD_DIR)-avx2/.built: sources/kimodo.cpp/.localai-version CMakeLists.txt Makefile
	cmake -S . -B $(BUILD_DIR)-avx2 $(CMAKE_ARGS) -DGGML_AVX=ON -DGGML_AVX2=ON -DGGML_FMA=ON -DGGML_F16C=ON -DGGML_BMI2=ON
	cmake --build $(BUILD_DIR)-avx2 --target ggml-cpu -j$(JOBS)
	touch $@

kimodocpp: main.go kimodo.go glb.go $(BUILD_DIR)/.built $(CPU_VARIANTS)
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o $@ ./

package: kimodocpp
	BUILD_DIR=$(BUILD_DIR) bash package.sh

build: package

test:
	CGO_ENABLED=0 $(GOCMD) test -v ./...

purge:
	rm -rf build-native build-cpu build-vulkan build-native-avx2 build-cpu-avx2 build-vulkan-avx2

clean: purge
	rm -rf package kimodocpp sources
