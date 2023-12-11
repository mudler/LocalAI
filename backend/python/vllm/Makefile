.PHONY: vllm
vllm:
	@echo "Creating virtual environment..."
	@conda env create --name vllm --file vllm.yml
	@echo "Virtual environment created."

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