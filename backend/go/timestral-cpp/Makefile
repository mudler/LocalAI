# timestral-cpp backend Makefile.
#
# The upstream pin is TIMESTRAL_VERSION below, in this file so the dependency
# bump bot can find and update it.
#
# Local dev shortcut: if you already have an out-of-tree timestral.cpp build,
# symlink the .so + header into this directory and skip the clone/cmake steps:
#
#   ln -sf /path/to/timestral.cpp/build-shared/libtimestral.so .
#   ln -sf /path/to/timestral.cpp/include/timestral_capi.h .
#   go build -o timestral-cpp-grpc .
#
# The default target does the clone-at-pin and cmake build, so CI needs no
# side checkout.

TIMESTRAL_VERSION?=6c0ee070de54e9b2611ea90696dee3a41178700c
TIMESTRAL_REPO?=https://github.com/mudler/timestral.cpp

GOCMD?=go
GO_TAGS?=
JOBS?=$(shell nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)

NATIVE?=false

# Build ggml statically into libtimestral (PIC) so the shared library is
# self-contained: dlopen needs no libggml*.so next to it, only system libs
# (libstdc++, libgomp, libc) that the runtime image already provides.
#
# The engine runs on the CPU only. BUILD_TYPE is therefore not forwarded: a
# metal, cublas or vulkan build type still produces the same CPU library.
CMAKE_ARGS?=-DCMAKE_BUILD_TYPE=Release -DTIMESTRAL_SHARED=ON -DTIMESTRAL_BUILD_CLI=OFF -DTIMESTRAL_BUILD_TESTS=OFF -DBUILD_SHARED_LIBS=OFF -DCMAKE_POSITION_INDEPENDENT_CODE=ON

ifeq ($(NATIVE),false)
	CMAKE_ARGS+=-DGGML_NATIVE=OFF
endif

.PHONY: timestral-cpp-grpc package build clean purge test all

all: timestral-cpp-grpc

# Clone the upstream source at the pinned commit, with its ggml submodule. The
# directory is the target, so make clones only when it is missing. After a
# TIMESTRAL_VERSION bump, run 'make purge && make' to fetch again.
sources/timestral.cpp:
	mkdir -p sources/timestral.cpp
	cd sources/timestral.cpp && \
	git init -q && \
	git remote add origin $(TIMESTRAL_REPO) && \
	git fetch --depth 1 origin $(TIMESTRAL_VERSION) && \
	git checkout FETCH_HEAD && \
	git submodule update --init --recursive --depth 1 --single-branch

# Build the shared library out of tree, then stage it and the header next to
# the Go sources, so purego.Dlopen("libtimestral.so") finds it.
libtimestral.so: sources/timestral.cpp
	cmake -B sources/timestral.cpp/build-shared -S sources/timestral.cpp $(CMAKE_ARGS)
	cmake --build sources/timestral.cpp/build-shared --config Release -j$(JOBS) --target timestral
	cp -fv sources/timestral.cpp/build-shared/libtimestral.so* ./ 2>/dev/null || true
	cp -fv sources/timestral.cpp/build-shared/libtimestral.dylib ./ 2>/dev/null || true
	cp -fv sources/timestral.cpp/include/timestral_capi.h ./

timestral-cpp-grpc: libtimestral.so main.go abi.go gotimestralcpp.go options.go plan.go
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o timestral-cpp-grpc .

package: timestral-cpp-grpc
	bash package.sh

build: package

# The pure-Go specs always run. The integration specs need
# TIMESTRAL_BACKEND_TEST_MODEL and a built library; test.sh fetches the 5M F32
# model and sets both. Without them the integration specs skip.
test:
	bash test.sh

clean: purge
	rm -rf libtimestral.so* libtimestral.dylib timestral_capi.h package timestral-cpp-grpc

purge:
	rm -rf sources/timestral.cpp
