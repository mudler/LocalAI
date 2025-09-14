CMAKE_ARGS?=
BUILD_TYPE?=
NATIVE?=false

GOCMD?=go
GO_TAGS?=
JOBS?=$(shell nproc --ignore=1)

# stablediffusion.cpp (ggml)
STABLEDIFFUSION_GGML_REPO?=https://github.com/leejet/stable-diffusion.cpp
STABLEDIFFUSION_GGML_VERSION?=0ebe6fe118f125665939b27c89f34ed38716bff8

CMAKE_ARGS+=-DGGML_MAX_NAME=128

ifeq ($(NATIVE),false)
	CMAKE_ARGS+=-DGGML_NATIVE=OFF
endif

# If build type is cublas, then we set -DGGML_CUDA=ON to CMAKE_ARGS automatically
ifeq ($(BUILD_TYPE),cublas)
	CMAKE_ARGS+=-DSD_CUDA=ON -DGGML_CUDA=ON
# If build type is openblas then we set -DGGML_BLAS=ON -DGGML_BLAS_VENDOR=OpenBLAS
# to CMAKE_ARGS automatically
else ifeq ($(BUILD_TYPE),openblas)
	CMAKE_ARGS+=-DGGML_BLAS=ON -DGGML_BLAS_VENDOR=OpenBLAS
# If build type is clblas (openCL) we set -DGGML_CLBLAST=ON -DCLBlast_DIR=/some/path
else ifeq ($(BUILD_TYPE),clblas)
	CMAKE_ARGS+=-DGGML_CLBLAST=ON -DCLBlast_DIR=/some/path
# If it's hipblas we do have also to set CC=/opt/rocm/llvm/bin/clang CXX=/opt/rocm/llvm/bin/clang++
else ifeq ($(BUILD_TYPE),hipblas)
	CMAKE_ARGS+=-DSD_HIPBLAS=ON -DGGML_HIPBLAS=ON
else ifeq ($(BUILD_TYPE),vulkan)
	CMAKE_ARGS+=-DSD_VULKAN=ON -DGGML_VULKAN=ON
else ifeq ($(OS),Darwin)
	ifneq ($(BUILD_TYPE),metal)
		CMAKE_ARGS+=-DSD_METAL=OFF -DGGML_METAL=OFF
	else
		CMAKE_ARGS+=-DSD_METAL=ON -DGGML_METAL=ON
		CMAKE_ARGS+=-DGGML_METAL_EMBED_LIBRARY=ON
	endif
endif

ifeq ($(BUILD_TYPE),sycl_f16)
	CMAKE_ARGS+=-DGGML_SYCL=ON \
		-DCMAKE_C_COMPILER=icx \
		-DCMAKE_CXX_COMPILER=icpx \
		-DSD_SYCL=ON \
		-DGGML_SYCL_F16=ON
endif

ifeq ($(BUILD_TYPE),sycl_f32)
	CMAKE_ARGS+=-DGGML_SYCL=ON \
		-DCMAKE_C_COMPILER=icx \
		-DCMAKE_CXX_COMPILER=icpx \
		-DSD_SYCL=ON
endif

sources/stablediffusion-ggml.cpp:
	git clone --recursive $(STABLEDIFFUSION_GGML_REPO) sources/stablediffusion-ggml.cpp && \
	cd sources/stablediffusion-ggml.cpp && \
	git checkout $(STABLEDIFFUSION_GGML_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

libgosd.so: sources/stablediffusion-ggml.cpp CMakeLists.txt gosd.cpp gosd.h
	mkdir -p build && \
	cd build && \
	cmake .. $(CMAKE_ARGS) && \
	cmake --build . --config Release -j$(JOBS) && \
	cd .. && \
	mv build/libgosd.so ./

stablediffusion-ggml: main.go gosd.go libgosd.so
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o stablediffusion-ggml ./

package: stablediffusion-ggml
	bash package.sh

build: package

clean:
	rm -rf libgosd.so build stablediffusion-ggml package sources
