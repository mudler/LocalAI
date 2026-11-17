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
# Upstream versions each platform on its own and does not attach every package
# to every release, so each package carries its own release tag, version and
# SHA-256. Packages of different versions and tags side by side are normal.
#
# STRATA_VERSION   the newest upstream release tag
# STRATA_COMMIT    the commit of that tag; the Python modules the backend
#                  needs (tokenizer, chat template) are fetched at it
#
# Per package of the engine (file strata-engine-...):
#   STRATA_ENGINE_TAG_<pkg>      release the package is downloaded from
#   STRATA_ENGINE_VERSION_<pkg>  the version in the package file name
#   STRATA_ENGINE_SHA256_<pkg>   SHA-256 of the .tar.gz
# The URL is $(STRATA_REPO)/releases/download/<TAG>/<file name>.
# ---------------------------------------------------------------------------
STRATA_VERSION?=v0.2.15
STRATA_COMMIT?=c77572806996349b49995e4f98d41b5294d36a21

# Qwen engine
STRATA_ENGINE_TAG_linux_x86_64_cuda_cuda12?=v0.2.14
STRATA_ENGINE_VERSION_linux_x86_64_cuda_cuda12?=0.1.60
STRATA_ENGINE_SHA256_linux_x86_64_cuda_cuda12?=a50a4d1efd7fbf0da03a0a2d4d91b0ac059d92579ae41f611e49bc9228262856
STRATA_ENGINE_TAG_linux_x86_64_cuda_cuda13?=v0.2.14
STRATA_ENGINE_VERSION_linux_x86_64_cuda_cuda13?=0.1.60
STRATA_ENGINE_SHA256_linux_x86_64_cuda_cuda13?=a72c194c8f6de96629ed53079b5e344ab81223d45667a53f6a6d79398895aa50
STRATA_ENGINE_TAG_linux_aarch64_cuda_cuda13?=v0.2.15
STRATA_ENGINE_VERSION_linux_aarch64_cuda_cuda13?=0.1.59
STRATA_ENGINE_SHA256_linux_aarch64_cuda_cuda13?=b213d9b173aae99645fc5922d42276fa1ed17ac67f7b788c55c912ba49e148f1
STRATA_ENGINE_TAG_linux_x86_64_hip_rx9070?=v0.2.14
STRATA_ENGINE_VERSION_linux_x86_64_hip_rx9070?=0.1.60
STRATA_ENGINE_SHA256_linux_x86_64_hip_rx9070?=4fc045afc0905cf1338b39c7bc9f940d2f4905a82330d9e07ab185215af77ad8
STRATA_ENGINE_TAG_linux_x86_64_hip_rx7900?=v0.2.14
STRATA_ENGINE_VERSION_linux_x86_64_hip_rx7900?=0.1.60
STRATA_ENGINE_SHA256_linux_x86_64_hip_rx7900?=ff8093d4d48e85d10b9bc317cc4fd518961e011af6dc0097e9d6384bd04bab82
STRATA_ENGINE_TAG_linux_x86_64_hip_rx6000?=v0.2.14
STRATA_ENGINE_VERSION_linux_x86_64_hip_rx6000?=0.1.60
STRATA_ENGINE_SHA256_linux_x86_64_hip_rx6000?=45b1e3aed1241e72594e9980cb07171d2b976c7e473940faaf007b9eb1cfbb51
STRATA_ENGINE_TAG_linux_x86_64_hip_halo?=v0.2.14
STRATA_ENGINE_VERSION_linux_x86_64_hip_halo?=0.1.60
STRATA_ENGINE_SHA256_linux_x86_64_hip_halo?=cea2393ecea5e0f3b1ae90ba525d2db8a7f3ef2817dda184cfcfe6be8ddf081a
STRATA_ENGINE_TAG_linux_x86_64_sycl?=v0.2.13
STRATA_ENGINE_VERSION_linux_x86_64_sycl?=0.1.59
STRATA_ENGINE_SHA256_linux_x86_64_sycl?=8b2586ab57a2ad8df593d11dc6a3509620ca49fb352692ae0f7f4f7d014a7839
STRATA_ENGINE_TAG_macos_arm64_metal?=v0.2.14
STRATA_ENGINE_VERSION_macos_arm64_metal?=0.1.60
STRATA_ENGINE_SHA256_macos_arm64_metal?=31ce1cd98bef83938434c15fcc94c1768340037b8fe7f292ee82836db0ca074d

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
      endif
    else ifneq ($(filter aarch64 arm64,$(UNAME_M)),)
      # Upstream's arm64 package is built for the DGX Spark (sm_121, CUDA 13).
      ifeq ($(CUDA_MAJOR_VERSION),13)
        STRATA_PACKAGES:=linux_aarch64_cuda_cuda13
      endif
    endif
  else ifeq ($(BUILD_TYPE),hipblas)
    ifeq ($(UNAME_M),x86_64)
      # One package per GPU family; strata-engine picks the one that matches the
      # card at run time (RDNA4, RDNA3, RDNA2, Strix Halo).
      STRATA_PACKAGES:=linux_x86_64_hip_rx9070 linux_x86_64_hip_rx7900 linux_x86_64_hip_rx6000 linux_x86_64_hip_halo
    endif
  else ifneq ($(filter sycl_f16 sycl_f32 intel,$(BUILD_TYPE)),)
    ifeq ($(UNAME_M),x86_64)
      STRATA_PACKAGES:=linux_x86_64_sycl
    endif
  endif
