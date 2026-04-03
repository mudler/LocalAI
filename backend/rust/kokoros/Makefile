CURRENT_DIR=$(abspath ./)

# Enable CUDA feature when building with cublas/l4t
CARGO_FEATURES :=
ifneq (,$(filter cublas l4t,$(BUILD_TYPE)))
CARGO_FEATURES := --features cuda --no-default-features
endif

.PHONY: kokoros-grpc
kokoros-grpc:
	mkdir -p $(CURRENT_DIR)/proto
	cp $(CURRENT_DIR)/../../backend.proto $(CURRENT_DIR)/proto/backend.proto
	cd $(CURRENT_DIR) && \
		BACKEND_PROTO_PATH=$(CURRENT_DIR)/proto/backend.proto \
		cargo build --release $(CARGO_FEATURES)

.PHONY: package
package:
	bash package.sh

.PHONY: build
build: kokoros-grpc package

.PHONY: clean
clean:
	cargo clean
	rm -rf package proto
