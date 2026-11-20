EIGEN_REPO?=https://gitlab.com/libeigen/eigen.git
EIGEN_VERSION?=549bf8c75b6aae071cde2f28aa48f16ee3ae60b0
GOCMD?=go
GO_TAGS?=
JOBS?=2
CMAKE_ARGS?=
.DEFAULT_GOAL := build

ifeq ($(shell uname -s),Darwin)
CMAKE_ARGS+=-DCMAKE_BUILD_WITH_INSTALL_RPATH=ON -DCMAKE_INSTALL_RPATH=@loader_path
endif

.PHONY: native build package test clean FORCE
FORCE:

sources/eigen/.localai-version: FORCE
	@test -d sources/eigen/.git || git clone $(EIGEN_REPO) sources/eigen
	@if test -f $@ && test "$$(git -C sources/eigen rev-parse HEAD)" = "$(EIGEN_VERSION)"; then exit 0; fi; \
		cd sources/eigen && git diff --quiet && git diff --cached --quiet && \
		git fetch origin $(EIGEN_VERSION) && git checkout $(EIGEN_VERSION) && touch .localai-version

native: sources/eigen/.localai-version
	cmake -S . -B build-native -DCMAKE_BUILD_TYPE=Release $(CMAKE_ARGS)
	cmake --build build-native --target meshremesh -j$(JOBS)

test:
	$(GOCMD) test ./...

meshremesh: native $(wildcard *.go)
	CGO_ENABLED=0 $(GOCMD) build -tags "$(GO_TAGS)" -o $@ ./

package: meshremesh
	bash package.sh

build: package

clean:
	rm -rf build-native package meshremesh
