CURRENT_DIR=$(abspath ./)

.PHONY: kokoros-grpc
kokoros-grpc:
	mkdir -p $(CURRENT_DIR)/proto
	cp $(CURRENT_DIR)/../../backend.proto $(CURRENT_DIR)/proto/backend.proto
	cd $(CURRENT_DIR) && \
		BACKEND_PROTO_PATH=$(CURRENT_DIR)/proto/backend.proto \
		cargo build --release

.PHONY: package
package:
	bash package.sh

.PHONY: test
test: kokoros-grpc
	cd $(CURRENT_DIR) && cargo test

.PHONY: build
build: kokoros-grpc package

.PHONY: clean
clean:
	cargo clean
	rm -rf package proto
