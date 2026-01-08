GOCMD=go

huggingface:
	CGO_ENABLED=0 $(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o huggingface ./

package:
	bash package.sh

build: huggingface package