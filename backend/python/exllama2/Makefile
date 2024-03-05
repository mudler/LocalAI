ifneq (,$(findstring sycl,$(BUILD_TYPE)))
export SKIP_CONDA=1
endif

.PHONY: exllama2
exllama2:
	$(MAKE) -C ../common-env/transformers
	bash install.sh

.PHONY: run
run:
	@echo "Running exllama2..."
	bash run.sh
	@echo "exllama2 run."
