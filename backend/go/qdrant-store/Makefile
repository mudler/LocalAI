GOCMD=go
TEST_FLAKES?=5

qdrant-store:
	CGO_ENABLED=0 $(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o qdrant-store ./

package:
	bash package.sh

build: qdrant-store package

test:
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --flake-attempts $(TEST_FLAKES) -v -r ./

clean:
	rm -f qdrant-store
