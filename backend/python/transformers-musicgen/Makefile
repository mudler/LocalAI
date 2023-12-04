.PHONY: transformers-musicgen
transformers-musicgen:
	@echo "Creating virtual environment..."
	@conda env create --name transformers-musicgen --file transformers-musicgen.yml
	@echo "Virtual environment created."
# TODO: check the variable to re-enable NVIDIA

.PHONY: run
run:
	@echo "Running transformers..."
	bash run.sh
	@echo "transformers run."

# It is not working well by using command line. It only6 works with IDE like VSCode.
.PHONY: test
test:
	@echo "Testing transformers..."
	bash test.sh
	@echo "transformers tested."
