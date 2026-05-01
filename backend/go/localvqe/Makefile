CMAKE_ARGS?=
BUILD_TYPE?=
NATIVE?=false

GOCMD?=go
GO_TAGS?=
JOBS?=$(shell nproc --ignore=1)

# LocalVQE upstream version pin. Bump to a specific commit when picking up
# a new release; `main` works for development but is not reproducible.
LOCALVQE_REPO?=https://github.com/localai-org/LocalVQE
LOCALVQE_VERSION?=72bfb4c6

# LocalVQE handles CPU feature selection internally (it ships the multiple
# libggml-cpu-*.so variants and its loader picks the best one at runtime
# via GGML_BACKEND_DL), so we build a single liblocalvqe.so + the per-CPU
# ggml shared libs and let it sort itself out. No need for a wrapper
# MODULE library or per-AVX backend variants here.

CMAKE_ARGS+=-DLOCALVQE_BUILD_SHARED=ON
CMAKE_ARGS+=-DGGML_BUILD_TESTS=OFF
CMAKE_ARGS+=-DGGML_BUILD_EXAMPLES=OFF

ifeq ($(NATIVE),false)
	CMAKE_ARGS+=-DGGML_NATIVE=OFF
endif

# LocalVQE upstream supports CPU + Vulkan only. Other BUILD_TYPE values
# fall through to the default CPU build — Vulkan is already as fast as the
# specialised GPU paths would be on this 1.3 M-parameter model.
ifeq ($(BUILD_TYPE),vulkan)
	CMAKE_ARGS+=-DGGML_VULKAN=ON -DLOCALVQE_VULKAN=ON
else ifeq ($(OS),Darwin)
	CMAKE_ARGS+=-DGGML_METAL=OFF
endif

# --- Sources ---

sources/LocalVQE:
	mkdir -p sources/LocalVQE
	cd sources/LocalVQE && \
	git init && \
	git remote add origin $(LOCALVQE_REPO) && \
	git fetch origin && \
	git checkout $(LOCALVQE_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

# --- Native build ---
#
# Drives cmake directly against the upstream LocalVQE/ggml CMakeLists.
# Produces liblocalvqe.so plus the per-CPU libggml-cpu-*.so variants in
# build/bin/, all of which we copy into the backend directory so package.sh
# can pick them up. The `liblocalvqe.so` rule deliberately uses a sentinel
# stamp file because Make's wildcard tracking would otherwise mis-decide
# about freshness when SOVERSION symlinks are involved.

LIB_SENTINEL=.localvqe-build.stamp

$(LIB_SENTINEL): sources/LocalVQE
	mkdir -p build && \
	cd build && \
	cmake ../sources/LocalVQE/ggml $(CMAKE_ARGS) -DCMAKE_BUILD_TYPE=Release && \
	cmake --build . --config Release -j$(JOBS)
	# Upstream's CPU build sets GGML_BACKEND_DL=ON + GGML_CPU_ALL_VARIANTS=ON,
	# which produces multiple libggml-cpu-*.so files (SSE4.2 / AVX2 / AVX-512)
	# that the loader picks at runtime. We must build every target — the
	# default `--target localvqe_shared` drops these. CMAKE_LIBRARY_OUTPUT_DIRECTORY
	# routes all of them into build/bin; copy them out next to the binary.
	cp -P build/bin/liblocalvqe.so* . 2>/dev/null || cp -P build/liblocalvqe.so* .
	cp -P build/bin/libggml*.so* . 2>/dev/null || true
	touch $(LIB_SENTINEL)

liblocalvqe.so: $(LIB_SENTINEL)

# --- Go binary + packaging ---

localvqe: main.go golocalvqe.go $(LIB_SENTINEL)
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o localvqe ./

package: localvqe
	bash package.sh

build: package

clean: purge
	rm -rf liblocalvqe.so* libggml*.so* package sources/LocalVQE localvqe $(LIB_SENTINEL)

purge:
	rm -rf build

test: localvqe
	@echo "Running localvqe tests..."
	bash test.sh
	@echo "localvqe tests completed."

all: localvqe package

.PHONY: build package clean purge test all
