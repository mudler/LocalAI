GOCMD=go

# The strata backend is a Go program that drives the Strata inference engine
# (https://github.com/Niko1221/Strata, MIT) as a child process. The engine is
# not compiled here. Upstream publishes ready-made engine packages for every
# target it supports, and this Makefile fetches the one that matches the build
# (BUILD_TYPE, CUDA_MAJOR_VERSION and the CPU architecture), checks it against a
# pinned SHA-256 and unpacks it. See docs/content/models/engines/strata.md.
#
# Targets:
#   strata   the Go binary only (what `make -C backend/go/strata` builds)
#   engine   everything the image needs besides the Go binary: the engine
#            package(s), a portable Python and the Strata Python modules.
#            Dockerfile.golang calls this in its own cacheable layer.
#   build    engine + strata + package
#
# ---------------------------------------------------------------------------
# Pins. This is the one place that names a Strata version. Everything below is
# rewritten by .github/bump_strata.sh (the bump_deps.yaml job "bump-strata");
# do not edit the values by hand. The bot matches `^NAME?=` lines.
#
# STRATA_VERSION       the upstream release tag
# STRATA_COMMIT        the commit of that tag; the Python modules the backend
#                      needs (tokenizer, chat template) are fetched at it
# STRATA_ENGINE_VERSION the version in the engine package file names
# STRATA_SHA256_<asset> SHA-256 of strata-engine-<version>-<asset>.tar.gz
# STRATA_DSV41_ENGINE_VERSION, STRATA_DSV41_SHA256_<asset>
#                      the same for the DeepSeek V4.1 engine package,
#                      strata-dsv41-<version>-<asset>.tar.gz. Upstream ships it
#                      for fewer targets than the Qwen engine (see below).
# ---------------------------------------------------------------------------
STRATA_VERSION?=v0.2.12
STRATA_COMMIT?=146df3c468739147f520ac3816de806f71232571
STRATA_ENGINE_VERSION?=0.1.58
STRATA_SHA256_linux_x86_64_cuda_cuda12?=a0e068d6c79b30a0126ae015e9d61e39ad3b2b2b48d61e6b543bd2fc2dee5d2f
STRATA_SHA256_linux_x86_64_cuda_cuda13?=bbbfa8260125b94591cae30747cb0e4a9cee26d9cb0b2e284e33000c91509d54
STRATA_SHA256_linux_aarch64_cuda_cuda13?=fa0a587bb686fe54c565f3c109308b0db62f233d6ba50d02929b57a9b3c8ff89
STRATA_SHA256_linux_x86_64_hip_rx9070?=f0bc19645618cfb27986a2c9ee413bd9cd5a00661dee0ff1c08358acda319471
STRATA_SHA256_linux_x86_64_hip_rx7900?=7d83a7739ae16bc6065b42bb379c667d176aec8fded5fa52153c754adb9c360c
STRATA_SHA256_linux_x86_64_hip_rx6000?=45d96cb6c04b3c545ff04a8729061eebf64fd7b00f376fe30a68e38f8ac41e34
STRATA_SHA256_linux_x86_64_hip_halo?=1ce376c443ef662c72cd1fc4f828e093f78f83498c946837ca22cc250eda8861
STRATA_SHA256_linux_x86_64_sycl?=2a32d61383327073a8d4d81682d18678cd344fb331d4c5979d57d849d6b6b219
STRATA_SHA256_macos_arm64_metal?=9b2352fb6f4d2597e0e37b02cf13122949a7cf4e23a538b302065d21940890c6

STRATA_DSV41_ENGINE_VERSION?=0.1.58
STRATA_DSV41_SHA256_linux_x86_64_cuda_cuda13?=8ff96186db086b525c716a92f81e7d59e3cb2a0383aa4198ce0ddf14b7473c43
STRATA_DSV41_SHA256_linux_aarch64_cuda_cuda13?=d7d0e21ac5eb72ab046129487734cd8aa22ea199e5292f52ce75c9ebaa28e635
STRATA_DSV41_SHA256_linux_x86_64_hip_rx9070?=22e6f835a97fcb68c36c003b5a33374a80bec5033f9ec393e52bd510139c2f4d
STRATA_DSV41_SHA256_macos_arm64_metal?=4141eb6c9650f8b7100b25a64e49ec037c0f7120c7f71975265d17d149675d00

