GEMX_REPO?=https://github.com/localai-org/gem-x.cpp
GEMX_VERSION?=576cab62d18829ae00df5959b8ccf0a1032e0c87
BUILD_TYPE?=
GOCMD?=go
GO_TAGS?=
JOBS?=4
CMAKE_ARGS?=
BUILD_DIR?=build-$(if $(filter vulkan,$(BUILD_TYPE)),vulkan,cpu)

CMAKE_ARGS+=-DCMAKE_BUILD_TYPE=Release -DGGML_NATIVE=OFF -DGGML_METAL=OFF
ifeq ($(shell uname -s),Darwin)
CMAKE_ARGS+=-DCMAKE_BUILD_WITH_INSTALL_RPATH=ON -DCMAKE_INSTALL_RPATH=@loader_path
endif
ifeq ($(BUILD_TYPE),vulkan)
CMAKE_ARGS+=-DGEMX_VULKAN=ON
else
CMAKE_ARGS+=-DGEMX_VULKAN=OFF
endif

.PHONY: all build package test clean purge FORCE
all: build

FORCE:

sources/gem-x.cpp/.checkout-ready: FORCE
	@test -d sources/gem-x.cpp/.git || git clone $(GEMX_REPO) sources/gem-x.cpp
	@if test -f $@ && test "$$(git -C sources/gem-x.cpp rev-parse HEAD)" = "$(GEMX_VERSION)"; then exit 0; fi; \
		cd sources/gem-x.cpp && (git diff --quiet && git diff --cached --quiet || \
		{ echo "gem-x.cpp has local source changes; preserve them and run make clean before updating the pin" >&2; exit 1; }) && \
		git fetch origin $(GEMX_VERSION) && git checkout $(GEMX_VERSION) && \
		git submodule update --init --recursive --depth 1 && touch .checkout-ready

$(BUILD_DIR)/.built: sources/gem-x.cpp/.checkout-ready CMakeLists.txt native/bridge.cpp Makefile
	cmake -S . -B $(BUILD_DIR) $(CMAKE_ARGS)
	cmake --build $(BUILD_DIR) -j$(JOBS)
	touch $@


gemxcpp: main.go gemx.go native.go image_projection.go $(BUILD_DIR)/.built
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o $@ ./

package: gemxcpp
	BUILD_DIR=$(BUILD_DIR) bash package.sh

build: package

test:
	CGO_ENABLED=0 $(GOCMD) test -v ./...

purge:
	rm -rf build-native build-cpu build-vulkan build-native-avx2 build-cpu-avx2 build-vulkan-avx2

clean: purge
	rm -rf package gemxcpp sources
