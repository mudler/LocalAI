export CONDA_ENV_PATH = "diffusers.yml"

ifeq ($(BUILD_TYPE), hipblas)
export CONDA_ENV_PATH = "diffusers-rocm.yml"
endif

.PHONY: diffusers
diffusers:
	@echo "Installing $(CONDA_ENV_PATH)..."
	bash install.sh $(CONDA_ENV_PATH)

.PHONY: run
run:
	@echo "Running diffusers..."
	bash run.sh
	@echo "Diffusers run."

test:
	bash test.sh
