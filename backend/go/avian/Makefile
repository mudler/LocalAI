GOCMD=go

avian:
	CGO_ENABLED=0 $(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o avian ./

package:
	bash package.sh

build: avian package

clean:
	rm -f avian
