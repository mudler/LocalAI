KIMODO_REPO?=https://github.com/localai-org/kimodo.cpp
KIMODO_VERSION?=568b0253f346fbe369587c7dae73d58594a14c90
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

.PHONY: all build package test clean purge
all: build

sources/kimodo.cpp:
	git clone $(KIMODO_REPO) $@
	cd $@ && git checkout $(KIMODO_VERSION) && git submodule update --init --recursive --depth 1

sources/kimodo.cpp/.localai-patched: sources/kimodo.cpp patches/text-threads.patch
	cd sources/kimodo.cpp && (git apply --check ../../patches/text-threads.patch && git apply ../../patches/text-threads.patch || git apply --reverse --check ../../patches/text-threads.patch)
	touch $@

$(BUILD_DIR)/.built: sources/kimodo.cpp/.localai-patched CMakeLists.txt native/bridge.cpp Makefile
	cmake -S . -B $(BUILD_DIR) $(CMAKE_ARGS)
	cmake --build $(BUILD_DIR) --target kimodo -j$(JOBS)
	touch $@

$(BUILD_DIR)-avx2/.built: sources/kimodo.cpp/.localai-patched CMakeLists.txt Makefile
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
