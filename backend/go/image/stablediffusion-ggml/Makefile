INCLUDE_PATH := $(abspath ./)
LIBRARY_PATH := $(abspath ./)

AR?=ar

BUILD_TYPE?=
# keep standard at C11 and C++11
CXXFLAGS = -I. -I$(INCLUDE_PATH)/../../../../sources/stablediffusion-ggml.cpp/thirdparty -I$(INCLUDE_PATH)/../../../../sources/stablediffusion-ggml.cpp/ggml/include -I$(INCLUDE_PATH)/../../../../sources/stablediffusion-ggml.cpp -O3 -DNDEBUG -std=c++17 -fPIC

# warnings
CXXFLAGS += -Wall -Wextra -Wpedantic -Wcast-qual -Wno-unused-function

gosd.o:
	$(CXX) $(CXXFLAGS) gosd.cpp -o gosd.o -c

libsd.a: gosd.o
	cp $(INCLUDE_PATH)/../../../../sources/stablediffusion-ggml.cpp/build/libstable-diffusion.a ./libsd.a
	$(AR) rcs libsd.a gosd.o

clean:
	rm -f gosd.o libsd.a