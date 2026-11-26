GOCMD=go

# The colibri backend is a Go program that runs colibri's `coli serve`
# gateway (https://github.com/JustVugg/colibri, Apache-2.0) as a child
# process. Nothing is compiled from C here: upstream publishes release
# archives with the engines and the Python gateway for each platform, and
# this Makefile fetches the one for the build, checks it against a pinned
# SHA-256 and unpacks it. See docs/content/models/engines/colibri.md.
#
# Targets:
#   colibri  the Go binary only (what `make -C backend/go/colibri` builds)
#   engine   everything the image needs besides the Go binary: the colibri
#            release archive and a portable Python for `coli serve`.
#            Dockerfile.golang calls this in its own cacheable layer.
#   build    engine + colibri + package
#
# ---------------------------------------------------------------------------
# Pins. COLIBRI_VERSION is the upstream release tag. Each archive has its own
# SHA-256, from the release's SHA256SUMS.txt. Change them together.
# ---------------------------------------------------------------------------
COLIBRI_VERSION?=v2.2.0
COLIBRI_SHA256_linux_x86_64?=b1e2bc8a650b2b216e17357614cfcb4355a83fb9d3077d61f8e1d63beea14d72
COLIBRI_SHA256_linux_aarch64?=1e340e5bda50863303e7d2b3e2cd530f48926967c1bb390049c2df4dd43af602
COLIBRI_SHA256_macos_arm64?=6fce6e344131e527f904d012e2701044345e615be5a2ca3611f6b2f905d864d9
COLIBRI_REPO?=https://github.com/JustVugg/colibri

# Portable CPython for `coli serve` (python-build-standalone). The gateway
# uses the standard library only, so no packages are installed. The backend
# image is built FROM scratch, so the host's Python cannot be assumed.
# Same pins as backend/go/strata.
PBS_RELEASE?=20261026
PBS_PYTHON?=3.12.16
PBS_SHA256_linux_x86_64?=ce4f6d0a6cfdb3bed6e44c2f9affd85943c76a42e25ad3a259002f31d6fb8a20
PBS_SHA256_linux_aarch64?=bb4b6392670c5d3f93876996fcaa19858a4a413a0c7720144c18b0d6f1f0db2d
PBS_SHA256_macos_arm64?=353d5f1b4fd2715e7053f12f068435d5bbad16b91e057006b5d01e0925709a7e

UNAME_S:=$(shell uname -s)
UNAME_M:=$(shell uname -m)

ifeq ($(UNAME_S),Darwin)
  ifeq ($(UNAME_M),arm64)
    PLATFORM:=macos_arm64
  endif
else ifeq ($(UNAME_S),Linux)
  ifeq ($(UNAME_M),x86_64)
    PLATFORM:=linux_x86_64
  else ifneq ($(filter aarch64 arm64,$(UNAME_M)),)
    PLATFORM:=linux_aarch64
  endif
endif

ASSET_linux_x86_64=linux-x86_64
ASSET_linux_aarch64=linux-aarch64
ASSET_macos_arm64=macos-arm64
COLIBRI_FILE=colibri-$(COLIBRI_VERSION)-$(ASSET_$(PLATFORM)).tar.gz

PBS_TRIPLE_linux_x86_64=x86_64-unknown-linux-gnu
PBS_TRIPLE_linux_aarch64=aarch64-unknown-linux-gnu
PBS_TRIPLE_macos_arm64=aarch64-apple-darwin
PBS_FILE=cpython-$(PBS_PYTHON)+$(PBS_RELEASE)-$(PBS_TRIPLE_$(PLATFORM))-install_only_stripped.tar.gz

STAMP=.stamp

.PHONY: colibri engine engine-dist engine-python build package test clean purge check-pins print-asset

