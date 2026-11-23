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
# Tencent Hy4-Flash runs on its own engine program, strata-hy4, shipped as
# separate packages (file strata-hy4-...) with the same targets. Its pins are
# STRATA_HY4_TAG_<pkg>, STRATA_HY4_VERSION_<pkg> and STRATA_HY4_SHA256_<pkg>.
# Thinking Machines Inkling 2 Mini likewise runs on its own engine program,
# strata-inkling (file strata-inkling-...), with the pins STRATA_INKLING_TAG_<pkg>,
# STRATA_INKLING_VERSION_<pkg> and STRATA_INKLING_SHA256_<pkg>.
# ---------------------------------------------------------------------------
STRATA_VERSION?=v0.2.20
STRATA_COMMIT?=a34979fc94f3d126b9bb8210f4ad311345688b52

# Qwen engine
STRATA_ENGINE_TAG_linux_x86_64_cuda_cuda12?=v0.2.20
STRATA_ENGINE_VERSION_linux_x86_64_cuda_cuda12?=0.1.66
STRATA_ENGINE_SHA256_linux_x86_64_cuda_cuda12?=74980aebedc70fb2cfcd96b3c15874cb3e7e517700ecdf8ed811247a9a577f30
STRATA_ENGINE_TAG_linux_x86_64_cuda_cuda13?=v0.2.20
STRATA_ENGINE_VERSION_linux_x86_64_cuda_cuda13?=0.1.66
STRATA_ENGINE_SHA256_linux_x86_64_cuda_cuda13?=13c5bebeaf24574b02307857bce6abfd3f861f3682ca98dbe36c1c461fd5e059
STRATA_ENGINE_TAG_linux_aarch64_cuda_cuda13?=v0.2.20
STRATA_ENGINE_VERSION_linux_aarch64_cuda_cuda13?=0.1.66
STRATA_ENGINE_SHA256_linux_aarch64_cuda_cuda13?=de6fdacfa1d7daf1c86c64add0d8e82844783919af39fc172d6b5134816be1be
STRATA_ENGINE_TAG_linux_x86_64_hip_rx9070?=v0.2.20
STRATA_ENGINE_VERSION_linux_x86_64_hip_rx9070?=0.1.66
STRATA_ENGINE_SHA256_linux_x86_64_hip_rx9070?=8ceaf7a82603a70b77f03bc890621626207ab50c57f5b3b1afe340b5cbc03d9b
STRATA_ENGINE_TAG_linux_x86_64_hip_rx7900?=v0.2.20
STRATA_ENGINE_VERSION_linux_x86_64_hip_rx7900?=0.1.66
STRATA_ENGINE_SHA256_linux_x86_64_hip_rx7900?=a9cc741a60c101689c0c8dbe5b731382526f22c1c55a92f22b2c1dfcd26dd86f
STRATA_ENGINE_TAG_linux_x86_64_hip_rx6000?=v0.2.20
STRATA_ENGINE_VERSION_linux_x86_64_hip_rx6000?=0.1.66
STRATA_ENGINE_SHA256_linux_x86_64_hip_rx6000?=d850ff4929f13ac4553627236cc3e25d0e1b8a636d0cae17714bf3801598e8b1
STRATA_ENGINE_TAG_linux_x86_64_hip_halo?=v0.2.20
STRATA_ENGINE_VERSION_linux_x86_64_hip_halo?=0.1.66
STRATA_ENGINE_SHA256_linux_x86_64_hip_halo?=9198a10463da40c5330c9fb50ea363312aacf2c722900d57cf0512e704527d31
STRATA_ENGINE_TAG_linux_x86_64_sycl?=v0.2.20
STRATA_ENGINE_VERSION_linux_x86_64_sycl?=0.1.66
STRATA_ENGINE_SHA256_linux_x86_64_sycl?=b5c9599ac55ab0bf09f7663ae71c75cf8dfc247e05015a9344c5e513caa36c41
STRATA_ENGINE_TAG_macos_arm64_metal?=v0.2.20
STRATA_ENGINE_VERSION_macos_arm64_metal?=0.1.66
STRATA_ENGINE_SHA256_macos_arm64_metal?=d135beed9a508d0dad37300a2589097ac898e4226fe9f9ac255404b3a555d89a

