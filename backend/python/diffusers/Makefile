CONDA_ENV_PATH = "diffusers.yml"

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