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

NEMO_SPEECH_VERSION?=4f9676226f667d14608487df744f375db87127f8
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

# NEMO_SPEECH_TTS_WITH_JA=ON compiles Open JTalk's bundled MeCab, and
# mecab/src/dictionary.cpp derives a comparator from std::binary_function, which
# C++17 removed. libstdc++ still ships it as deprecated-but-present under
# -std=gnu++17, so Linux never notices; libc++ compiles it out and the build dies
# with "no template named 'binary_function' in namespace 'std'". Upstream's own
# CMakeLists already carries the equivalent workaround for MSVC's STL
# (_HAS_AUTO_PTR_ETC plus /FIfunctional) but has no libc++ branch, because
# NEMO_SPEECH_TTS_WITH_JA defaults OFF upstream and only LocalAI turns it on.
#
# libc++ gates the two templates on _LIBCPP_ENABLE_CXX17_REMOVED_UNARY_BINARY_FUNCTION,
# and has done since LLVM 16, which is older than any clang Xcode still ships.
# The name matters: the older _LIBCPP_ENABLE_CXX17_REMOVED_BINDERS covers
# bind1st/bind2nd/ptr_fun/mem_fun and NOT unary_function/binary_function, and the
# umbrella _LIBCPP_ENABLE_CXX17_REMOVED_FEATURES no longer exists at all. A wrong
# name is silently accepted by the preprocessor and fixes nothing.
#
# Applied through CMAKE_CXX_FLAGS rather than to the one target because the
# tokenizer CMakeLists is upstream's and this tree is a pinned checkout, not a
# patched one. Project-wide is also the safer scope: the macro decides whether
# libc++'s internal __binary_function alias resolves to std::binary_function or
# to __binary_function_keep_layout_base, which is a base class of std::less and
# friends, so defining it for a subset of translation units would give those
# class templates two spellings in one binary. Both bases are empty and, at
# C++17, carry identical members, so the project-wide define changes no layout
# and no ABI. On Linux the macro is not a name libstdc++ knows, so the branch is
# unreachable there and would be inert even if it were taken.
ifeq ($(shell uname -s),Darwin)
	CXX_COMPAT_FLAGS?=-D_LIBCPP_ENABLE_CXX17_REMOVED_UNARY_BINARY_FUNCTION
else
	CXX_COMPAT_FLAGS?=
endif
ifneq ($(strip $(CXX_COMPAT_FLAGS)),)
	CMAKE_ARGS+=-DCMAKE_CXX_FLAGS=$(CXX_COMPAT_FLAGS)
endif

# scripts/build_itn_deps.sh installs the Sparrowhawk/OpenFST runtime here.
# NEMO_SPEECH_DEPENDENCY_PREFIX defaults to <src>/.deps upstream, and the ITN
# stack goes under its itn/ subdirectory. ITN_MARKER is a real output of that
# script (it prints exactly this file on success), so it can drive a make rule.
ITN_PREFIX=sources/NeMo-Speech.cpp/.deps/itn
ITN_LIB_DIR=$(ITN_PREFIX)/lib
ITN_MARKER=$(ITN_LIB_DIR)/libsparrowhawk.so
ITN_FST_HEADER=$(ITN_PREFIX)/include/fst/fst.h

# SentencePiece became a core ASR dependency in 5be7bfb: RNNT context biasing
# uses it even when Flashlight and text normalization are disabled. Build the
# pinned static archive provided by upstream so every platform gets the same
# dependency instead of relying on an undeclared system package.
SENTENCEPIECE_PREFIX=sources/NeMo-Speech.cpp/.deps/sentencepiece
SENTENCEPIECE_MARKER=$(SENTENCEPIECE_PREFIX)/lib/libsentencepiece.a

# Linux's ASR CMake block looks in NEMO_SPEECH_DEPENDENCY_PREFIX directly, but
# the Apple branch uses generic find_library()/find_path(). Put the same private
# prefix on CMake's search path so Darwin consumes the archive built above too.
CMAKE_ARGS+=-DCMAKE_PREFIX_PATH=$(abspath $(SENTENCEPIECE_PREFIX))

ITN_CC?=gcc-12
ITN_CXX?=g++-12