colibri:
	CGO_ENABLED=0 $(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o colibri ./

print-asset:
	@echo "$(COLIBRI_FILE)"

# Fails with one clear line per malformed pin. No recipe here uses $(MAKE):
# Dockerfile.golang probes `make -n engine`.
check-pins:
	@fail=0; \
	echo '$(COLIBRI_VERSION)' | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$$' || { echo 'bad COLIBRI_VERSION' >&2; fail=1; }; \
	$(foreach v,$(filter COLIBRI_SHA256_% PBS_SHA256_%,$(.VARIABLES)),echo '$($(v))' | grep -Eq '^[0-9a-f]{64}$$' || { echo 'bad pin $(v)' >&2; fail=1; }; ) \
	for p in linux_x86_64 linux_aarch64 macos_arm64; do \
	  echo " $(filter COLIBRI_SHA256_% PBS_SHA256_%,$(.VARIABLES)) " | grep -q " COLIBRI_SHA256_$$p " || { echo "missing COLIBRI_SHA256_$$p" >&2; fail=1; }; \
	  echo " $(filter COLIBRI_SHA256_% PBS_SHA256_%,$(.VARIABLES)) " | grep -q " PBS_SHA256_$$p " || { echo "missing PBS_SHA256_$$p" >&2; fail=1; }; \
	done; \
	exit $$fail

engine: engine-dist engine-python

# The colibri release archive, verified and unpacked into colibri-dist/.
engine-dist:
	@if [ -z "$(PLATFORM)" ]; then \
	  echo "colibri: no colibri release archive for $(UNAME_S)/$(UNAME_M); supported: Linux x86_64 and aarch64, macOS arm64" >&2; \
	  exit 1; \
	fi
	@set -e; \
	if [ -f colibri-dist/$(STAMP)-$(COLIBRI_VERSION) ]; then echo "==> colibri-dist is up to date"; exit 0; fi; \
	rm -rf colibri-dist; mkdir -p colibri-dist downloads; \
	echo "==> fetching $(COLIBRI_FILE)"; \
	sh ./fetch-asset.sh "$(COLIBRI_REPO)/releases/download/$(COLIBRI_VERSION)/$(COLIBRI_FILE)" "$(COLIBRI_SHA256_$(PLATFORM))" "downloads/$(COLIBRI_FILE)"; \
	tar -xzf "downloads/$(COLIBRI_FILE)" -C colibri-dist; \
	rm -f "downloads/$(COLIBRI_FILE)"; \
	for f in coli openai_server.py qwen36; do \
	  [ -e "colibri-dist/$$f" ] || { echo "colibri: $(COLIBRI_FILE) has no $$f at its top level" >&2; exit 1; }; \
	done; \
	touch colibri-dist/$(STAMP)-$(COLIBRI_VERSION)

# Portable CPython; coli serve needs the standard library only.
engine-python:
	@if [ -z "$(PLATFORM)" ]; then echo "colibri: no portable Python for $(UNAME_S)/$(UNAME_M)" >&2; exit 1; fi
	@set -e; \
	if [ -f python/$(STAMP)-$(PBS_RELEASE)-$(PBS_PYTHON) ]; then echo "==> python is up to date"; exit 0; fi; \
	rm -rf python; mkdir -p downloads; \
	echo "==> fetching $(PBS_FILE)"; \
	sh ./fetch-asset.sh "https://github.com/astral-sh/python-build-standalone/releases/download/$(PBS_RELEASE)/$(subst +,%2B,$(PBS_FILE))" "$(PBS_SHA256_$(PLATFORM))" "downloads/$(PBS_FILE)"; \
	tar -xzf "downloads/$(PBS_FILE)" -C .; \
	rm -f "downloads/$(PBS_FILE)"; \
	./python/bin/python3 -I -c "import json, http.server, ssl; print('==> python ok')"; \
	touch python/$(STAMP)-$(PBS_RELEASE)-$(PBS_PYTHON)

package:
	bash package.sh

build: engine colibri package

test:
	CGO_ENABLED=0 $(GOCMD) test ./...

clean:
	rm -rf colibri package

# Also drops everything fetched.
purge: clean
	rm -rf colibri-dist python downloads
