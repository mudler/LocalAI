SKINTOKENS_REPO?=https://github.com/localai-org/skin-tokens.cpp
SKINTOKENS_VERSION?=43aac7b0fe45f5cd802365e6964c13bfc5e5fb52
BUILD_TYPE?=
GOCMD?=go
GO_TAGS?=
JOBS?=4
CMAKE_ARGS?=
BUILD_DIR?=build-$(if $(filter vulkan,$(BUILD_TYPE)),vulkan,cpu)

CMAKE_ARGS+=-DCMAKE_BUILD_TYPE=Release -DGGML_NATIVE=OFF -DGGML_METAL=OFF -DSKINTOKENS_CPU_ALL_VARIANTS=ON
ifeq ($(shell uname -s),Darwin)
CMAKE_ARGS+=-DCMAKE_BUILD_WITH_INSTALL_RPATH=ON -DCMAKE_INSTALL_RPATH=@loader_path
endif
ifeq ($(BUILD_TYPE),vulkan)
CMAKE_ARGS+=-DSKINTOKENS_ENABLE_VULKAN=ON
else
CMAKE_ARGS+=-DSKINTOKENS_ENABLE_VULKAN=OFF
endif

.PHONY: all build package test clean purge FORCE
all: build

FORCE:

sources/skin-tokens.cpp/.localai-version: FORCE
	@test -d sources/skin-tokens.cpp/.git || git clone $(SKINTOKENS_REPO) sources/skin-tokens.cpp
	@if test -f $@ && test "$$(git -C sources/skin-tokens.cpp rev-parse HEAD)" = "$(SKINTOKENS_VERSION)"; then exit 0; fi; \
		cd sources/skin-tokens.cpp && (git diff --quiet && git diff --cached --quiet || \
		{ echo "skin-tokens.cpp has local source changes; preserve them and run make clean before updating the pin" >&2; exit 1; }) && \
		git fetch origin $(SKINTOKENS_VERSION) && git checkout $(SKINTOKENS_VERSION) && \
		git submodule update --init --recursive --depth 1 && touch .localai-version

$(BUILD_DIR)/.built: sources/skin-tokens.cpp/.localai-version CMakeLists.txt native/bridge.cpp Makefile
	cmake -S . -B $(BUILD_DIR) $(CMAKE_ARGS)
	cmake --build $(BUILD_DIR) --target skintokens -j$(JOBS)
	touch $@

skintokenscpp: main.go skintokens.go materials.go $(BUILD_DIR)/.built
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o $@ ./

package: skintokenscpp
	BUILD_DIR=$(BUILD_DIR) bash package.sh

build: package

test:
	CGO_ENABLED=0 $(GOCMD) test -v ./...

purge:
	rm -rf build-native build-cpu build-vulkan build-native-avx2 build-cpu-avx2 build-vulkan-avx2

clean: purge
	rm -rf package skintokenscpp sources