# Pin protoc to the apt one. backend/Dockerfile.golang drops protoc 27.1 into
# /usr/local/bin, which precedes /usr/bin on PATH, while libprotobuf-dev is the
# distro's (3.21 on noble, 3.12 on jammy). Sparrowhawk resolves protoc from PATH
# at make time (configure.ac uses AC_CHECK_PROG, so PROTOC substitutes to the
# bare word, and src/proto/Makefile.am invokes $(PROTOC)), and it commits no
# pregenerated stubs, so this always runs. Code generated by 27.1 includes
# google/protobuf/runtime_version.h and a PROTOBUF_VERSION #error guard that the
# older headers do not have, so the mismatch breaks the build. configure honours
# a pre-set PROTOC ("Let the user override the test"), which is what this is.
ITN_PROTOC?=/usr/bin/protoc

# Text normalization is Linux-only: Sparrowhawk/OpenFST assume a GNU toolchain
# and the gcc-12 pin has no macOS analogue. Documented gap, see the spec.
#
# An already-configured build tree wins over the platform default. Without that,
# a tree configured WITH_NORM=OFF would silently try to reconfigure itself to ON
# on the next bare `make test`, which means demanding gcc-12 from a developer who
# deliberately built without it. An explicit WITH_NORM= on the command line still
# overrides both, since command-line variables beat ?= assignments.
CMAKE_CACHE=sources/NeMo-Speech.cpp/build/CMakeCache.txt
CACHED_WITH_NORM=$(shell sed -n 's/^NEMO_SPEECH_WITH_NORM:BOOL=//p' $(CMAKE_CACHE) 2>/dev/null)
ifeq ($(shell uname -s),Darwin)
	WITH_NORM?=OFF
else ifneq ($(CACHED_WITH_NORM),)
	WITH_NORM?=$(CACHED_WITH_NORM)
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

# ggml-patches/ is a CUDA series. Every kernel it adds lives under
# src/ggml-cuda/; the only files it touches outside that directory are enum and
# name-table entries in include/ggml.h and src/ggml.c plus, in ggml-cpu, a
# supports_op returning false and an abort case for the CUDA-only op. Upstream
# agrees: its metal-* and vulkan-* CMake presets inherit the cpu-* ones, which
# set NEMO_SPEECH_GGML_PATCHED=OFF, and every use of a patch-only symbol in the
# ASR sources sits behind NEMO_SPEECH_FUSED_RELPOS_ATTN /
# NEMO_SPEECH_FASTCONFORMER_CUDA_FUSIONS (both force-OFF without GGML_CUDA) or
# behind NEMO_SPEECH_GGML_PATCHED itself, which guards a Q8_PLANAR flag write
# that a non-CUDA buffer already throws before reaching.
#
# So on macOS the series buys nothing, and it cannot be applied there anyway:
# upstream's scripts/apply-ggml-patches.sh uses mapfile, a bash 4 builtin, and
# macOS ships bash 3.2 as the only bash on the runner's PATH. Skip the patch
# step and tell cmake the linked ggml is stock, which is exactly upstream's own
# Metal configuration. Linux keeps applying the series unchanged.
ifeq ($(shell uname -s),Darwin)
	GGML_PATCHED?=OFF
else
	GGML_PATCHED?=ON
endif
CMAKE_ARGS+=-DNEMO_SPEECH_GGML_PATCHED=$(GGML_PATCHED)

.PHONY: nemo-speech-cpp-grpc package build clean purge test all stage-libs patch-ggml engine itn sentencepiece patch-itn-headers

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
#
# Both branches keep the order-only clone prerequisite: it is the only thing
# that pulls sources/ in on a WITH_NORM=OFF tree, where the library rule has no
# other prerequisite left.
ifeq ($(GGML_PATCHED),ON)
patch-ggml: | sources/NeMo-Speech.cpp
	cd sources/NeMo-Speech.cpp && bash scripts/apply-ggml-patches.sh
else
patch-ggml: | sources/NeMo-Speech.cpp
	@echo "[ggml-patch] skipped: NEMO_SPEECH_GGML_PATCHED=$(GGML_PATCHED), the series is CUDA-only"
endif

