GOCMD=go

cloud-proxy:
	CGO_ENABLED=0 $(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o cloud-proxy ./

package:
	bash package.sh

build: cloud-proxy package

clean:
	rm -f cloud-proxy
