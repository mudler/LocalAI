# Basic platform detection
HOST_SYSTEM = $(shell uname | cut -f 1 -d_)
SYSTEM ?= $(HOST_SYSTEM)

TAG_LIB_GRPC?=v1.59.0
GIT_REPO_LIB_GRPC?=https://github.com/grpc/grpc.git
GIT_CLONE_DEPTH?=1
NUM_BUILD_THREADS?=$(shell nproc --ignore=1)

INSTALLED_PACKAGES=installed_packages
GRPC_REPO=grpc_repo
GRPC_BUILD=grpc_build

export CMAKE_ARGS?=
CMAKE_ARGS+=-DCMAKE_BUILD_TYPE=Release
CMAKE_ARGS+=-DgRPC_INSTALL=ON
CMAKE_ARGS+=-DEXECUTABLE_OUTPUT_PATH=../$(INSTALLED_PACKAGES)/grpc/bin
CMAKE_ARGS+=-DLIBRARY_OUTPUT_PATH=../$(INSTALLED_PACKAGES)/grpc/lib
CMAKE_ARGS+=-DgRPC_BUILD_TESTS=OFF
CMAKE_ARGS+=-DgRPC_BUILD_CSHARP_EXT=OFF
CMAKE_ARGS+=-DgRPC_BUILD_GRPC_CPP_PLUGIN=ON
CMAKE_ARGS+=-DgRPC_BUILD_GRPC_CSHARP_PLUGIN=OFF
CMAKE_ARGS+=-DgRPC_BUILD_GRPC_NODE_PLUGIN=OFF
CMAKE_ARGS+=-DgRPC_BUILD_GRPC_OBJECTIVE_C_PLUGIN=OFF
CMAKE_ARGS+=-DgRPC_BUILD_GRPC_PHP_PLUGIN=OFF
CMAKE_ARGS+=-DgRPC_BUILD_GRPC_PYTHON_PLUGIN=ON
CMAKE_ARGS+=-DgRPC_BUILD_GRPC_RUBY_PLUGIN=OFF
CMAKE_ARGS+=-Dprotobuf_WITH_ZLIB=ON
CMAKE_ARGS+=-DRE2_BUILD_TESTING=OFF
CMAKE_ARGS+=-DCMAKE_INSTALL_PREFIX=../$(INSTALLED_PACKAGES)

# windows need to set OPENSSL_NO_ASM. Results in slower crypto performance but doesn't build otherwise.
# May be resolvable, but for now its set. More info: https://stackoverflow.com/a/75240504/480673
ifeq ($(SYSTEM),MSYS)
CMAKE_ARGS+=-DOPENSSL_NO_ASM=ON
endif
ifeq ($(SYSTEM),MINGW64)
CMAKE_ARGS+=-DOPENSSL_NO_ASM=ON
endif
ifeq ($(SYSTEM),MINGW32)
CMAKE_ARGS+=-DOPENSSL_NO_ASM=ON
endif
ifeq ($(SYSTEM),CYGWIN)
CMAKE_ARGS+=-DOPENSSL_NO_ASM=ON
endif

$(INSTALLED_PACKAGES): grpc_build

$(GRPC_REPO):
	git clone --depth $(GIT_CLONE_DEPTH) -b $(TAG_LIB_GRPC) $(GIT_REPO_LIB_GRPC) $(GRPC_REPO)/grpc
	cd $(GRPC_REPO)/grpc && git submodule update --init --recursive --depth $(GIT_CLONE_DEPTH)

$(GRPC_BUILD): $(GRPC_REPO)
	mkdir -p $(GRPC_BUILD)
	cd $(GRPC_BUILD) && cmake $(CMAKE_ARGS) ../$(GRPC_REPO)/grpc && cmake --build . -- -j ${NUM_BUILD_THREADS} && cmake --build . --target install -- -j ${NUM_BUILD_THREADS}

build: $(INSTALLED_PACKAGES)

rebuild:
	rm -rf grpc_build
	$(MAKE) grpc_build

clean:
	rm -rf grpc_build
	rm -rf grpc_repo
	rm -rf installed_packages