# The Sparrowhawk/OpenFST text-normalization stack, as a target in its own right
# keyed on a file the build script actually produces.
#
# It used to be a side effect of the runtime library rule, which meant make had
# no idea whether it existed: once the library was up to date the script could
# never run again, so a tree built WITH_NORM=OFF could not be moved to ON, and
# anything that needed the ITN prefix was stuck demanding a full clean. As its
# own rule it is built on demand, rebuilt independently, and reachable directly
# with 'make itn'.
#
# OpenFST's templates ICE on gcc-13/14 at -O2, hence the gcc-12 pin for this one
# step; the runtime itself builds with the image default compiler.
$(ITN_MARKER): | sources/NeMo-Speech.cpp
	@command -v $(ITN_CC) >/dev/null 2>&1 && command -v $(ITN_CXX) >/dev/null 2>&1 || { \
		echo "ERROR: $(ITN_CC)/$(ITN_CXX) not found, and text normalization needs them:" >&2; \
		echo "       OpenFST's templates ICE on gcc-13 and gcc-14 at -O2." >&2; \
		echo "       Install them, or build this backend with WITH_NORM=OFF." >&2; \
		exit 1; }
	# configure's only gate on a preset PROTOC is test -n, so a path that does not
	# exist is accepted here and surfaces much later as a bare "No such file or
	# directory" from inside make -C src/proto. Check it up front instead.
	@command -v $(ITN_PROTOC) >/dev/null 2>&1 || { \
		echo "ERROR: protoc not found at $(ITN_PROTOC)." >&2; \
		echo "       Install the protobuf-compiler package, whose protoc matches" >&2; \
		echo "       the libprotobuf-dev headers Sparrowhawk compiles against, or" >&2; \
		echo "       point this at a matching one with ITN_PROTOC=/path/to/protoc." >&2; \
		exit 1; }
	cd sources/NeMo-Speech.cpp && CC=$(ITN_CC) CXX=$(ITN_CXX) PROTOC=$(ITN_PROTOC) \
		JOBS=$(JOBS) scripts/build_itn_deps.sh
	@$(MAKE) --no-print-directory patch-itn-headers

# OpenFST 1.8.3's FstImpl copy-assignment operator assigns a raw SymbolTable*
# (what SymbolTable::Copy() returns) straight to a std::unique_ptr member:
#
#     isymbols_ = impl.isymbols_ ? impl.isymbols_->Copy() : nullptr;
#
# std::unique_ptr has no operator= taking a raw pointer in any C++ standard, so
# that line is ill-formed everywhere. It survived because nothing instantiates
# FstImpl::operator=, and gcc <= 13 only checks a template member's body when it
# is instantiated. gcc 14 resolves non-dependent operator expressions at template
# definition time, so it rejects the line in every translation unit that so much
# as includes <fst/fst.h>, with no instantiation involved. Verified: gcc 14.2
# fails on a file whose entire content is '#include <fst/fst.h>'.
#
# That is why this only shows up now. build_itn_deps.sh builds OpenFST with
# gcc-12 (its templates ICE on newer gcc at -O2) and upstream's own images build
# the runtime with gcc-13, so neither compiler ever sees it. LocalAI's
# backend/Dockerfile.golang installs gcc-14 and makes it the default via
# update-alternatives, and fst_normalizer.cpp is the one translation unit here
# that includes OpenFST, so it is the one that breaks.
#
# The fix is the same spelling FstImpl::SetInputSymbols already uses 80 lines
# further down, and matches the copy constructor's deep-copy intent exactly. It
# is applied to the installed prefix rather than to the OpenFST checkout because
# the prefix is the only copy the cmake build compiles against; libfst.so is
# already linked by this point and cannot contain the function, since no
# compiler could ever have emitted it. Only these two lines are affected: gcc 14
# reports exactly two errors over the whole OpenFST include closure, both here.
#
# Guarded on both sides so a pinned-version bump cannot silently no-op it: the
# first check fails if neither the broken nor the fixed spelling is present, the
# last fails if the broken one survives.
patch-itn-headers:
	@test -f $(ITN_FST_HEADER) || { \
		echo "ERROR: $(ITN_FST_HEADER) missing; the ITN prefix is not installed." >&2; \
		exit 1; }
	@grep -q 'isymbols_ = impl.isymbols_' $(ITN_FST_HEADER) || \
	 grep -q 'isymbols_.reset(impl.isymbols_' $(ITN_FST_HEADER) || { \
		echo "ERROR: FstImpl::operator= in $(ITN_FST_HEADER) matches neither the" >&2; \
		echo "       known-broken nor the patched form. OpenFST changed upstream;" >&2; \
		echo "       re-check whether this patch is still needed before removing it." >&2; \
		exit 1; }
	sed -i -E 's|^([[:space:]]*)([io]symbols_) = (impl\.[io]symbols_ \? impl\.[io]symbols_->Copy\(\) : nullptr);$$|\1\2.reset(\3);|' $(ITN_FST_HEADER)
	@if grep -q 'symbols_ = impl.[io]symbols_' $(ITN_FST_HEADER); then \
		echo "ERROR: the FstImpl::operator= patch did not apply to $(ITN_FST_HEADER)." >&2; \
		exit 1; \
	fi

itn: $(ITN_MARKER)

