# SPDX-License-Identifier: MIT

AUDIO_CPP_VERSION?=f8fb0c19739193adfad0d9e58da99f25eda65256
AUDIO_CPP_REPO?=https://github.com/0xShug0/audio.cpp
AUDIO_CPP_SRC?=

CURRENT_MAKEFILE_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
BUILD_DIR := build

BUILD_TYPE ?=
JOBS ?= $(shell nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)
UNAME_S := $(shell uname -s)

CMAKE_ARGS ?= -DCMAKE_BUILD_TYPE=Release
CMAKE_ARGS += -DENGINE_ENABLE_CUDA=OFF
CMAKE_ARGS += -DENGINE_ENABLE_VULKAN=OFF
CMAKE_ARGS += -DENGINE_ENABLE_METAL=OFF

ifeq ($(BUILD_TYPE),cublas)
	CMAKE_ARGS += -DENGINE_ENABLE_CUDA=ON
else ifeq ($(BUILD_TYPE),vulkan)
	CMAKE_ARGS += -DENGINE_ENABLE_VULKAN=ON
else ifeq ($(UNAME_S),Darwin)
	CMAKE_ARGS += -DENGINE_ENABLE_METAL=ON
endif

.PHONY: all grpc-server test clean purge

all: grpc-server

audio.cpp:
ifneq ($(AUDIO_CPP_SRC),)
	ln -sfn $(abspath $(AUDIO_CPP_SRC)) audio.cpp
else
	mkdir -p audio.cpp
	cd audio.cpp && \
	git init -q && \
	git remote add origin $(AUDIO_CPP_REPO) && \
	git fetch --depth 1 origin $(AUDIO_CPP_VERSION) && \
	git checkout --detach FETCH_HEAD && \
	git submodule update --init --recursive --depth 1
endif

grpc-server: audio.cpp
	mkdir -p $(BUILD_DIR)
	cd $(BUILD_DIR) && cmake $(CMAKE_ARGS) $(CURRENT_MAKEFILE_DIR)
	cmake --build $(BUILD_DIR) --config Release \
		--target audio-cpp-grpc-server -j $(JOBS)
	cp $(BUILD_DIR)/audio-cpp-grpc-server grpc-server

test:
	bash tests/build_contract_test.sh

clean:
	rm -rf $(BUILD_DIR) grpc-server

purge: clean
	rm -rf audio.cpp
