GOCMD=go

valkey-store:
	CGO_ENABLED=0 $(GOCMD) build -ldflags "$(LD_FLAGS)" -tags "$(GO_TAGS)" -o valkey-store ./

package:
	bash package.sh

build: valkey-store package

## Runs the backend's Ginkgo suite. The unit (mock) specs run without a
## container; the integration specs skip automatically unless VALKEY_ADDR is set.
test:
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --flake-attempts $(TEST_FLAKES) -v -r ./

clean:
	rm -f valkey-store
