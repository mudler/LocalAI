
TRANSFORMERS_MUSICGEN_CONDA_PATH = "transformers-musicgen.yml"

ifeq ($(BUILD_TYPE), cublas)
	TRANSFORMERS_MUSICGEN_CONDA_PATH = "transformers-musicgen-nvidia.yml"
endif

.PHONY: transformers-musicgen
transformers-musicgen:
	@echo "Creating virtual environment..."
	@conda env create --name transformers-musicgen --file $(TRANSFORMERS_MUSICGEN_CONDA_PATH)
	@echo "Virtual environment created."

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
