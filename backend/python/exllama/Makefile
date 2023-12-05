.PHONY: exllama
exllama:
	@echo "Creating virtual environment..."
	@conda env create --name exllama --file exllama.yml
	@echo "Virtual environment created."
	bash install.sh

.PHONY: run
run:
	@echo "Running exllama..."
	bash run.sh
	@echo "exllama run."
