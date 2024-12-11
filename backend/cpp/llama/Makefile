
LLAMA_VERSION?=
LLAMA_REPO?=https://github.com/ggerganov/llama.cpp

CMAKE_ARGS?=
BUILD_TYPE?=
ONEAPI_VARS?=/opt/intel/oneapi/setvars.sh
TARGET?=--target grpc-server

llama.cpp:
	mkdir -p llama.cpp
	cd llama.cpp && \
	git init && \
	git remote add origin $(LLAMA_REPO)  && \
	git fetch origin && \
	git checkout -b build $(LLAMA_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

llama.cpp/examples/grpc-server: llama.cpp
	mkdir -p llama.cpp/examples/grpc-server
	bash prepare.sh

rebuild:
	bash prepare.sh
	rm -rf grpc-server
	$(MAKE) grpc-server

purge:
	rm -rf llama.cpp/build
	rm -rf llama.cpp/examples/grpc-server
	rm -rf grpc-server

clean: purge
	rm -rf llama.cpp

grpc-server: llama.cpp llama.cpp/examples/grpc-server
	@echo "Building grpc-server for llama.cpp with $(BUILD_TYPE) build type and $(CMAKE_ARGS)"
ifneq (,$(findstring sycl,$(BUILD_TYPE)))
	+bash -c "source $(ONEAPI_VARS); \
	cd llama.cpp && mkdir -p build && cd build && cmake .. $(CMAKE_ARGS) && cmake --build . --config Release $(TARGET)"
else
	+cd llama.cpp && mkdir -p build && cd build && cmake .. $(CMAKE_ARGS) && cmake --build . --config Release $(TARGET)
endif
	cp llama.cpp/build/bin/grpc-server .