STRATA_REPO?=https://github.com/Niko1221/Strata

# llama.cpp, for its gguf-py folder only (the pack tool dequantizes with it).
# The commit is the one Strata's setup.py pins as LLAMA_CPP_COMMIT.
LLAMA_CPP_REPO?=https://github.com/ggml-org/llama.cpp
LLAMA_CPP_COMMIT?=3cf03257f219afbe7334045ff7c6a06ac68c627d

# Portable CPython for the tokenizer helper (python-build-standalone). The
# backend image is built FROM scratch, so the host's Python cannot be assumed.
# Pinned by release date and SHA-256, one per platform. The Python packages the
# helper imports are pinned with hashes in requirements.txt.
PBS_RELEASE?=20261026
PBS_PYTHON?=3.12.16
PBS_SHA256_linux_x86_64?=ce4f6d0a6cfdb3bed6e44c2f9affd85943c76a42e25ad3a259002f31d6fb8a20
PBS_SHA256_linux_aarch64?=bb4b6392670c5d3f93876996fcaa19858a4a413a0c7720144c18b0d6f1f0db2d
PBS_SHA256_macos_arm64?=353d5f1b4fd2715e7053f12f068435d5bbad16b91e057006b5d01e0925709a7e

# ---------------------------------------------------------------------------
# Which engine package(s) this build ships. The result is a list of upstream
# package keys; an empty list means Strata has no engine for the target, and
# `make engine` stops with a message (at run time, not at parse time, so that
# `make strata`, `make test` and `make -n engine` still work everywhere).
# ---------------------------------------------------------------------------
BUILD_TYPE?=
CUDA_MAJOR_VERSION?=
UNAME_S:=$(shell uname -s)
UNAME_M:=$(shell uname -m)

ifeq ($(UNAME_S),Darwin)
  ifeq ($(UNAME_M),arm64)
    PBS_PLATFORM:=macos_arm64
    STRATA_PACKAGES:=macos_arm64_metal
    STRATA_DSV41_PACKAGES:=macos_arm64_metal
  endif
else ifeq ($(UNAME_S),Linux)
  ifeq ($(UNAME_M),x86_64)
    PBS_PLATFORM:=linux_x86_64
  else ifneq ($(filter aarch64 arm64,$(UNAME_M)),)
    PBS_PLATFORM:=linux_aarch64
  endif
  ifeq ($(BUILD_TYPE),cublas)
    ifeq ($(UNAME_M),x86_64)
      ifeq ($(CUDA_MAJOR_VERSION),12)
        STRATA_PACKAGES:=linux_x86_64_cuda_cuda12
      else ifeq ($(CUDA_MAJOR_VERSION),13)
        STRATA_PACKAGES:=linux_x86_64_cuda_cuda13
        STRATA_DSV41_PACKAGES:=linux_x86_64_cuda_cuda13
      endif
    else ifneq ($(filter aarch64 arm64,$(UNAME_M)),)
      # Upstream's arm64 package is built for the DGX Spark (sm_121, CUDA 13).
      ifeq ($(CUDA_MAJOR_VERSION),13)
        STRATA_PACKAGES:=linux_aarch64_cuda_cuda13
        STRATA_DSV41_PACKAGES:=linux_aarch64_cuda_cuda13
      endif
    endif
  else ifeq ($(BUILD_TYPE),hipblas)
    ifeq ($(UNAME_M),x86_64)
      # One package per GPU family; strata-engine picks the one that matches the
      # card at run time (RDNA4, RDNA3, RDNA2, Strix Halo).
      STRATA_PACKAGES:=linux_x86_64_hip_rx9070 linux_x86_64_hip_rx7900 linux_x86_64_hip_rx6000 linux_x86_64_hip_halo
      # The DeepSeek engine has code for the RX 9070 / R9700 (gfx1200, gfx1201) only.
      STRATA_DSV41_PACKAGES:=linux_x86_64_hip_rx9070
    endif
  else ifneq ($(filter sycl_f16 sycl_f32 intel,$(BUILD_TYPE)),)
    ifeq ($(UNAME_M),x86_64)
      STRATA_PACKAGES:=linux_x86_64_sycl
    endif
  endif
