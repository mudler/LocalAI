
# go-piper version
PIPER_REPO?=https://github.com/mudler/go-piper
PIPER_VERSION?=e10ca041a885d4a8f3871d52924b47792d5e5aa0

CURRENT_DIR=$(abspath ./)
GOCMD=go

PIPER_CGO_CXXFLAGS+=-I$(CURRENT_DIR)/sources/go-piper/piper/src/cpp -I$(CURRENT_DIR)/sources/go-piper/piper/build/fi/include -I$(CURRENT_DIR)/sources/go-piper/piper/build/pi/include -I$(CURRENT_DIR)/sources/go-piper/piper/build/si/include
PIPER_CGO_LDFLAGS+=-L$(CURRENT_DIR)/sources/go-piper/piper/build/fi/lib -L$(CURRENT_DIR)/sources/go-piper/piper/build/pi/lib -L$(CURRENT_DIR)/sources/go-piper/piper/build/si/lib -lfmt -lspdlog -lucd

## go-piper
sources/go-piper:
	mkdir -p sources/go-piper
	cd sources/go-piper && \
	git init && \
	git remote add origin $(PIPER_REPO) && \
	git fetch origin && \
	git checkout $(PIPER_VERSION) && \
	git submodule update --init --recursive --depth 1 --single-branch

sources/go-piper/libpiper_binding.a: sources/go-piper
	$(MAKE) -C sources/go-piper libpiper_binding.a example/main piper.o

espeak-ng-data: sources/go-piper sources/go-piper/libpiper_binding.a
	mkdir -p espeak-ng-data
	@cp -rf sources/go-piper/piper-phonemize/pi/share/espeak-ng-data/. espeak-ng-data

piper: sources/go-piper sources/go-piper/libpiper_binding.a espeak-ng-data
	$(GOCMD) mod edit -replace github.com/mudler/go-piper=$(CURRENT_DIR)/sources/go-piper
	CGO_CXXFLAGS="$(PIPER_CGO_CXXFLAGS)" CGO_LDFLAGS="$(PIPER_CGO_LDFLAGS)" LIBRARY_PATH=$(CURRENT_DIR)/sources/go-piper \
	$(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o piper ./

package:
	bash package.sh

build: piper package