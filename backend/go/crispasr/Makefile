CMAKE_ARGS?=
BUILD_TYPE?=
NATIVE?=false

GOCMD?=go
GO_TAGS?=
JOBS?=$(shell nproc --ignore=1)

# CrispASR version (release tag)
CRISPASR_REPO?=https://github.com/CrispStrobe/CrispASR
CRISPASR_VERSION?=c96281d6d409a7f97edbce62c12a6dd2f4da6a92
SO_TARGET?=libgocrispasr.so

CMAKE_ARGS+=-DBUILD_SHARED_LIBS=OFF
# Keep the build lean: no tests/examples/server/SDL2/curl/ffmpeg (the FROM scratch
# image cannot satisfy those runtime deps). All ASR/TTS model backends stay enabled.
CMAKE_ARGS+=-DCRISPASR_BUILD_TESTS=OFF -DCRISPASR_BUILD_EXAMPLES=OFF -DCRISPASR_BUILD_SERVER=OFF
CMAKE_ARGS+=-DCRISPASR_SDL2=OFF -DCRISPASR_CURL=OFF -DCRISPASR_FFMPEG=OFF

ifeq ($(NATIVE),false)
	CMAKE_ARGS+=-DGGML_NATIVE=OFF
endif

ifeq ($(BUILD_TYPE),cublas)
	CMAKE_ARGS+=-DGGML_CUDA=ON
else ifeq ($(BUILD_TYPE),openblas)
	CMAKE_ARGS+=-DGGML_BLAS=ON -DGGML_BLAS_VENDOR=OpenBLAS
else ifeq ($(BUILD_TYPE),clblas)
	CMAKE_ARGS+=-DGGML_CLBLAST=ON -DCLBlast_DIR=/some/path
else ifeq ($(BUILD_TYPE),hipblas)
	CMAKE_ARGS+=-DGGML_HIPBLAS=ON
else ifeq ($(BUILD_TYPE),vulkan)
	CMAKE_ARGS+=-DGGML_VULKAN=ON
else ifeq ($(OS),Darwin)
	ifneq ($(BUILD_TYPE),metal)
		CMAKE_ARGS+=-DGGML_METAL=OFF
	else
		CMAKE_ARGS+=-DGGML_METAL=ON
		CMAKE_ARGS+=-DGGML_METAL_EMBED_LIBRARY=ON
	endif
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

sources/CrispASR:
	mkdir -p sources/CrispASR
	cd sources/CrispASR && \
	git init && \
	git remote add origin $(CRISPASR_REPO) && \
	git fetch origin && \
	git checkout $(CRISPASR_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch
	# CrispASR's src/CMakeLists.txt locates its vendored llama.cpp
	# (crispasr-llama-core, used by the chat C-ABI) and the c2pa-audio
	# submodule (crispasr_c2pa_native, the pure-C++ C2PA signer) via
	# ${CMAKE_SOURCE_DIR}, which assumes CrispASR is the top-level CMake
	# project. We add_subdirectory it, so ${CMAKE_SOURCE_DIR} is THIS backend
	# dir and those sources aren't found. Rewrite to ${PROJECT_SOURCE_DIR}
	# (the crispasr project root), which is correct both standalone and as a
	# subproject. Idempotent.
	sed -i.bak \
		-e 's#\$${CMAKE_SOURCE_DIR}/examples/talk-llama#\$${PROJECT_SOURCE_DIR}/examples/talk-llama#' \
		-e 's#\$${CMAKE_SOURCE_DIR}/third_party/c2pa-audio#\$${PROJECT_SOURCE_DIR}/third_party/c2pa-audio#' \
		sources/CrispASR/src/CMakeLists.txt && rm -f sources/CrispASR/src/CMakeLists.txt.bak

# Detect OS
UNAME_S := $(shell uname -s)

ifeq ($(UNAME_S),Linux)
	VARIANT_TARGETS = libgocrispasr-avx.so libgocrispasr-avx2.so libgocrispasr-avx512.so libgocrispasr-fallback.so
else
	# On non-Linux (e.g., Darwin), build only fallback variant (as a dylib)
	VARIANT_TARGETS = libgocrispasr-fallback.dylib
endif

crispasr: main.go gocrispasr.go $(VARIANT_TARGETS)
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o crispasr ./

package: crispasr
	bash package.sh

build: package

clean: purge
	rm -rf libgocrispasr*.so libgocrispasr*.dylib package sources/CrispASR crispasr

purge:
	rm -rf build*

ifeq ($(UNAME_S),Linux)
libgocrispasr-avx.so: sources/CrispASR
	$(MAKE) purge
	$(info ${GREEN}I crispasr build info:avx${RESET})
	SO_TARGET=libgocrispasr-avx.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) libgocrispasr-custom
	rm -rfv build*

libgocrispasr-avx2.so: sources/CrispASR
	$(MAKE) purge
	$(info ${GREEN}I crispasr build info:avx2${RESET})
	SO_TARGET=libgocrispasr-avx2.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=off -DGGML_FMA=on -DGGML_F16C=on -DGGML_BMI2=on" $(MAKE) libgocrispasr-custom
	rm -rfv build*

libgocrispasr-avx512.so: sources/CrispASR
	$(MAKE) purge
	$(info ${GREEN}I crispasr build info:avx512${RESET})
	SO_TARGET=libgocrispasr-avx512.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=on -DGGML_AVX2=on -DGGML_AVX512=on -DGGML_FMA=on -DGGML_F16C=on -DGGML_BMI2=on" $(MAKE) libgocrispasr-custom
	rm -rfv build*
endif

libgocrispasr-fallback.so: sources/CrispASR
	$(MAKE) purge
	$(info ${GREEN}I crispasr build info:fallback${RESET})
	SO_TARGET=libgocrispasr-fallback.so CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) libgocrispasr-custom
	rm -rfv build*

# Build fallback variant as a dylib (Darwin)
libgocrispasr-fallback.dylib: sources/CrispASR
	$(MAKE) purge
	$(info ${GREEN}I crispasr build info:fallback (dylib)${RESET})
	SO_TARGET=libgocrispasr-fallback.dylib CMAKE_ARGS="$(CMAKE_ARGS) -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off" $(MAKE) libgocrispasr-custom
	rm -rfv build*

libgocrispasr-custom: CMakeLists.txt cpp/crispasr_shim.cpp cpp/crispasr_shim.h
	mkdir -p build-$(SO_TARGET) && \
	cd build-$(SO_TARGET) && \
	cmake .. $(CMAKE_ARGS) && \
	cmake --build . --config Release -j$(JOBS) && \
	cd .. && \
	(mv build-$(SO_TARGET)/libgocrispasr.so ./$(SO_TARGET) 2>/dev/null || \
	 mv build-$(SO_TARGET)/libgocrispasr.dylib ./$(SO_TARGET) 2>/dev/null)

test: crispasr
	CGO_ENABLED=0 $(GOCMD) test -v ./...

all: crispasr package
