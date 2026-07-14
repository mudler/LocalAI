GOCMD?=go
GO_TAGS?=

# The opus shim is a small C wrapper around libopus' variadic
# opus_encoder_ctl (see csrc/opus_shim.c). It is built as a shared library
# and dlopen'd at runtime by the Go backend (codec.go). The extension is
# OS-specific: Linux uses .so, macOS uses .dylib. OS is exported by the root
# Makefile (`export OS := $(shell uname -s)`).
SHIM_EXT=so

OPUS_CFLAGS := $(shell pkg-config --cflags opus)
OPUS_LIBS := $(shell pkg-config --libs opus)
SHIM_LDFLAGS := $(OPUS_LIBS)

ifeq ($(OS),Darwin)
	SHIM_EXT=dylib
	# Resolve libopus symbols lazily from the already globally-loaded
	# libopus (codec.go dlopens it RTLD_GLOBAL before the shim) rather than
	# recording an absolute Homebrew path in the dylib. This keeps the
	# packaged shim relocatable on machines that have no Homebrew.
	SHIM_LDFLAGS := -undefined dynamic_lookup
endif

libopusshim.$(SHIM_EXT): csrc/opus_shim.c
	$(CC) -shared -fPIC -o $@ $< $(OPUS_CFLAGS) $(SHIM_LDFLAGS)

opus: libopusshim.$(SHIM_EXT)
	$(GOCMD) build -tags "$(GO_TAGS)" -o opus ./

package: opus
	bash package.sh

build: package

clean:
	rm -f opus libopusshim.$(SHIM_EXT)
	rm -rf package

.PHONY: build package clean
