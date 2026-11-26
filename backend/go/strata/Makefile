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
STRATA_VERSION?=v0.2.22
STRATA_COMMIT?=b28ca95e3e6847f2d5e317994f253b66716deeaa

# Qwen engine
STRATA_ENGINE_TAG_linux_x86_64_cuda_cuda12?=v0.2.22
STRATA_ENGINE_VERSION_linux_x86_64_cuda_cuda12?=0.1.69
STRATA_ENGINE_SHA256_linux_x86_64_cuda_cuda12?=80aecfc14d93bc1678b8e1a14c0e2e92f0bbf99998b9929825cb2a7bf0670e1b
STRATA_ENGINE_TAG_linux_x86_64_cuda_cuda13?=v0.2.22
STRATA_ENGINE_VERSION_linux_x86_64_cuda_cuda13?=0.1.69
STRATA_ENGINE_SHA256_linux_x86_64_cuda_cuda13?=85e2cb1f1624269eeb42c21b2693400ee898625bccb5be994ff101d3474f083f
STRATA_ENGINE_TAG_linux_aarch64_cuda_cuda13?=v0.2.22
STRATA_ENGINE_VERSION_linux_aarch64_cuda_cuda13?=0.1.69
STRATA_ENGINE_SHA256_linux_aarch64_cuda_cuda13?=07943058ee0cebe1c938a99e7b7e181c108fe93b54ef9f5eb14effaa4b461cab
STRATA_ENGINE_TAG_linux_x86_64_hip_rx9070?=v0.2.22
STRATA_ENGINE_VERSION_linux_x86_64_hip_rx9070?=0.1.69
STRATA_ENGINE_SHA256_linux_x86_64_hip_rx9070?=238ed027555df85cd048e3d2c9492c281c2f47a7712344fd659e95c02170f601
STRATA_ENGINE_TAG_linux_x86_64_hip_rx7900?=v0.2.22
STRATA_ENGINE_VERSION_linux_x86_64_hip_rx7900?=0.1.69
STRATA_ENGINE_SHA256_linux_x86_64_hip_rx7900?=b3104784aca1848f53dabbd97dddaa5e2060798047ddefe9c3d603eed04d6df6
STRATA_ENGINE_TAG_linux_x86_64_hip_rx6000?=v0.2.22
STRATA_ENGINE_VERSION_linux_x86_64_hip_rx6000?=0.1.69
STRATA_ENGINE_SHA256_linux_x86_64_hip_rx6000?=a754446a67e90e33b3d11abcdb050eef83343924ee902b4e8994f3af52488d5f
STRATA_ENGINE_TAG_linux_x86_64_hip_halo?=v0.2.22
STRATA_ENGINE_VERSION_linux_x86_64_hip_halo?=0.1.69
STRATA_ENGINE_SHA256_linux_x86_64_hip_halo?=06209bb6b79d236624d8ae1fb08dbcccd555bf97521a59ca69534addf086e037
STRATA_ENGINE_TAG_linux_x86_64_sycl?=v0.2.22
STRATA_ENGINE_VERSION_linux_x86_64_sycl?=0.1.69
STRATA_ENGINE_SHA256_linux_x86_64_sycl?=94f0a892515280310da23bd49b49f37940867f223f520b3d5d4d33a2c07f08c3
STRATA_ENGINE_TAG_macos_arm64_metal?=v0.2.22
STRATA_ENGINE_VERSION_macos_arm64_metal?=0.1.69
STRATA_ENGINE_SHA256_macos_arm64_metal?=96331165be23b908bf1d5cb716761108119dbf437c4217cf0faa175ab60400cc