endif

# Package key -> directory name inside the image (engines/<name>).
engine_dir=$(patsubst linux_x86_64_cuda_%,%,$(patsubst linux_aarch64_cuda_%,%,$(patsubst linux_x86_64_hip_%,hip-%,$(patsubst linux_x86_64_sycl,sycl,$(patsubst macos_arm64_metal,metal,$(1))))))
# Upstream publishes the DeepSeek engine for CUDA 13 (x86_64 and arm64), the
# RX 9070 / R9700 family and Apple Silicon only. There is none for CUDA 12 or
# Intel SYCL, and none for the other AMD families: those images carry the Qwen
# engine alone, and a DeepSeek model fails to load there with a clear message.
asset_dsv41=strata-dsv41-$(STRATA_DSV41_ENGINE_VERSION)-$(subst x86-64,x86_64,$(subst _,-,$(1))).tar.gz
# Package key -> upstream file name: linux_x86_64_hip_halo -> linux-x86_64-hip-halo
asset_name=strata-engine-$(STRATA_ENGINE_VERSION)-$(subst x86-64,x86_64,$(subst _,-,$(1))).tar.gz

PBS_TRIPLE_linux_x86_64=x86_64-unknown-linux-gnu
PBS_TRIPLE_linux_aarch64=aarch64-unknown-linux-gnu
PBS_TRIPLE_macos_arm64=aarch64-apple-darwin
PBS_FILE=cpython-$(PBS_PYTHON)+$(PBS_RELEASE)-$(PBS_TRIPLE_$(PBS_PLATFORM))-install_only_stripped.tar.gz

SRC_DIR=sources/strata
LLAMA_DIR=sources/llama.cpp
STAMP=.stamp

.PHONY: strata package-stub engine engine-packages engine-dsv41 engine-python engine-source build package test clean purge print-packages print-asset print-dsv41-packages print-dsv41-asset print-dsv41-sha256 check-pins

