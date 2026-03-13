GOCMD?=go
GO_TAGS?=

OPUS_CFLAGS := $(shell pkg-config --cflags opus)
OPUS_LIBS := $(shell pkg-config --libs opus)

libopusshim.so: csrc/opus_shim.c
	$(CC) -shared -fPIC -o $@ $< $(OPUS_CFLAGS) $(OPUS_LIBS)

opus: libopusshim.so
	$(GOCMD) build -tags "$(GO_TAGS)" -o opus ./

package: opus
	bash package.sh

build: package

clean:
	rm -f opus libopusshim.so
