CONDA_ENV_PATH = "transformers.yml"

ifeq ($(BUILD_TYPE), cublas)
	CONDA_ENV_PATH = "transformers-nvidia.yml"
endif

ifeq ($(BUILD_TYPE), hipblas)
	CONDA_ENV_PATH = "transformers-rocm.yml"
endif

.PHONY: transformers
transformers:
	@echo "Installing $(CONDA_ENV_PATH)..."
	bash install.sh $(CONDA_ENV_PATH)
