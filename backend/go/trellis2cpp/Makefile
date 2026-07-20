CMAKE_ARGS?=
BUILD_TYPE?=
NATIVE?=false

CURRENT_DIR=$(abspath ./)
GOCMD?=go
GO_TAGS?=
JOBS?=$(shell nproc --ignore=1)

# trellis2.cpp — C++/ggml port of Microsoft TRELLIS.2 (image -> 3D GLB).
# The ggml submodule is pinned by trellis2cpp's .gitmodules and fetched via
# --recursive. The commit pin lives here so bump_deps.yaml can update it.
TRELLIS2CPP_REPO?=https://github.com/localai-org/trellis2cpp
TRELLIS2CPP_VERSION?=73dfbe5dfc2cbefd0950853718086556c6d9b043

# libtrellis2 + ggml as shared libraries; no example/test binaries.
CMAKE_ARGS+=-DCMAKE_BUILD_TYPE=Release
CMAKE_ARGS+=-DBUILD_SHARED_LIBS=ON
CMAKE_ARGS+=-DTRELLIS2_BUILD_EXAMPLES=OFF
CMAKE_ARGS+=-DTRELLIS2_BUILD_TESTS=OFF
# Print remeshing is part of trellis2cpp's ABI, so upstream owns the tested
# CGAL/Boost versions, checksums, fetch logic, and update automation. LocalAI
# only opts into that dependency set and pins the trellis2cpp commit above.
CMAKE_ARGS+=-DTRELLIS2_FETCH_PRINT_REMESH_DEPS=ON
CMAKE_ARGS+=-DTRELLIS2_PRINT_REMESH_DEPS_DIR=$(CURRENT_DIR)/sources/print-remesh-deps

ifeq ($(NATIVE),false)
	CMAKE_ARGS+=-DGGML_NATIVE=OFF
endif

ifeq ($(BUILD_TYPE),cublas)
	CMAKE_ARGS+=-DGGML_CUDA=ON
else ifeq ($(BUILD_TYPE),vulkan)
	CMAKE_ARGS+=-DGGML_VULKAN=ON
else ifeq ($(BUILD_TYPE),hipblas)
	ROCM_HOME ?= /opt/rocm
	ROCM_PATH ?= /opt/rocm
	export CXX=$(ROCM_HOME)/llvm/bin/clang++
	export CC=$(ROCM_HOME)/llvm/bin/clang
	AMDGPU_TARGETS?=gfx908,gfx90a,gfx942,gfx950,gfx1030,gfx1100,gfx1101,gfx1102,gfx1200,gfx1201
	CMAKE_ARGS+=-DGGML_HIP=ON -DAMDGPU_TARGETS=$(AMDGPU_TARGETS)
else ifeq ($(OS),Darwin)
	ifneq ($(BUILD_TYPE),metal)
		CMAKE_ARGS+=-DTRELLIS2_METAL=OFF -DGGML_METAL=OFF
	else
		# trellis2cpp turns on GGML_METAL(+EMBED_LIBRARY) itself when
		# TRELLIS2_METAL is enabled on Apple platforms.
		CMAKE_ARGS+=-DTRELLIS2_METAL=ON
	endif
	# Dependent libggml*.dylib resolve next to libtrellis2.dylib even
	# without DYLD_LIBRARY_PATH being exported.
	CMAKE_ARGS+=-DCMAKE_BUILD_WITH_INSTALL_RPATH=ON -DCMAKE_INSTALL_RPATH=@loader_path
endif

ifeq ($(BUILD_TYPE),sycl_f16)
	CMAKE_ARGS+=-DGGML_SYCL=ON \
		-DCMAKE_C_COMPILER=icx \
		-DCMAKE_CXX_COMPILER=icpx \
		-DGGML_SYCL_F16=ON
endif

ifeq ($(BUILD_TYPE),sycl_f32)
	CMAKE_ARGS+=-DGGML_SYCL=ON \
		-DCMAKE_C_COMPILER=icx \
		-DCMAKE_CXX_COMPILER=icpx
endif

sources/trellis2cpp:
	git clone --recursive $(TRELLIS2CPP_REPO) sources/trellis2cpp && \
	cd sources/trellis2cpp && \
	git checkout $(TRELLIS2CPP_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

# Detect OS
UNAME_S := $(shell uname -s)
UNAME_M := $(shell uname -m)

# The AVX variants are x86-only. ARM64 images use the portable fallback while
# still enabling the selected GPU backend (Vulkan/CUDA) through CMAKE_ARGS.
ifeq ($(UNAME_S),Linux)
	ifneq (,$(filter x86_64 amd64,$(UNAME_M)))
		VARIANTS = avx avx2 avx512 fallback
	else
		VARIANTS = fallback
	endif
else
	# On non-Linux (e.g., Darwin), build only the fallback variant
	VARIANTS = fallback
endif
VARIANT_TARGETS = $(foreach v,$(VARIANTS),variants/$(v)/.built)

VARIANT_FLAGS_avx      = -DGGML_AVX=on  -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off
VARIANT_FLAGS_avx2     = -DGGML_AVX=on  -DGGML_AVX2=on  -DGGML_AVX512=off -DGGML_FMA=on  -DGGML_F16C=on  -DGGML_BMI2=on
VARIANT_FLAGS_avx512   = -DGGML_AVX=on  -DGGML_AVX2=on  -DGGML_AVX512=on  -DGGML_FMA=on  -DGGML_F16C=on  -DGGML_BMI2=on
VARIANT_FLAGS_fallback = -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off

# libtrellis2 links libggml/libggml-base/libggml-cpu (+ the GPU backend) by
# soname, and those sonames collide across SIMD variants — so each variant
# lives in its own directory and run.sh selects one via LD_LIBRARY_PATH,
# unlike stablediffusion-ggml's flat renamed-.so scheme.
variants/%/.built: sources/trellis2cpp
	rm -rf build-$* variants/$*
	mkdir -p build-$* variants/$*
	cd build-$* && cmake ../sources/trellis2cpp $(CMAKE_ARGS) $(VARIANT_FLAGS_$*) && \
		cmake --build . --config Release -j$(JOBS)
	@for f in build-$*/libtrellis2.so build-$*/libtrellis2.dylib; do \
		if [ -e $$f ]; then cp -a $$f variants/$*/; fi; done
	find build-$*/ggml \( -name 'libggml*.so*' -o -name 'libggml*.dylib' \) -exec cp -a {} variants/$*/ \;
	rm -rf build-$*
	touch $@

trellis2cpp: main.go trellis2.go $(VARIANT_TARGETS)
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o trellis2cpp ./

package: trellis2cpp
	bash package.sh

build: package

clean: purge
	rm -rf variants trellis2cpp package sources

purge:
	rm -rf build-*

# Weight-free by construction: pure-Go unit tests over model-path resolution,
# validation, and request-parameter mapping. The multi-GB GGUF weights are
# never downloaded in CI; end-to-end generation is exercised manually.
test:
	$(GOCMD) test -v ./...

all: trellis2cpp package
