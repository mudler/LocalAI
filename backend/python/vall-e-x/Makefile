ifneq (,$(findstring sycl,$(BUILD_TYPE)))
export SKIP_CONDA=1
endif

.PHONY: ttsvalle
ttsvalle:
	$(MAKE) -C ../common-env/transformers
	bash install.sh

.PHONY: run
run:
	@echo "Running ttsvalle..."
	bash run.sh
	@echo "ttsvalle run."

.PHONY: test
test:
	@echo "Testing valle..."
	bash test.sh
	@echo "valle tested."