strata:
	CGO_ENABLED=0 $(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o strata ./

print-packages:
	@echo "$(STRATA_PACKAGES)"

print-dsv41-packages:
	@echo "$(STRATA_DSV41_PACKAGES)"

print-dsv41-asset:
	@echo "$(call asset_dsv41,$(PKG))"

print-dsv41-sha256:
	@echo "$(STRATA_DSV41_SHA256_$(PKG))"

print-asset:
	@echo "$(call asset_name,$(PKG))"

# Fails with one clear line when a pin is malformed (used by
# scripts/build/strata-pins_test.sh and handy before a bump PR).
# No recipe below uses $(MAKE): Dockerfile.golang probes `make -n engine`, and
# make still runs recipe lines that mention $(MAKE) under -n.
check-pins:
	@fail=0; \
	$(foreach v,$(filter STRATA_SHA256_% STRATA_DSV41_SHA256_% PBS_SHA256_%,$(.VARIABLES)),echo '$($(v))' | grep -Eq '^[0-9a-f]{64}$$' || { echo 'bad pin $(v)' >&2; fail=1; }; ) \
	echo "$(STRATA_COMMIT)" | grep -Eq '^[0-9a-f]{40}$$' || { echo "bad STRATA_COMMIT" >&2; fail=1; }; \
	echo "$(LLAMA_CPP_COMMIT)" | grep -Eq '^[0-9a-f]{40}$$' || { echo "bad LLAMA_CPP_COMMIT" >&2; fail=1; }; \
	exit $$fail

engine: engine-packages engine-dsv41 engine-python engine-source

# The upstream engine package(s) for this target, verified and unpacked into
# engines/<name>/. Each archive is deleted after unpacking to keep the layer small.
engine-packages:
	@if [ -z "$(STRATA_PACKAGES)" ]; then \
	  echo "strata: Strata has no engine package for BUILD_TYPE='$(BUILD_TYPE)' CUDA_MAJOR_VERSION='$(CUDA_MAJOR_VERSION)' on $(UNAME_S)/$(UNAME_M)." >&2; \
	  echo "strata: supported targets are NVIDIA CUDA 12 and 13 (x86_64), NVIDIA CUDA 13 on arm64 (DGX Spark), AMD ROCm (x86_64), Intel SYCL (x86_64) and Apple Silicon (Metal)." >&2; \
	  exit 1; \
	fi
	@set -e; \
	$(foreach pkg,$(STRATA_PACKAGES),sh ./fetch-engine.sh '$(call engine_dir,$(pkg))' '$(call asset_name,$(pkg))' '$(STRATA_SHA256_$(pkg))' '$(STRATA_REPO)/releases/download/$(STRATA_VERSION)' '$(STRATA_ENGINE_VERSION)'; )

# The DeepSeek V4.1 engine package for this target, into engines-dsv41/<name>/.
# Targets without one (see above) ship the Qwen engine only; that is not an error.
engine-dsv41:
	@if [ -z "$(STRATA_DSV41_PACKAGES)" ]; then \
	  echo "==> no DeepSeek V4.1 engine package for BUILD_TYPE='$(BUILD_TYPE)' CUDA_MAJOR_VERSION='$(CUDA_MAJOR_VERSION)' on $(UNAME_S)/$(UNAME_M); this image runs the Qwen engine only"; \
	  exit 0; \
	fi
	@set -e; \
	$(foreach pkg,$(STRATA_DSV41_PACKAGES),sh ./fetch-engine.sh '$(call engine_dir,$(pkg))' '$(call asset_dsv41,$(pkg))' '$(STRATA_DSV41_SHA256_$(pkg))' '$(STRATA_REPO)/releases/download/$(STRATA_VERSION)' '$(STRATA_DSV41_ENGINE_VERSION)' engines-dsv41 strata-dsv41; )

# Portable CPython plus the pinned, hash-checked packages the helper imports.
engine-python:
	@if [ -z "$(PBS_PLATFORM)" ]; then echo "strata: no portable Python for $(UNAME_S)/$(UNAME_M)" >&2; exit 1; fi
	@set -e; \
	if [ -f python/$(STAMP)-$(PBS_RELEASE)-$(PBS_PYTHON) ]; then echo "==> python is up to date"; exit 0; fi; \
	rm -rf python; mkdir -p downloads; \
	echo "==> fetching $(PBS_FILE)"; \
	sh ./fetch-asset.sh "https://github.com/astral-sh/python-build-standalone/releases/download/$(PBS_RELEASE)/$(subst +,%2B,$(PBS_FILE))" "$(PBS_SHA256_$(PBS_PLATFORM))" "downloads/$(PBS_FILE)"; \
	tar -xzf "downloads/$(PBS_FILE)" -C .; \
	rm -f "downloads/$(PBS_FILE)"; \
	./python/bin/python3 -m pip install --no-cache-dir --disable-pip-version-check --require-hashes --only-binary=:all: --no-deps -r requirements.txt; \
	./python/bin/python3 -I -c "import regex, jinja2, numpy, yaml, tqdm; print('==> python ok: regex', regex.__version__, 'jinja2', jinja2.__version__, 'numpy', numpy.__version__)"; \
	touch python/$(STAMP)-$(PBS_RELEASE)-$(PBS_PYTHON)

# The Strata files the backend uses, fetched at the pinned commit (the commit
# hash is the integrity check): the three the tokenizer helper imports
# (tools/strata_tokenizer.py, serve/frontend.py and the serve package marker),
# the pack tools that build a model's pack from its GGUF files on the first
# load (tools/iq_pack.py with gguf_reader.py and _paths.py), and the licence.
# For DeepSeek V4.1 it also takes serve/dsml.py (the tool-call parser), the
# V4.1 chat template of serve/fixtures and tools/dsv41/pack_gguf.py.
# iq_pack.py reads gguf-py from third_party/llama.cpp/gguf-py under the Strata
# tree, so that folder is fetched from llama.cpp at the commit Strata pins.
engine-source:
	@set -e; \
	if [ -f strata-src/$(STAMP)-$(STRATA_COMMIT) ]; then echo "==> strata-src is up to date"; exit 0; fi; \
	rm -rf $(SRC_DIR) strata-src; mkdir -p $(SRC_DIR) strata-src; \
	cd $(SRC_DIR) && git init -q && git remote add origin $(STRATA_REPO) && \
	git sparse-checkout init --no-cone && \
	git sparse-checkout set /LICENSE /tools/strata_tokenizer.py /tools/iq_pack.py /tools/gguf_reader.py /tools/_paths.py /serve/__init__.py /serve/frontend.py /serve/dsml.py /serve/fixtures/deepseek41/chat_template.jinja /tools/dsv41/pack_gguf.py && \
	git fetch -q --depth 1 --filter=blob:none origin $(STRATA_COMMIT) && \
	git checkout -q FETCH_HEAD; \
	cd "$(CURDIR)"; \
	rm -rf $(LLAMA_DIR); mkdir -p $(LLAMA_DIR); \
	cd $(LLAMA_DIR) && git init -q && git remote add origin $(LLAMA_CPP_REPO) && \
	git sparse-checkout init --no-cone && \
	git sparse-checkout set /gguf-py/gguf /LICENSE && \
	git fetch -q --depth 1 --filter=blob:none origin $(LLAMA_CPP_COMMIT) && \
	git checkout -q FETCH_HEAD; \
	cd "$(CURDIR)"; \
	mkdir -p strata-src/tools/dsv41 strata-src/serve/fixtures/deepseek41 strata-src/third_party/llama.cpp; \
	cp $(SRC_DIR)/LICENSE strata-src/LICENSE; \
	cp $(SRC_DIR)/tools/strata_tokenizer.py $(SRC_DIR)/tools/iq_pack.py $(SRC_DIR)/tools/gguf_reader.py $(SRC_DIR)/tools/_paths.py strata-src/tools/; \
	cp -r $(LLAMA_DIR)/gguf-py strata-src/third_party/llama.cpp/gguf-py; \
	cp $(LLAMA_DIR)/LICENSE strata-src/third_party/llama.cpp/LICENSE; \
	cp $(SRC_DIR)/serve/__init__.py $(SRC_DIR)/serve/frontend.py $(SRC_DIR)/serve/dsml.py strata-src/serve/; \
	cp $(SRC_DIR)/serve/fixtures/deepseek41/chat_template.jinja strata-src/serve/fixtures/deepseek41/; \
	cp $(SRC_DIR)/tools/dsv41/pack_gguf.py strata-src/tools/dsv41/; \
	touch strata-src/$(STAMP)-$(STRATA_COMMIT)

package:
	bash package.sh

# The backend with a stub engine and a synthetic tokenizer pack, for the
# tests/e2e-backends suite. Needs no engine package and no GPU, but the
# Python on PATH needs regex and jinja2.
package-stub: strata engine-source
	bash e2e/package-stub.sh

build: engine strata package

test:
	CGO_ENABLED=0 $(GOCMD) test ./...

clean:
	rm -rf strata package package-stub

# Also drops everything fetched: engines, Python, sources and downloads.
purge: clean
	rm -rf engines engines-dsv41 python strata-src sources downloads
