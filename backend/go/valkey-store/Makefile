GOCMD=go

valkey-store:
	CGO_ENABLED=0 $(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o valkey-store ./

package:
	bash package.sh

build: valkey-store package

clean:
	rm -f valkey-store