# Hy4-Flash engine (strata-hy4, a separate program; same packages per target)
STRATA_HY4_TAG_linux_x86_64_cuda_cuda12?=v0.2.20
STRATA_HY4_VERSION_linux_x86_64_cuda_cuda12?=0.1.66
STRATA_HY4_SHA256_linux_x86_64_cuda_cuda12?=fa535d0321adb87dac7dafdc7a1a4dc185121bbfe85d41e1bd2da281ad676e3b
STRATA_HY4_TAG_linux_x86_64_cuda_cuda13?=v0.2.20
STRATA_HY4_VERSION_linux_x86_64_cuda_cuda13?=0.1.66
STRATA_HY4_SHA256_linux_x86_64_cuda_cuda13?=112355a4beb4c8d468f2d682f48d8f9d75c1397502ac03977521cc61277a7f0c
STRATA_HY4_TAG_linux_aarch64_cuda_cuda13?=v0.2.20
STRATA_HY4_VERSION_linux_aarch64_cuda_cuda13?=0.1.66
STRATA_HY4_SHA256_linux_aarch64_cuda_cuda13?=69981deb045e577c9285c8045e603de4b23c6f44914371c94b09f5ea90f92bfc
STRATA_HY4_TAG_linux_x86_64_hip_rx9070?=v0.2.20
STRATA_HY4_VERSION_linux_x86_64_hip_rx9070?=0.1.66
STRATA_HY4_SHA256_linux_x86_64_hip_rx9070?=db1710fdd2b4de0a9d806af82f28dbc7dcfe8e4b6ee36a8d5cde136949f5cbf3
STRATA_HY4_TAG_linux_x86_64_hip_rx7900?=v0.2.20
STRATA_HY4_VERSION_linux_x86_64_hip_rx7900?=0.1.66
STRATA_HY4_SHA256_linux_x86_64_hip_rx7900?=4190c7b10b8d3a816ecc8a24a22fc2fcea5e9b32775ba5b5781794bffd907d4f
STRATA_HY4_TAG_linux_x86_64_hip_rx6000?=v0.2.20
STRATA_HY4_VERSION_linux_x86_64_hip_rx6000?=0.1.66
STRATA_HY4_SHA256_linux_x86_64_hip_rx6000?=72c41f0a7a3dd1fb126dadf4d7541bfd2d5ecbbc8515d3327f90aa1e78d37fa7
STRATA_HY4_TAG_linux_x86_64_hip_halo?=v0.2.20
STRATA_HY4_VERSION_linux_x86_64_hip_halo?=0.1.66
STRATA_HY4_SHA256_linux_x86_64_hip_halo?=34d9ba0e067eb7016c613721a3b34068638f33e8a6a0b19d3b0e3f0413ee4024
STRATA_HY4_TAG_linux_x86_64_sycl?=v0.2.20
STRATA_HY4_VERSION_linux_x86_64_sycl?=0.1.66
STRATA_HY4_SHA256_linux_x86_64_sycl?=490130fe9c161c3d56acf4f03468f7026807e069a547ec137e4cfa963593d46c
STRATA_HY4_TAG_macos_arm64_metal?=v0.2.20
STRATA_HY4_VERSION_macos_arm64_metal?=0.1.66
STRATA_HY4_SHA256_macos_arm64_metal?=5db9e8e07f9c469d2e4c9059451cf83293dbfd305d8a1dc03f3cf31530d0ce17

