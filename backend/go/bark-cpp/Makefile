INCLUDE_PATH := $(abspath ./)
LIBRARY_PATH := $(abspath ./)

AR?=ar

CMAKE_ARGS?=-DGGML_NATIVE=OFF
BUILD_TYPE?=
GOCMD=go
# keep standard at C11 and C++11
CXXFLAGS = -I. -I$(INCLUDE_PATH)/sources/bark.cpp/examples -I$(INCLUDE_PATH)/sources/bark.cpp/encodec.cpp/ggml/include -I$(INCLUDE_PATH)/sources/bark.cpp/spm-headers -I$(INCLUDE_PATH)/sources/bark.cpp -O3 -DNDEBUG -std=c++17 -fPIC
LDFLAGS  = -L$(LIBRARY_PATH) -L$(LIBRARY_PATH)/sources/bark.cpp/build/examples -lbark -lstdc++ -lm

# bark.cpp
BARKCPP_REPO?=https://github.com/PABannier/bark.cpp.git
BARKCPP_VERSION?=5d5be84f089ab9ea53b7a793f088d3fbf7247495

# warnings
CXXFLAGS += -Wall -Wextra -Wpedantic -Wcast-qual -Wno-unused-function

## bark.cpp
sources/bark.cpp:
	git clone --recursive $(BARKCPP_REPO) sources/bark.cpp && \
	cd sources/bark.cpp && \
	git checkout $(BARKCPP_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

sources/bark.cpp/build/libbark.a: sources/bark.cpp
	cd sources/bark.cpp && \
	mkdir -p build && \
	cd build && \
	cmake $(CMAKE_ARGS) .. && \
	cmake --build . --config Release

gobark.o:
	$(CXX) $(CXXFLAGS) gobark.cpp -o gobark.o -c $(LDFLAGS)

libbark.a: sources/bark.cpp/build/libbark.a gobark.o
	cp $(INCLUDE_PATH)/sources/bark.cpp/build/libbark.a ./
	$(AR) rcs libbark.a gobark.o

bark-cpp: libbark.a
	CGO_LDFLAGS="$(CGO_LDFLAGS)" C_INCLUDE_PATH="$(CURDIR)" LIBRARY_PATH=$(CURDIR) \
	$(GOCMD) build -v -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o bark-cpp ./

package:
	bash package.sh

build: bark-cpp package

clean:
	rm -f gobark.o libbark.a