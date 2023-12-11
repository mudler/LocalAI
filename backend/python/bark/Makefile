.PHONY: ttsbark
ttsbark:
	@echo "Creating virtual environment..."
	@conda env create --name ttsbark --file ttsbark.yml
	@echo "Virtual environment created."

.PHONY: run
run:
	@echo "Running bark..."
	bash run.sh
	@echo "bark run."

.PHONY: test
test:
	@echo "Testing bark..."
	bash test.sh
	@echo "bark tested."
