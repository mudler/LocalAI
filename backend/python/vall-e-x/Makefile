.PHONY: ttsvalle
ttsvalle:
	@echo "Creating virtual environment..."
	@conda env create --name ttsvalle --file ttsvalle.yml
	@echo "Virtual environment created."
	bash install.sh

.PHONY: run
run:
	@echo "Running ttsvalle..."
	bash run.sh
	@echo "ttsvalle run."

.PHONY: test
test:
	@echo "Testing valle..."
	bash test.sh
	@echo "valle tested."
