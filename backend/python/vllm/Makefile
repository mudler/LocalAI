.PONY: vllm
vllm:
	@echo "Creating virtual environment..."
	@conda env create --name vllm --file vllm.yml
	@echo "Virtual environment created."

.PONY: run
run:
	@echo "Running vllm..."
	bash run.sh
	@echo "vllm run."