# Hy4-Flash engine (strata-hy4, a separate program; same packages per target)
STRATA_HY4_TAG_linux_x86_64_cuda_cuda12?=v0.2.21
STRATA_HY4_VERSION_linux_x86_64_cuda_cuda12?=0.1.67
STRATA_HY4_SHA256_linux_x86_64_cuda_cuda12?=4e69521a14e9a5845b319b8b16b9fdd14a26875837b0814e6ea5f07f8ba5ee1f
STRATA_HY4_TAG_linux_x86_64_cuda_cuda13?=v0.2.21
STRATA_HY4_VERSION_linux_x86_64_cuda_cuda13?=0.1.67
STRATA_HY4_SHA256_linux_x86_64_cuda_cuda13?=e411b07ef992be79a64a3af66f92cb53ec58115f25f1dc6b591042832e0e1f99
STRATA_HY4_TAG_linux_aarch64_cuda_cuda13?=v0.2.21
STRATA_HY4_VERSION_linux_aarch64_cuda_cuda13?=0.1.67
STRATA_HY4_SHA256_linux_aarch64_cuda_cuda13?=ffd16155b7d2be38d9a7c2b99ea539f72a0dcfee0049d788c81953de096c6210
STRATA_HY4_TAG_linux_x86_64_hip_rx9070?=v0.2.21
STRATA_HY4_VERSION_linux_x86_64_hip_rx9070?=0.1.67
STRATA_HY4_SHA256_linux_x86_64_hip_rx9070?=33b4950494563a1e619b8c967a08db9f04d64788c452fe98a8a808308b7586eb
STRATA_HY4_TAG_linux_x86_64_hip_rx7900?=v0.2.21
STRATA_HY4_VERSION_linux_x86_64_hip_rx7900?=0.1.67
STRATA_HY4_SHA256_linux_x86_64_hip_rx7900?=f77a616d907010aeea0747436be7849afa3a6fbbc649bf87d97b66f464a389ab
STRATA_HY4_TAG_linux_x86_64_hip_rx6000?=v0.2.21
STRATA_HY4_VERSION_linux_x86_64_hip_rx6000?=0.1.67
STRATA_HY4_SHA256_linux_x86_64_hip_rx6000?=c36ddf77f31fdf8e5c1f3cf52d98d99cec07cce44290f5e3de520b1222a470e9
STRATA_HY4_TAG_linux_x86_64_hip_halo?=v0.2.21
STRATA_HY4_VERSION_linux_x86_64_hip_halo?=0.1.67
STRATA_HY4_SHA256_linux_x86_64_hip_halo?=b16d89d593e6ec3448246134ef4f82e9c6657a872e95bcf8020d8384a767e787
STRATA_HY4_TAG_linux_x86_64_sycl?=v0.2.21
STRATA_HY4_VERSION_linux_x86_64_sycl?=0.1.67
STRATA_HY4_SHA256_linux_x86_64_sycl?=bee9b2579dfb5582dbf4ef28a9a9a33ae982d3fc17b86c8b3bddd5b42dfb0830
STRATA_HY4_TAG_macos_arm64_metal?=v0.2.21
STRATA_HY4_VERSION_macos_arm64_metal?=0.1.67
STRATA_HY4_SHA256_macos_arm64_metal?=000492ed6826b945a6f56c1470a3fb130cd3baeb5ad600475b950cd00892743f

# Inkling 2 Mini engine (strata-inkling, a separate program; same packages per target)
STRATA_INKLING_TAG_linux_x86_64_cuda_cuda12?=v0.2.22
STRATA_INKLING_VERSION_linux_x86_64_cuda_cuda12?=0.1.69
STRATA_INKLING_SHA256_linux_x86_64_cuda_cuda12?=1e5ff6d1cae75533aa0643cc4f4753b3791280572f816955b18d0351dbcb1e54
STRATA_INKLING_TAG_linux_x86_64_cuda_cuda13?=v0.2.22
STRATA_INKLING_VERSION_linux_x86_64_cuda_cuda13?=0.1.69
STRATA_INKLING_SHA256_linux_x86_64_cuda_cuda13?=866798f92e2b0e543ab47dc6212d8ddded7e321100db4fdcb732ff92813b5f3e
STRATA_INKLING_TAG_linux_aarch64_cuda_cuda13?=v0.2.22
STRATA_INKLING_VERSION_linux_aarch64_cuda_cuda13?=0.1.69
STRATA_INKLING_SHA256_linux_aarch64_cuda_cuda13?=8182cd16314b63575329b7824894aa0b343f197af5a136203c113529dc063d08
STRATA_INKLING_TAG_linux_x86_64_hip_rx9070?=v0.2.21
STRATA_INKLING_VERSION_linux_x86_64_hip_rx9070?=0.1.67
STRATA_INKLING_SHA256_linux_x86_64_hip_rx9070?=e50f924706574a057f94cfb826aad0428f7da86a324a4c612a06b32e53719ccd
STRATA_INKLING_TAG_linux_x86_64_hip_rx7900?=v0.2.21
STRATA_INKLING_VERSION_linux_x86_64_hip_rx7900?=0.1.67
STRATA_INKLING_SHA256_linux_x86_64_hip_rx7900?=c72a1bed33e63f4e8f8f03c803799fb596c57648f0ab578a7ae32bfa930e6dd7
STRATA_INKLING_TAG_linux_x86_64_hip_rx6000?=v0.2.21
STRATA_INKLING_VERSION_linux_x86_64_hip_rx6000?=0.1.67
STRATA_INKLING_SHA256_linux_x86_64_hip_rx6000?=bc584cc6b387d887a633748b8df70ab498fed369ebb49795d096a2862a729eb1
STRATA_INKLING_TAG_linux_x86_64_hip_halo?=v0.2.21
STRATA_INKLING_VERSION_linux_x86_64_hip_halo?=0.1.67
STRATA_INKLING_SHA256_linux_x86_64_hip_halo?=fa2caaae39dc2ba7263e722b6bc44744b2b9b633428fe37d6698be09a0ec744c
STRATA_INKLING_TAG_linux_x86_64_sycl?=v0.2.21
STRATA_INKLING_VERSION_linux_x86_64_sycl?=0.1.67
STRATA_INKLING_SHA256_linux_x86_64_sycl?=9c3a0c8f2fcf7595aa0535efbdd7da86a119de9891757c81b3eb1f68b6db55fc
STRATA_INKLING_TAG_macos_arm64_metal?=v0.2.21
STRATA_INKLING_VERSION_macos_arm64_metal?=0.1.67
STRATA_INKLING_SHA256_macos_arm64_metal?=8613d9922662d1a3740927dd62be4e0c0a6e289a8383910fc2699dedeeac448f

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
