# default are fmt and then check
.DEFAULT_GOAL := all

.PHONY: all
all: fmt check

.PHONY: fmt
fmt:
	@echo "Formatting code..."
	@cargo fmt --all --check

.PHONY: build
build:
	@echo "Building..."
	@cargo build --release

.PHONY: test
test:
	@echo "Testing..."
	@cargo test --all

.PHONY: check
check:
	@echo "Checking..."
	@cargo check --all

.PHONY: clean
clean:
	@echo "Cleaning..."
	@cargo clean

.PHONY: burn
burn:
	@echo "Burning..."
	@cargo run --bin server --package backend-burn

.PHONY: health
health:
	@echo "Burning..."
	grpcurl -plaintext -import-path ../../pkg/grpc/proto -proto backend.proto -d '{}' '[::1]:50051' backend.Backend/Health
