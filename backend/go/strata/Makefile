GOCMD=go

# Packaged as a standalone gallery backend by backend/Dockerfile.golang.
# The backend is pure Go: the Strata engine executable is supplied by the
# user (see README.md) and is not built here.
strata:
	CGO_ENABLED=0 $(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o strata ./

package:
	bash package.sh

build: strata package

test:
	CGO_ENABLED=0 $(GOCMD) test ./...

clean:
	rm -f strata