# Inkling 2 Mini engine (strata-inkling, a separate program; same packages per target)
STRATA_INKLING_TAG_linux_x86_64_cuda_cuda12?=v0.2.20
STRATA_INKLING_VERSION_linux_x86_64_cuda_cuda12?=0.1.66
STRATA_INKLING_SHA256_linux_x86_64_cuda_cuda12?=2265b17d4d1865ea7c1ef627f01604a44b1508551793ba8b423f9e7a2e1675f7
STRATA_INKLING_TAG_linux_x86_64_cuda_cuda13?=v0.2.20
STRATA_INKLING_VERSION_linux_x86_64_cuda_cuda13?=0.1.66
STRATA_INKLING_SHA256_linux_x86_64_cuda_cuda13?=fdaa87037918798023bd34b4e2e1e23832418d59b9421f279995b274648847c3
STRATA_INKLING_TAG_linux_aarch64_cuda_cuda13?=v0.2.20
STRATA_INKLING_VERSION_linux_aarch64_cuda_cuda13?=0.1.66
STRATA_INKLING_SHA256_linux_aarch64_cuda_cuda13?=a01f0e1f2e629d53ff17f1defd7164076844f62ed73cf747298afe68e43a77c1
STRATA_INKLING_TAG_linux_x86_64_hip_rx9070?=v0.2.20
STRATA_INKLING_VERSION_linux_x86_64_hip_rx9070?=0.1.66
STRATA_INKLING_SHA256_linux_x86_64_hip_rx9070?=3cc99f387b2059741dd3956acc0361a889aaeb26bcfa2e86bebe2d312c5578a1
STRATA_INKLING_TAG_linux_x86_64_hip_rx7900?=v0.2.20
STRATA_INKLING_VERSION_linux_x86_64_hip_rx7900?=0.1.66
STRATA_INKLING_SHA256_linux_x86_64_hip_rx7900?=4def626b047c4da3d1a2e4973ddd0881e984335e8f068fad60ee0da678942cbd
STRATA_INKLING_TAG_linux_x86_64_hip_rx6000?=v0.2.20
STRATA_INKLING_VERSION_linux_x86_64_hip_rx6000?=0.1.66
STRATA_INKLING_SHA256_linux_x86_64_hip_rx6000?=154222107458c6d2cd187e7776e5a87373cc0c568d3641c1b3efa10c6091c403
STRATA_INKLING_TAG_linux_x86_64_hip_halo?=v0.2.20
STRATA_INKLING_VERSION_linux_x86_64_hip_halo?=0.1.66
STRATA_INKLING_SHA256_linux_x86_64_hip_halo?=9ef6feccf3cd868fd6d68e08eab80f47ba087ce1b5b7ce2eb334addc13517139
STRATA_INKLING_TAG_linux_x86_64_sycl?=v0.2.20
STRATA_INKLING_VERSION_linux_x86_64_sycl?=0.1.66
STRATA_INKLING_SHA256_linux_x86_64_sycl?=81b72fd2d8c3d323a38ca783e97cd61d1b6ad2093b376fe86b43e6b840add509
STRATA_INKLING_TAG_macos_arm64_metal?=v0.2.20
STRATA_INKLING_VERSION_macos_arm64_metal?=0.1.66
STRATA_INKLING_SHA256_macos_arm64_metal?=4fee154bc838e898bbe12b4ad258712ad90e1d50e5be5e3473db3724495c9929

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
asset_name_hy4=strata-hy4-$(STRATA_HY4_VERSION_$(1))-$(subst x86-64,x86_64,$(subst _,-,$(1))).tar.gz
asset_name_inkling=strata-inkling-$(STRATA_INKLING_VERSION_$(1))-$(subst x86-64,x86_64,$(subst _,-,$(1))).tar.gz

PBS_TRIPLE_linux_x86_64=x86_64-unknown-linux-gnu
PBS_TRIPLE_linux_aarch64=aarch64-unknown-linux-gnu
PBS_TRIPLE_macos_arm64=aarch64-apple-darwin
PBS_FILE=cpython-$(PBS_PYTHON)+$(PBS_RELEASE)-$(PBS_TRIPLE_$(PBS_PLATFORM))-install_only_stripped.tar.gz

SRC_DIR=sources/strata
LLAMA_DIR=sources/llama.cpp
STAMP=.stamp

.PHONY: strata package-stub engine engine-packages engine-packages-hy4 engine-packages-inkling engine-python engine-source build package test clean purge print-packages print-asset check-pins

