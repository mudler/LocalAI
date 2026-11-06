GOCMD=go

# The strata backend is a Go program that drives the Strata inference engine
# (https://github.com/Niko1221/Strata, MIT) as a child process. The engine is
# not compiled here. Upstream publishes ready-made engine packages for every
# target it supports, and this Makefile fetches the one that matches the build
# (BUILD_TYPE, CUDA_MAJOR_VERSION and the CPU architecture), checks it against a
# pinned SHA-256 and unpacks it. See docs/content/features/strata.md.
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
# ---------------------------------------------------------------------------
STRATA_VERSION?=v0.2.11
STRATA_COMMIT?=c3b3f5035506f3e966f1b4b7b188aeaabae92f1d
STRATA_ENGINE_VERSION?=0.1.57
STRATA_SHA256_linux_x86_64_cuda_cuda12?=4c8877c5dd49f75758f667eee64160d186b55fc6eafd4db4e1f6de0836a35b49
STRATA_SHA256_linux_x86_64_cuda_cuda13?=cc58226bffc12216a5a2e6019dfdbf4896042500df132cc3fa3c72a2388a228b
STRATA_SHA256_linux_aarch64_cuda_cuda13?=b871dac15d161696e23625c70026a7f360533751d1dd43be0198ef078679ef8e
STRATA_SHA256_linux_x86_64_hip_rx9070?=ee069a82ba34ea19fa3e5699931b93370a52172a631a46efca85925bfe4525f0
STRATA_SHA256_linux_x86_64_hip_rx7900?=214114bf18cac6a9aaaf94d08876aa0bc6b97d9bdb786bb674968bdcb575b0a8
STRATA_SHA256_linux_x86_64_hip_rx6000?=4a0ed26fcfd16b87fe693f4ede7bd7751c16e6f37d431d303350ef91bbd903cb
STRATA_SHA256_linux_x86_64_hip_halo?=493c5ec9ea09cb57751922e127eea49721d5687b8f62827bda7d6fb4fad36553
STRATA_SHA256_linux_x86_64_sycl?=edf555d126bb6a19aa623041a2676b40c4d691851d1deb88d13c8f012e86d93d
STRATA_SHA256_macos_arm64_metal?=c79551c0f346be0204a277000821c45cd9f557881b7baafd2511315be137db4f

STRATA_REPO?=https://github.com/Niko1221/Strata

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
asset_name=strata-engine-$(STRATA_ENGINE_VERSION)-$(subst x86-64,x86_64,$(subst _,-,$(1))).tar.gz

PBS_TRIPLE_linux_x86_64=x86_64-unknown-linux-gnu
PBS_TRIPLE_linux_aarch64=aarch64-unknown-linux-gnu
PBS_TRIPLE_macos_arm64=aarch64-apple-darwin
PBS_FILE=cpython-$(PBS_PYTHON)+$(PBS_RELEASE)-$(PBS_TRIPLE_$(PBS_PLATFORM))-install_only_stripped.tar.gz

SRC_DIR=sources/strata
STAMP=.stamp

.PHONY: strata engine engine-packages engine-python engine-source build package test clean purge print-packages print-asset check-pins

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
	$(foreach v,$(filter STRATA_SHA256_% PBS_SHA256_%,$(.VARIABLES)),echo '$($(v))' | grep -Eq '^[0-9a-f]{64}$$' || { echo 'bad pin $(v)' >&2; fail=1; }; ) \
	echo "$(STRATA_COMMIT)" | grep -Eq '^[0-9a-f]{40}$$' || { echo "bad STRATA_COMMIT" >&2; fail=1; }; \
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
	$(foreach pkg,$(STRATA_PACKAGES),sh ./fetch-engine.sh '$(call engine_dir,$(pkg))' '$(call asset_name,$(pkg))' '$(STRATA_SHA256_$(pkg))' '$(STRATA_REPO)/releases/download/$(STRATA_VERSION)' '$(STRATA_ENGINE_VERSION)'; )

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
	./python/bin/python3 -I -c "import regex, jinja2; print('==> python ok: regex', regex.__version__, 'jinja2', jinja2.__version__)"; \
	touch python/$(STAMP)-$(PBS_RELEASE)-$(PBS_PYTHON)

# The three Strata files the helper imports (tools/strata_tokenizer.py,
# serve/frontend.py and the serve package marker) and the licence, fetched at the
# pinned commit. The commit hash is the integrity check.
engine-source:
	@set -e; \
	if [ -f strata-src/$(STAMP)-$(STRATA_COMMIT) ]; then echo "==> strata-src is up to date"; exit 0; fi; \
	rm -rf $(SRC_DIR) strata-src; mkdir -p $(SRC_DIR) strata-src; \
	cd $(SRC_DIR) && git init -q && git remote add origin $(STRATA_REPO) && \
	git sparse-checkout init --no-cone && \
	git sparse-checkout set /LICENSE /tools/strata_tokenizer.py /serve/__init__.py /serve/frontend.py && \
	git fetch -q --depth 1 --filter=blob:none origin $(STRATA_COMMIT) && \
	git checkout -q FETCH_HEAD; \
	cd "$(CURDIR)"; \
	mkdir -p strata-src/tools strata-src/serve; \
	cp $(SRC_DIR)/LICENSE strata-src/LICENSE; \
	cp $(SRC_DIR)/tools/strata_tokenizer.py strata-src/tools/; \
	cp $(SRC_DIR)/serve/__init__.py $(SRC_DIR)/serve/frontend.py strata-src/serve/; \
	touch strata-src/$(STAMP)-$(STRATA_COMMIT)

package:
	bash package.sh

build: engine strata package

test:
	CGO_ENABLED=0 $(GOCMD) test ./...

clean:
	rm -rf strata package

# Also drops everything fetched: engines, Python, sources and downloads.
purge: clean
	rm -rf engines python strata-src sources downloads
