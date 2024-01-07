.PHONY: vllm
vllm:
	$(MAKE) -C ../common-env/transformers

.PHONY: run
run:
	@echo "Running vllm..."
	bash run.sh
	@echo "vllm run."

.PHONY: test
test:
	@echo "Testing vllm..."
	bash test.sh
	@echo "vllm tested."