strata:
	CGO_ENABLED=0 $(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o strata ./

print-packages:
	@echo "$(STRATA_PACKAGES)"

print-asset:
	@echo "$(call asset_name,$(PKG))"

print-asset-hy4:
	@echo "$(call asset_name_hy4,$(PKG))"

print-asset-inkling:
	@echo "$(call asset_name_inkling,$(PKG))"

# Fails with one clear line when a pin is malformed (used by
# scripts/build/strata-pins_test.sh and handy before a bump PR).
# No recipe below uses $(MAKE): Dockerfile.golang probes `make -n engine`, and
# make still runs recipe lines that mention $(MAKE) under -n.
check-pins:
	@fail=0; \
	$(foreach v,$(filter STRATA_ENGINE_SHA256_%,$(.VARIABLES)),echo '$($(subst _SHA256_,_TAG_,$(v)))' | grep -Eq '^v[0-9][0-9.]*$$' || { echo 'bad or missing tag for $(v)' >&2; fail=1; }; echo '$($(subst _SHA256_,_VERSION_,$(v)))' | grep -Eq '^[0-9][0-9.]*$$' || { echo 'bad or missing version for $(v)' >&2; fail=1; }; ) \
	$(foreach v,$(filter STRATA_HY4_SHA256_%,$(.VARIABLES)),echo '$($(subst _SHA256_,_TAG_,$(v)))' | grep -Eq '^v[0-9][0-9.]*$$' || { echo 'bad or missing tag for $(v)' >&2; fail=1; }; echo '$($(subst _SHA256_,_VERSION_,$(v)))' | grep -Eq '^[0-9][0-9.]*$$' || { echo 'bad or missing version for $(v)' >&2; fail=1; }; ) \
	$(foreach v,$(filter STRATA_INKLING_SHA256_%,$(.VARIABLES)),echo '$($(subst _SHA256_,_TAG_,$(v)))' | grep -Eq '^v[0-9][0-9.]*$$' || { echo 'bad or missing tag for $(v)' >&2; fail=1; }; echo '$($(subst _SHA256_,_VERSION_,$(v)))' | grep -Eq '^[0-9][0-9.]*$$' || { echo 'bad or missing version for $(v)' >&2; fail=1; }; ) \
	$(foreach v,$(filter STRATA_ENGINE_SHA256_% STRATA_HY4_SHA256_% STRATA_INKLING_SHA256_% PBS_SHA256_%,$(.VARIABLES)),echo '$($(v))' | grep -Eq '^[0-9a-f]{64}$$' || { echo 'bad pin $(v)' >&2; fail=1; }; ) \
	echo "$(STRATA_COMMIT)" | grep -Eq '^[0-9a-f]{40}$$' || { echo "bad STRATA_COMMIT" >&2; fail=1; }; \
	echo "$(LLAMA_CPP_COMMIT)" | grep -Eq '^[0-9a-f]{40}$$' || { echo "bad LLAMA_CPP_COMMIT" >&2; fail=1; }; \
	exit $$fail

engine: engine-packages engine-packages-hy4 engine-packages-inkling engine-python engine-source

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

# The Hy4-Flash engine package(s) for the same target, into engines-hy4/<name>/.
# Same package set as above, so the check there already refused an unsupported target.
engine-packages-hy4:
	@if [ -z "$(STRATA_PACKAGES)" ]; then exit 1; fi
	@set -e; \
	$(foreach pkg,$(STRATA_PACKAGES),sh ./fetch-engine.sh '$(call engine_dir,$(pkg))' '$(call asset_name_hy4,$(pkg))' '$(STRATA_HY4_SHA256_$(pkg))' '$(STRATA_REPO)/releases/download/$(STRATA_HY4_TAG_$(pkg))' '$(STRATA_HY4_VERSION_$(pkg))' engines-hy4 strata-hy4; )

# The Inkling 2 Mini engine package(s) for the same target, into engines-inkling/<name>/.
engine-packages-inkling:
	@if [ -z "$(STRATA_PACKAGES)" ]; then exit 1; fi
	@set -e; \
	$(foreach pkg,$(STRATA_PACKAGES),sh ./fetch-engine.sh '$(call engine_dir,$(pkg))' '$(call asset_name_inkling,$(pkg))' '$(STRATA_INKLING_SHA256_$(pkg))' '$(STRATA_REPO)/releases/download/$(STRATA_INKLING_TAG_$(pkg))' '$(STRATA_INKLING_VERSION_$(pkg))' engines-inkling strata-inkling; )

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
# From Strata v0.2.16 the Python sources are in the terminal/ folder of the
# repository (terminal/tools, terminal/serve); before, they were at the top.
# Both places are fetched and the files are copied from the one that exists,
# to the same tools/ and serve/ folders of strata-src.
# Strata v0.2.15 no longer carries any DeepSeek code (serve/dsml.py, the V4.1
# chat template and tools/dsv41/pack_gguf.py are gone), so none is fetched.
# Hy4-Flash adds tools/hy4_tokenizer.py (the tokenizer directory from a model
# file), serve/hy4_dialect.py (its tagged reasoning and tool-call tokens) and
# serve/toolformat.py (which parser a chat template needs); serve/frontend.py
# imports the last two when it runs a Hy4 template.
# Inkling 2 Mini adds tools/inkling_tokenizer.py and serve/inkling_dialect.py
# (its channel tokens, the prompt opener and the forced tool-call text).
# Since v0.2.15 iq_pack.py uses strata_gguf by default. llama.cpp's gguf-py,
# fetched at the commit Strata pins into third_party/llama.cpp/gguf-py under
# the Strata tree, stays available as the STRATA_GGUF_PY alternative.
engine-source:
	@set -e; \
	if [ -f strata-src/$(STAMP)-$(STRATA_COMMIT) ]; then echo "==> strata-src is up to date"; exit 0; fi; \
	rm -rf $(SRC_DIR) strata-src; mkdir -p $(SRC_DIR) strata-src; \
	cd $(SRC_DIR) && git init -q && git remote add origin $(STRATA_REPO) && \
	git sparse-checkout init --no-cone && \
	git sparse-checkout set /LICENSE /tools/strata_tokenizer.py /tools/iq_pack.py /tools/gguf_reader.py /tools/_paths.py /tools/strata_gguf.py /tools/strata_quants.py /tools/strata_quant_tables.py /serve/__init__.py /serve/frontend.py /serve/hy4_dialect.py /serve/toolformat.py /tools/hy4_tokenizer.py /serve/inkling_dialect.py /tools/inkling_tokenizer.py /terminal/tools/strata_tokenizer.py /terminal/tools/iq_pack.py /terminal/tools/gguf_reader.py /terminal/tools/_paths.py /terminal/tools/strata_gguf.py /terminal/tools/strata_quants.py /terminal/tools/strata_quant_tables.py /terminal/serve/__init__.py /terminal/serve/frontend.py /terminal/serve/hy4_dialect.py /terminal/serve/toolformat.py /terminal/tools/hy4_tokenizer.py /terminal/serve/inkling_dialect.py /terminal/tools/inkling_tokenizer.py && \
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
	PYSRC=$(SRC_DIR); [ -d $(SRC_DIR)/terminal/tools ] && PYSRC=$(SRC_DIR)/terminal; \
	cp $$PYSRC/tools/hy4_tokenizer.py $$PYSRC/tools/inkling_tokenizer.py $$PYSRC/tools/strata_tokenizer.py $$PYSRC/tools/iq_pack.py $$PYSRC/tools/gguf_reader.py $$PYSRC/tools/_paths.py $$PYSRC/tools/strata_gguf.py $$PYSRC/tools/strata_quants.py $$PYSRC/tools/strata_quant_tables.py strata-src/tools/; \
	cp -r $(LLAMA_DIR)/gguf-py strata-src/third_party/llama.cpp/gguf-py; \
	cp $(LLAMA_DIR)/LICENSE strata-src/third_party/llama.cpp/LICENSE; \
	cp $$PYSRC/serve/__init__.py $$PYSRC/serve/frontend.py $$PYSRC/serve/hy4_dialect.py $$PYSRC/serve/inkling_dialect.py $$PYSRC/serve/toolformat.py strata-src/serve/; \
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
	rm -rf engines engines-hy4 engines-inkling python strata-src sources downloads
