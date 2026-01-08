GOCMD=go

local-store:
	CGO_ENABLED=0 $(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o local-store ./

package:
	bash package.sh

build: local-store package