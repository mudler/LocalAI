# nemo-speech-cpp backend Makefile.
#
# Upstream pin lives below as NEMO_SPEECH_VERSION so .github/bump_deps.sh can
# find and update it, matching the parakeet-cpp / vibevoice-cpp convention.

NEMO_SPEECH_VERSION?=2e12e2def8a98ed06666f7ee3ca94e7193e04be4
NEMO_SPEECH_REPO?=https://github.com/NVIDIA/NeMo-Speech.cpp

GOCMD?=go
GO_TAGS?=
JOBS?=$(shell nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)

BUILD_TYPE?=
NATIVE?=false

# NEMO_SPEECH_CUBLAS_SHIM defaults ON upstream and builds a drop-in
# libcublas.so.13. LocalAI's CUDA images ship the real cuBLAS, so the shim would
# shadow it with a slower native GEMM. Always OFF here.
CMAKE_ARGS?=-DCMAKE_BUILD_TYPE=Release \
	-DBUILD_SHARED_LIBS=OFF \
	-DCMAKE_POSITION_INDEPENDENT_CODE=ON \
	-DNEMO_SPEECH_CUBLAS_SHIM=OFF \
	-DNEMO_SPEECH_BUILD_ASR=ON \
	-DNEMO_SPEECH_BUILD_DIAR=ON \
	-DNEMO_SPEECH_BUILD_TTS=ON \
	-DNEMO_SPEECH_BUILD_NMT=ON \
	-DNEMO_SPEECH_BUILD_CLI=OFF \
	-DNEMO_SPEECH_BUILD_HTTP=OFF \
	-DNEMO_SPEECH_BUILD_GRPC=OFF \
	-DNEMO_SPEECH_WITH_FLASHLIGHT=OFF \
	-DNEMO_SPEECH_TTS_WITH_ZH=ON \
	-DNEMO_SPEECH_TTS_WITH_JA=ON

ifeq ($(NATIVE),false)
	CMAKE_ARGS+=-DGGML_NATIVE=OFF
endif

# Sparrowhawk/OpenFST assume a GNU toolchain and the gcc-12 pin has no macOS
# analogue, so text normalization is Linux-only. Documented gap, see the spec.
ifeq ($(shell uname -s),Darwin)
	WITH_NORM?=OFF
else
	WITH_NORM?=ON
endif
CMAKE_ARGS+=-DNEMO_SPEECH_WITH_NORM=$(WITH_NORM)

ifeq ($(BUILD_TYPE),cublas)
	CMAKE_ARGS+=-DGGML_CUDA=ON
else ifeq ($(BUILD_TYPE),vulkan)
	CMAKE_ARGS+=-DGGML_VULKAN=ON
else ifeq ($(BUILD_TYPE),metal)
	CMAKE_ARGS+=-DGGML_METAL=ON
endif

.PHONY: nemo-speech-cpp-grpc package build clean purge test all

all: nemo-speech-cpp-grpc

sources/NeMo-Speech.cpp:
	mkdir -p sources
	cd sources && git clone $(NEMO_SPEECH_REPO) NeMo-Speech.cpp
	cd sources/NeMo-Speech.cpp && git checkout $(NEMO_SPEECH_VERSION)
	# NMT links llama.cpp; ja needs open_jtalk; zh needs cppjieba. flashlight and
	# kenlm are deliberately not initialized, they are out of scope.
	cd sources/NeMo-Speech.cpp && git submodule update --init --recursive \
		ggml llama.cpp third_party/open_jtalk third_party/cppjieba third_party/cpp-httplib

# The prerequisite is order-only. cmake writes into the checkout, which bumps the
# directory mtime past the sentinel, and a plain prerequisite would then re-run
# git apply over an already-patched tree and fail every incremental build.
sources/NeMo-Speech.cpp/.patched: | sources/NeMo-Speech.cpp
	# NEMO_SPEECH_GGML_PATCHED defaults ON and silently assumes these are applied.
	# An unpatched checkout builds fine and produces wrong CUDA encoder output, so
	# a failure here must stop the build rather than warn.
	cd sources/NeMo-Speech.cpp && for p in ggml-patches/*.patch; do \
		git -C ggml apply "$$PWD/$$p" || exit 1; \
	done
	touch $@

# Upstream sets CMAKE_LIBRARY_OUTPUT_DIRECTORY to ${CMAKE_BINARY_DIR}/bin, so the
# shared objects land in build/bin rather than at the top of the build tree.
sources/NeMo-Speech.cpp/build/bin/libnemo_speech_asr_c.so: sources/NeMo-Speech.cpp/.patched
ifeq ($(WITH_NORM),ON)
	# OpenFST's templates ICE on gcc-13/14 at -O2, so this one step pins gcc-12
	# while the runtime itself builds with the image default.
	cd sources/NeMo-Speech.cpp && CC=gcc-12 CXX=g++-12 JOBS=$(JOBS) scripts/build_itn_deps.sh
endif
	cd sources/NeMo-Speech.cpp && cmake -B build -G Ninja $(CMAKE_ARGS)
	cd sources/NeMo-Speech.cpp && cmake --build build -j$(JOBS)

# ASR and NMT build a dedicated _c shared object that links the C++ implementation
# in privately. TTS does not: upstream compiles its c_api.cpp straight into
# libnemo_speech_tts and only aliases the nemo_speech_tts_c CMake target, so the
# TTS C ABI ships without the _c suffix.
build: sources/NeMo-Speech.cpp/build/bin/libnemo_speech_asr_c.so
	# -a keeps the SOVERSION symlink a symlink instead of duplicating the payload.
	cp -af sources/NeMo-Speech.cpp/build/bin/libnemo_speech_asr_c.* .
	cp -af sources/NeMo-Speech.cpp/build/bin/libnemo_speech_tts.* .
	cp -af sources/NeMo-Speech.cpp/build/bin/libnemo_speech_nmt_c.* .
	# The _c libraries are thin ABI shims with a DT_NEEDED on the C++
	# implementation DSO, so dlopen fails without these next to them. TTS needs
	# no counterpart, its implementation and ABI live in the same object.
	cp -af sources/NeMo-Speech.cpp/build/bin/libnemo_speech_asr.* .
	cp -af sources/NeMo-Speech.cpp/build/bin/libnemo_speech_nmt.* .

nemo-speech-cpp-grpc: build
	$(GOCMD) build -tags "$(GO_TAGS)" -o nemo-speech-cpp-grpc .

test:
	$(GOCMD) test -v ./...

package: nemo-speech-cpp-grpc
	bash package.sh

clean:
	# The SOVERSION suffix means the real payload is libnemo_speech_*.so.1, so
	# the globs have to reach past the .so.
	rm -f nemo-speech-cpp-grpc libnemo_speech_*.so* libnemo_speech_*.dylib
	rm -rf sources/NeMo-Speech.cpp/build

purge: clean
	rm -rf sources