endif

# Package key -> directory name inside the image (engines/<name>).
engine_dir=$(patsubst linux_x86_64_cuda_%,%,$(patsubst linux_aarch64_cuda_%,%,$(patsubst linux_x86_64_hip_%,hip-%,$(patsubst linux_x86_64_sycl,sycl,$(patsubst macos_arm64_metal,metal,$(1))))))
# Package key -> upstream file name: linux_x86_64_hip_halo -> linux-x86_64-hip-halo
asset_name=strata-engine-$(STRATA_ENGINE_VERSION_$(1))-$(subst x86-64,x86_64,$(subst _,-,$(1))).tar.gz

PBS_TRIPLE_linux_x86_64=x86_64-unknown-linux-gnu
PBS_TRIPLE_linux_aarch64=aarch64-unknown-linux-gnu
PBS_TRIPLE_macos_arm64=aarch64-apple-darwin
PBS_FILE=cpython-$(PBS_PYTHON)+$(PBS_RELEASE)-$(PBS_TRIPLE_$(PBS_PLATFORM))-install_only_stripped.tar.gz

SRC_DIR=sources/strata
LLAMA_DIR=sources/llama.cpp
STAMP=.stamp

.PHONY: strata package-stub engine engine-packages engine-python engine-source build package test clean purge print-packages print-asset check-pins

