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
TRELLIS2CPP_VERSION?=62d19ac9ea234a224c4f061e68b804cf56726abb

# CGAL Alpha Wrap print remeshing (t2_prepare_print_mesh /
# t2_bake_projected_glb). Header-only in the configuration we use: without
# GMP/MPFR on the system, CGAL >= 6 selects its Boost multiprecision
# exact-arithmetic backend, so libtrellis2 gains no new shared-library
# dependencies and package.sh needs no changes. Both archives are plain
# header drops fetched like sherpa-onnx fetches onnxruntime.
CGAL_VERSION?=6.1.1
BOOST_VERSION?=1.88.0
BOOST_UNDERSCORE=$(subst .,_,$(BOOST_VERSION))

# libtrellis2 + ggml as shared libraries; no example/test binaries.
CMAKE_ARGS+=-DCMAKE_BUILD_TYPE=Release
CMAKE_ARGS+=-DBUILD_SHARED_LIBS=ON
CMAKE_ARGS+=-DTRELLIS2_BUILD_EXAMPLES=OFF
CMAKE_ARGS+=-DTRELLIS2_BUILD_TESTS=OFF
CMAKE_ARGS+=-DCGAL_DIR=$(CURRENT_DIR)/sources/cgal
CMAKE_ARGS+=-DBoost_INCLUDE_DIR=$(CURRENT_DIR)/sources/boost
# CMake >= 3.30 removed module-mode FindBoost, which is the only mode that can
# find a bare header drop (the raw boost tarball has no BoostConfig.cmake).
# CMP0167=OLD restores it there; older CMakes ignore the unknown policy.
CMAKE_ARGS+=-DCMAKE_POLICY_DEFAULT_CMP0167=OLD

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

# The -library bundle is CGAL's supported header-only distribution (its
# CGALConfig.cmake sits at the bundle root, hence CGAL_DIR above). Zip rather
# than .tar.xz because Dockerfile.golang guarantees unzip but not xz.
# CGAL_SetupBoost.cmake pins cmake_policy(VERSION 3.12...3.30), which on
# CMake >= 3.30 forces CMP0167 NEW right before its find_package(Boost) —
# making the lookup config-only, which a bare header drop can never satisfy.
# Capping the range at 3.29 leaves CMP0167 to the OLD default passed above,
# so the module-mode FindBoost finds Boost_INCLUDE_DIR on every CMake.
sources/cgal:
	mkdir -p sources
	curl -L https://github.com/CGAL/cgal/releases/download/v$(CGAL_VERSION)/CGAL-$(CGAL_VERSION)-library.zip -o sources/cgal.zip
	cd sources && unzip -q cgal.zip -d cgal-tmp && rm cgal.zip && \
		mv cgal-tmp/* cgal && rmdir cgal-tmp
	sed -i.bak 's/cmake_policy(VERSION 3.12...3.30)/cmake_policy(VERSION 3.12...3.29)/' \
		sources/cgal/cmake/modules/CGAL_SetupBoost.cmake && \
		rm sources/cgal/cmake/modules/CGAL_SetupBoost.cmake.bak

# Only the boost/ header tree is extracted (~200MB instead of the full 1GB+
# source tree with tools and docs).
sources/boost:
	mkdir -p sources
	curl -L https://archives.boost.io/release/$(BOOST_VERSION)/source/boost_$(BOOST_UNDERSCORE).tar.gz -o sources/boost.tgz
	cd sources && mkdir boost && \
		tar -xzf boost.tgz -C boost --strip-components=1 boost_$(BOOST_UNDERSCORE)/boost && \
		rm boost.tgz

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
variants/%/.built: sources/trellis2cpp sources/cgal sources/boost
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
