# nemo-speech-cpp backend Makefile.
#
# Upstream pin lives below as NEMO_SPEECH_VERSION so .github/bump_deps.sh can
# find and update it, matching the parakeet-cpp / vibevoice-cpp convention.
#
# Bumping NEMO_SPEECH_VERSION is a no-op on an existing checkout: sources/ is a
# directory target, so make only clones when it is missing and never re-checks
# out an already-cloned tree. After a bump run 'make purge && make', the same
# rule the parakeet-cpp Makefile documents.
#
# 'build' is the entry point the backend image calls (backend/Dockerfile.golang
# runs 'make -C backend/go/$(BACKEND) build' and then copies package/), so it
# has to produce the binary and the package, not just the shared libraries.

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

# scripts/build_itn_deps.sh installs the Sparrowhawk/OpenFST runtime here.
# NEMO_SPEECH_DEPENDENCY_PREFIX defaults to <src>/.deps upstream, and the ITN
# stack goes under its itn/ subdirectory.
ITN_LIB_DIR=sources/NeMo-Speech.cpp/.deps/itn/lib

ifeq ($(BUILD_TYPE),cublas)
	CMAKE_ARGS+=-DGGML_CUDA=ON
else ifeq ($(BUILD_TYPE),vulkan)
	CMAKE_ARGS+=-DGGML_VULKAN=ON
else ifeq ($(BUILD_TYPE),metal)
	CMAKE_ARGS+=-DGGML_METAL=ON
endif

.PHONY: nemo-speech-cpp-grpc package build clean purge test all stage-libs patch-ggml

all: nemo-speech-cpp-grpc package

sources/NeMo-Speech.cpp:
	mkdir -p sources
	cd sources && git clone $(NEMO_SPEECH_REPO) NeMo-Speech.cpp
	cd sources/NeMo-Speech.cpp && git checkout $(NEMO_SPEECH_VERSION)
	# NMT links llama.cpp; ja needs open_jtalk; zh needs cppjieba. flashlight and
	# kenlm are deliberately not initialized, they are out of scope.
	cd sources/NeMo-Speech.cpp && git submodule update --init --recursive \
		ggml llama.cpp third_party/open_jtalk third_party/cppjieba third_party/cpp-httplib

# NEMO_SPEECH_GGML_PATCHED defaults ON and silently assumes the ggml-patches
# series is applied. An unpatched checkout builds fine and produces wrong CUDA
# encoder output, so a failure here must stop the build rather than warn.
#
# Upstream's own script is the right tool: it applies the series in filename
# order, exits non-zero when a patch does not apply, and decides "already
# applied" by comparing the full-series tree hash rather than a timestamp. That
# makes it safe to run unconditionally, so there is no sentinel file to go stale
# or to wedge the build when deleted.
patch-ggml: | sources/NeMo-Speech.cpp
	cd sources/NeMo-Speech.cpp && bash scripts/apply-ggml-patches.sh

# Upstream sets CMAKE_LIBRARY_OUTPUT_DIRECTORY to ${CMAKE_BINARY_DIR}/bin, so the
# shared objects land in build/bin rather than at the top of the build tree.
#
# patch-ggml is order-only: it is phony and therefore always runs, but an
# order-only prerequisite does not mark this target out of date, so an
# already-built tree is not relinked on every invocation.
sources/NeMo-Speech.cpp/build/bin/libnemo_speech_asr_c.so: | patch-ggml
ifeq ($(WITH_NORM),ON)
	# OpenFST's templates ICE on gcc-13/14 at -O2, so this one step pins gcc-12
	# while the runtime itself builds with the image default.
	cd sources/NeMo-Speech.cpp && CC=gcc-12 CXX=g++-12 JOBS=$(JOBS) scripts/build_itn_deps.sh
endif
	cd sources/NeMo-Speech.cpp && cmake -B build -G Ninja $(CMAKE_ARGS)
	cd sources/NeMo-Speech.cpp && cmake --build build -j$(JOBS)

# Stage the runtime next to the Go sources so purego.Dlopen finds it during
# local development and so package.sh has a single directory to bundle from.
#
# ASR and NMT build a dedicated _c shared object that links the C++ implementation
# in privately. TTS does not: upstream compiles its c_api.cpp straight into
# libnemo_speech_tts and only aliases the nemo_speech_tts_c CMake target, so the
# TTS C ABI ships without the _c suffix.
stage-libs: sources/NeMo-Speech.cpp/build/bin/libnemo_speech_asr_c.so
	# -a keeps the SOVERSION symlink a symlink instead of duplicating the payload.
	cp -af sources/NeMo-Speech.cpp/build/bin/libnemo_speech_asr_c.* .
	cp -af sources/NeMo-Speech.cpp/build/bin/libnemo_speech_tts.* .
	cp -af sources/NeMo-Speech.cpp/build/bin/libnemo_speech_nmt_c.* .
	# The _c libraries are thin ABI shims with a DT_NEEDED on the C++
	# implementation DSO, so dlopen fails without these next to them. TTS needs
	# no counterpart, its implementation and ABI live in the same object.
	cp -af sources/NeMo-Speech.cpp/build/bin/libnemo_speech_asr.* .
	cp -af sources/NeMo-Speech.cpp/build/bin/libnemo_speech_nmt.* .
ifeq ($(WITH_NORM),ON)
	# nemo_speech_text_normalization is STATIC but links sparrowhawk, fstfar and
	# fst PUBLIC, so those become DT_NEEDED on libnemo_speech_asr.so. They live in
	# a project-local prefix that nothing else on the system provides, so without
	# staging them here the packaged backend cannot dlopen at all.
	@test -d "$(ITN_LIB_DIR)" || { \
		echo "ERROR: WITH_NORM=ON but $(ITN_LIB_DIR) is missing." >&2; \
		echo "       scripts/build_itn_deps.sh did not run or did not install." >&2; \
		exit 1; }
	cp -af $(ITN_LIB_DIR)/*.so* .
endif

nemo-speech-cpp-grpc: stage-libs
	# CGO_ENABLED=0 matches whisper / parakeet-cpp / omnivoice-cpp: the runtime is
	# reached through purego.Dlopen, not cgo, and a static binary is what lets
	# run.sh route execution through the packaged lib/ld.so.
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o nemo-speech-cpp-grpc .

# The dlopen tests need the staged shared objects on the loader path, the same
# way parakeet-cpp sets it up.
test:
	LD_LIBRARY_PATH=$(CURDIR):$$LD_LIBRARY_PATH $(GOCMD) test ./... -count=1

package: nemo-speech-cpp-grpc
	bash package.sh

# What backend/Dockerfile.golang invokes. It must leave both the binary and a
# populated package/ behind, because the final image stage copies package/.
build: package

clean:
	# Every .so here is staged output (nemo runtime plus, on a WITH_NORM build,
	# the ITN stack), and the SOVERSION suffix means the payload is *.so.1, so
	# the globs have to reach past the .so.
	rm -f nemo-speech-cpp-grpc
	rm -f *.so *.so.* *.dylib
	rm -rf package
	rm -rf sources/NeMo-Speech.cpp/build

purge: clean
	rm -rf sources
