INCLUDE_PATH := $(abspath ./)
LIBRARY_PATH := $(abspath ./)

AR?=ar

BUILD_TYPE?=
# keep standard at C11 and C++11
CXXFLAGS = -I. -I$(INCLUDE_PATH)/../../../sources/bark.cpp/examples -I$(INCLUDE_PATH)/../../../sources/bark.cpp/spm-headers -I$(INCLUDE_PATH)/../../../sources/bark.cpp -O3 -DNDEBUG -std=c++17 -fPIC
LDFLAGS  = -L$(LIBRARY_PATH) -L$(LIBRARY_PATH)/../../../sources/bark.cpp/build/examples -lbark -lstdc++ -lm

# warnings
CXXFLAGS += -Wall -Wextra -Wpedantic -Wcast-qual -Wno-unused-function

gobark.o:
	$(CXX) $(CXXFLAGS) gobark.cpp -o gobark.o -c $(LDFLAGS)

libbark.a: gobark.o
	cp $(INCLUDE_PATH)/../../../sources/bark.cpp/build/libbark.a ./
	$(AR) rcs libbark.a gobark.o
	$(AR) rcs libbark.a $(LIBRARY_PATH)/../../../sources/bark.cpp/build/encodec.cpp/ggml/src/CMakeFiles/ggml.dir/ggml.c.o
	$(AR) rcs libbark.a $(LIBRARY_PATH)/../../../sources/bark.cpp/build/encodec.cpp/ggml/src/CMakeFiles/ggml.dir/ggml-alloc.c.o
	$(AR) rcs libbark.a $(LIBRARY_PATH)/../../../sources/bark.cpp/build/encodec.cpp/ggml/src/CMakeFiles/ggml.dir/ggml-backend.c.o

clean:
	rm -f gobark.o libbark.a