strata:
	CGO_ENABLED=0 $(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o strata ./

print-packages:
	@echo "$(STRATA_PACKAGES)"

print-asset:
	@echo "$(call asset_name,$(PKG))"

# Fails with one clear line when a pin is malformed (used by
# scripts/build/strata-pins_test.sh and handy before a bump PR).
# No recipe below uses $(MAKE): Dockerfile.golang probes `make -n engine`, and
# make still runs recipe lines that mention $(MAKE) under -n.
check-pins:
	@fail=0; \
	$(foreach v,$(filter STRATA_ENGINE_SHA256_%,$(.VARIABLES)),echo '$($(subst _SHA256_,_TAG_,$(v)))' | grep -Eq '^v[0-9][0-9.]*$$' || { echo 'bad or missing tag for $(v)' >&2; fail=1; }; echo '$($(subst _SHA256_,_VERSION_,$(v)))' | grep -Eq '^[0-9][0-9.]*$$' || { echo 'bad or missing version for $(v)' >&2; fail=1; }; ) \
	$(foreach v,$(filter STRATA_ENGINE_SHA256_% PBS_SHA256_%,$(.VARIABLES)),echo '$($(v))' | grep -Eq '^[0-9a-f]{64}$$' || { echo 'bad pin $(v)' >&2; fail=1; }; ) \
	echo "$(STRATA_COMMIT)" | grep -Eq '^[0-9a-f]{40}$$' || { echo "bad STRATA_COMMIT" >&2; fail=1; }; \
	echo "$(LLAMA_CPP_COMMIT)" | grep -Eq '^[0-9a-f]{40}$$' || { echo "bad LLAMA_CPP_COMMIT" >&2; fail=1; }; \
	exit $$fail

engine: engine-packages engine-python engine-source

# The upstream engine package(s) for this target, verified and unpacked into
# engines/<name>/. Each archive is deleted after unpacking to keep the layer small.
engine-packages:
	@if [ -z "$(STRATA_PACKAGES)" ]; then \
	  echo "strata: Strata has no engine package for BUILD_TYPE='$(BUILD_TYPE)' CUDA_MAJOR_VERSION='$(CUDA_MAJOR_VERSION)' on $(UNAME_S)/$(UNAME_M)." >&2; \
	  echo "strata: supported targets are NVIDIA CUDA 12 and 13 (x86_64), NVIDIA CUDA 13 on arm64 (DGX Spark), AMD ROCm (x86_64), Intel SYCL (x86_64) and Apple Silicon (Metal)." >&2; \
	  exit 1; \
	fi
	@set -e; \
	$(foreach pkg,$(STRATA_PACKAGES),sh ./fetch-engine.sh '$(call engine_dir,$(pkg))' '$(call asset_name,$(pkg))' '$(STRATA_ENGINE_SHA256_$(pkg))' '$(STRATA_REPO)/releases/download/$(STRATA_ENGINE_TAG_$(pkg))' '$(STRATA_ENGINE_VERSION_$(pkg))'; )

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
# load (tools/iq_pack.py with gguf_reader.py and _paths.py, and the strata_gguf,
# strata_quants and strata_quant_tables modules it uses as its GGUF library),
# and the licence.
# Strata v0.2.15 no longer carries any DeepSeek code (serve/dsml.py, the V4.1
# chat template and tools/dsv41/pack_gguf.py are gone), so none is fetched.
# Since v0.2.15 iq_pack.py uses strata_gguf by default. llama.cpp's gguf-py,
# fetched at the commit Strata pins into third_party/llama.cpp/gguf-py under
# the Strata tree, stays available as the STRATA_GGUF_PY alternative.
engine-source:
	@set -e; \
	if [ -f strata-src/$(STAMP)-$(STRATA_COMMIT) ]; then echo "==> strata-src is up to date"; exit 0; fi; \
	rm -rf $(SRC_DIR) strata-src; mkdir -p $(SRC_DIR) strata-src; \
	cd $(SRC_DIR) && git init -q && git remote add origin $(STRATA_REPO) && \
	git sparse-checkout init --no-cone && \
	git sparse-checkout set /LICENSE /tools/strata_tokenizer.py /tools/iq_pack.py /tools/gguf_reader.py /tools/_paths.py /tools/strata_gguf.py /tools/strata_quants.py /tools/strata_quant_tables.py /serve/__init__.py /serve/frontend.py && \
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
	mkdir -p strata-src/tools strata-src/serve strata-src/third_party/llama.cpp; \
	cp $(SRC_DIR)/LICENSE strata-src/LICENSE; \
	cp $(SRC_DIR)/tools/strata_tokenizer.py $(SRC_DIR)/tools/iq_pack.py $(SRC_DIR)/tools/gguf_reader.py $(SRC_DIR)/tools/_paths.py $(SRC_DIR)/tools/strata_gguf.py $(SRC_DIR)/tools/strata_quants.py $(SRC_DIR)/tools/strata_quant_tables.py strata-src/tools/; \
	cp -r $(LLAMA_DIR)/gguf-py strata-src/third_party/llama.cpp/gguf-py; \
	cp $(LLAMA_DIR)/LICENSE strata-src/third_party/llama.cpp/LICENSE; \
	cp $(SRC_DIR)/serve/__init__.py $(SRC_DIR)/serve/frontend.py strata-src/serve/; \
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
	rm -rf engines python strata-src sources downloads
