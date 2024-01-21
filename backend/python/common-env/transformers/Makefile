CONDA_ENV_PATH = "transformers.yml"

ifeq ($(BUILD_TYPE), cublas)
	CONDA_ENV_PATH = "transformers-nvidia.yml"
endif

.PHONY: transformers
transformers:
	@echo "Installing $(CONDA_ENV_PATH)..."
	bash install.sh $(CONDA_ENV_PATH)