$(SENTENCEPIECE_MARKER): | sources/NeMo-Speech.cpp
	# Upstream's license copies use GNU install's -D flag, which BSD install
	# does not support. Homebrew CMake 4 also rejects SentencePiece's old policy
	# floor. Patch both incompatibilities before running the helper on Darwin.
	@if [ "$(shell uname -s)" = Darwin ]; then \
		cd sources/NeMo-Speech.cpp && \
		mkdir -p .deps/sentencepiece/share/licenses/nemo-speech/third_party/sentencepiece && \
		perl -pi \
			-e 's/install -Dm0644/install -m 0644/g;' \
			-e 's/-DCMAKE_BUILD_TYPE=Release /-DCMAKE_BUILD_TYPE=Release -DCMAKE_POLICY_VERSION_MINIMUM=3.5 /;' \
			scripts/build_sentencepiece_static.sh; \
	fi
	cd sources/NeMo-Speech.cpp && JOBS=$(JOBS) scripts/build_sentencepiece_static.sh

sentencepiece: $(SENTENCEPIECE_MARKER)

# Only a WITH_NORM=ON build needs the ITN stack, and it must exist before cmake
# configures, since the WITH_NORM cmake block find_library()s into the prefix
# with REQUIRED.
NEMO_RUNTIME_PREREQS=$(SENTENCEPIECE_MARKER)
ifeq ($(WITH_NORM),ON)
NEMO_RUNTIME_PREREQS+=$(ITN_MARKER)
endif

# Upstream sets CMAKE_LIBRARY_OUTPUT_DIRECTORY to ${CMAKE_BINARY_DIR}/bin, so the
# shared objects land in build/bin rather than at the top of the build tree.
#
# patch-ggml is order-only: it is phony and therefore always runs, but an
# order-only prerequisite does not mark this target out of date, so an
# already-built tree is not relinked on every invocation.
sources/NeMo-Speech.cpp/build/bin/libnemo_speech_asr_c.so: $(NEMO_RUNTIME_PREREQS) | patch-ggml
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
	# nemo_speech_text_normalization is STATIC but links sparrowhawk, fstfar and
	# fst PUBLIC, so those become DT_NEEDED on libnemo_speech_asr.so. They live in
	# a project-local prefix that nothing else on the system provides, so without
	# staging them here the packaged backend cannot dlopen at all.
	#
	# Keyed on the prefix existing rather than on WITH_NORM, so this stages what
	# the tree actually built. A WITH_NORM=ON build cannot reach here without the
	# prefix (the library rule takes ITN_MARKER as a prerequisite), and if a
	# library that needs Sparrowhawk somehow arrives unstaged, package.sh's
	# closure guard fails the build rather than shipping it.
	@if [ -d "$(ITN_LIB_DIR)" ]; then \
		echo "cp -af $(ITN_LIB_DIR)/*.so* ."; \
		cp -af $(ITN_LIB_DIR)/*.so* .; \
	fi

## Builds the native runtime and stops short of the Go binary. Everything it
## touches lives under sources/, a clone pinned by NEMO_SPEECH_VERSION, so
## nothing here can observe a change elsewhere in the LocalAI tree.
## Dockerfile.golang calls this from a layer that copies in this directory and
## nothing else, which keeps the multi-minute ggml/llama.cpp compile in the
## registry layer cache across builds whose only change is on the Go side.
## Without it that prebuild is skipped and a CUDA build recompiles all of
## upstream on every Go-side edit. See .agents/ci-caching.md.
engine: stage-libs

nemo-speech-cpp-grpc: stage-libs
	# CGO_ENABLED=0 matches whisper / parakeet-cpp / omnivoice-cpp: the runtime is
	# reached through purego.Dlopen, not cgo, and a static binary is what lets
	# run.sh route execution through the packaged lib/ld.so.
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o nemo-speech-cpp-grpc .

# The dlopen tests need the staged shared objects on the loader path, the same
# way parakeet-cpp sets it up. Depends on stage-libs so that path is not an
# empty directory on a clean tree, which would fail the tests confusingly.
#
# NEMO_SPEECH_REQUIRE_LIBS turns a missing library from a skip into a failure.
# The ABI specs are the only thing standing between this backend and silent
# memory corruption, so a run that reaches them and quietly skips them is worse
# than one that fails: it reports green having checked nothing.
test: stage-libs
	NEMO_SPEECH_REQUIRE_LIBS=1 LD_LIBRARY_PATH=$(CURDIR):$$LD_LIBRARY_PATH $(GOCMD) test ./... -count=1

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
