INCLUDE_PATH := $(abspath ./)
LIBRARY_PATH := $(abspath ./)

AR?=ar
CMAKE_ARGS?=
BUILD_TYPE?=
ONEAPI_VARS?=/opt/intel/oneapi/setvars.sh
# keep standard at C11 and C++11
CXXFLAGS = -I. -I$(INCLUDE_PATH)/../../../../sources/stablediffusion-ggml.cpp/thirdparty -I$(INCLUDE_PATH)/../../../../sources/stablediffusion-ggml.cpp/ggml/include -I$(INCLUDE_PATH)/../../../../sources/stablediffusion-ggml.cpp -O3 -DNDEBUG -std=c++17 -fPIC

GOCMD?=go
CGO_LDFLAGS?=
# Avoid parent make file overwriting CGO_LDFLAGS which is needed for hipblas
CGO_LDFLAGS_SYCL=
GO_TAGS?=
LD_FLAGS?=

# Disable Shared libs as we are linking on static gRPC and we can't mix shared and static
CMAKE_ARGS+=-DBUILD_SHARED_LIBS=OFF

# If build type is cublas, then we set -DGGML_CUDA=ON to CMAKE_ARGS automatically
ifeq ($(BUILD_TYPE),cublas)
	CMAKE_ARGS+=-DSD_CUDA=ON
# If build type is openblas then we set -DGGML_BLAS=ON -DGGML_BLAS_VENDOR=OpenBLAS
# to CMAKE_ARGS automatically
else ifeq ($(BUILD_TYPE),openblas)
	CMAKE_ARGS+=-DGGML_BLAS=ON -DGGML_BLAS_VENDOR=OpenBLAS
# If build type is clblas (openCL) we set -DGGML_CLBLAST=ON -DCLBlast_DIR=/some/path
else ifeq ($(BUILD_TYPE),clblas)
	CMAKE_ARGS+=-DGGML_CLBLAST=ON -DCLBlast_DIR=/some/path
# If it's hipblas we do have also to set CC=/opt/rocm/llvm/bin/clang CXX=/opt/rocm/llvm/bin/clang++
else ifeq ($(BUILD_TYPE),hipblas)
	CMAKE_ARGS+=-DSD_HIPBLAS=ON
# If it's OSX, DO NOT embed the metal library - -DGGML_METAL_EMBED_LIBRARY=ON requires further investigation
# But if it's OSX without metal, disable it here
else ifeq ($(OS),Darwin)
	ifneq ($(BUILD_TYPE),metal)
		CMAKE_ARGS+=-DSD_METAL=OFF
	else
		CMAKE_ARGS+=-DSD_METAL=ON
		CMAKE_ARGS+=-DGGML_METAL_EMBED_LIBRARY=ON
		TARGET+=--target ggml-metal
	endif
endif

ifeq ($(BUILD_TYPE),sycl_f16)
	CMAKE_ARGS+=-DGGML_SYCL=ON \
		-DCMAKE_C_COMPILER=icx \
		-DCMAKE_CXX_COMPILER=icpx \
		-DSD_SYCL=ON \
		-DGGML_SYCL_F16=ON
	CC=icx
	CXX=icpx
	CGO_LDFLAGS_SYCL += -fsycl -L${DNNLROOT}/lib -ldnnl ${MKLROOT}/lib/intel64/libmkl_sycl.a -fiopenmp -fopenmp-targets=spir64 -lOpenCL
	CGO_LDFLAGS_SYCL += $(shell pkg-config --libs mkl-static-lp64-gomp)
	CGO_CXXFLAGS += -fiopenmp -fopenmp-targets=spir64
	CGO_CXXFLAGS += $(shell pkg-config --cflags mkl-static-lp64-gomp )
endif

ifeq ($(BUILD_TYPE),sycl_f32)
	CMAKE_ARGS+=-DGGML_SYCL=ON \
		-DCMAKE_C_COMPILER=icx \
		-DCMAKE_CXX_COMPILER=icpx \
		-DSD_SYCL=ON
	CC=icx
	CXX=icpx
	CGO_LDFLAGS_SYCL += -fsycl -L${DNNLROOT}/lib -ldnnl ${MKLROOT}/lib/intel64/libmkl_sycl.a -fiopenmp -fopenmp-targets=spir64 -lOpenCL
	CGO_LDFLAGS_SYCL += $(shell pkg-config --libs mkl-static-lp64-gomp)
	CGO_CXXFLAGS += -fiopenmp -fopenmp-targets=spir64
	CGO_CXXFLAGS += $(shell pkg-config --cflags mkl-static-lp64-gomp )
endif

# warnings
# CXXFLAGS += -Wall -Wextra -Wpedantic -Wcast-qual -Wno-unused-function

# Find all .a archives in ARCHIVE_DIR
# (ggml can have different backends cpu, cuda, etc., each backend generates a .a archive)
GGML_ARCHIVE_DIR := build/ggml/src/
ALL_ARCHIVES := $(shell find $(GGML_ARCHIVE_DIR) -type f -name '*.a')

# Name of the single merged library
COMBINED_LIB := libggmlall.a

# Rule to merge all the .a files into one
$(COMBINED_LIB): $(ALL_ARCHIVES)
	@echo "Merging all .a into $(COMBINED_LIB)"
	rm -f $@
	mkdir -p merge-tmp
	for a in $(ALL_ARCHIVES); do \
		( cd merge-tmp && ar x ../$$a ); \
	done
	( cd merge-tmp && ar rcs ../$@ *.o )
	# Ensure we have a proper index
	ranlib $@
	# Clean up
	rm -rf merge-tmp

build/libstable-diffusion.a:
	@echo "Building SD with $(BUILD_TYPE) build type and $(CMAKE_ARGS)"
ifneq (,$(findstring sycl,$(BUILD_TYPE)))
	+bash -c "source $(ONEAPI_VARS); \
	mkdir -p build && \
	cd build && \
	cmake $(CMAKE_ARGS) ../../../../../sources/stablediffusion-ggml.cpp && \
	cmake --build . --config Release"
else
	mkdir -p build && \
	cd build && \
	cmake $(CMAKE_ARGS) ../../../../../sources/stablediffusion-ggml.cpp && \
	cmake --build . --config Release
endif
	$(MAKE) $(COMBINED_LIB)

gosd.o:
ifneq (,$(findstring sycl,$(BUILD_TYPE)))
	+bash -c "source $(ONEAPI_VARS); \
	$(CXX) $(CXXFLAGS) gosd.cpp -o gosd.o -c"
else
	$(CXX) $(CXXFLAGS) gosd.cpp -o gosd.o -c
endif

libsd.a: gosd.o
	cp $(INCLUDE_PATH)/build/libstable-diffusion.a ./libsd.a
	$(AR) rcs libsd.a gosd.o

stablediffusion-ggml:
	CGO_LDFLAGS="$(CGO_LDFLAGS) $(CGO_LDFLAGS_SYCL)" C_INCLUDE_PATH="$(INCLUDE_PATH)" LIBRARY_PATH="$(LIBRARY_PATH)" \
	CC="$(CC)" CXX="$(CXX)" CGO_CXXFLAGS="$(CGO_CXXFLAGS)" \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o ../../../../backend-assets/grpc/stablediffusion-ggml ./
ifneq ($(UPX),)
	$(UPX) ../../../../backend-assets/grpc/stablediffusion-ggml
endif

clean:
	rm -rf gosd.o libsd.a build $(COMBINED_LIB)
