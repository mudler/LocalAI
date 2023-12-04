.PHONY: exllama2
exllama2:
	@echo "Creating virtual environment..."
	@conda env create --name exllama2 --file exllama2.yml
	@echo "Virtual environment created."
	bash install.sh

.PHONY: run
run:
	@echo "Running exllama2..."
	bash run.sh
	@echo "exllama2 run."
