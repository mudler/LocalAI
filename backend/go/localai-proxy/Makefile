GOCMD=go

# Packaged as a standalone gallery backend by backend/Dockerfile.golang.
localai-proxy:
	CGO_ENABLED=0 $(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o localai-proxy ./

package:
	bash package.sh

build: localai-proxy package

clean:
	rm -f localai-proxy
