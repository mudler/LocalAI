# default are fmt and then check
.DEFAULT_GOAL := all

.PHONY: all
all: fmt check

.PHONY: fmt
fmt:
	@echo "Formatting code..."
	@cargo fmt --all -- --check

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
