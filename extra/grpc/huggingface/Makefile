.PONY: huggingface
huggingface:
	@echo "Creating virtual environment..."
	@conda env create --name huggingface --file huggingface.yml
	@echo "Virtual environment created."

.PONY: run
run:
	@echo "Running huggingface..."
	bash run.sh
	@echo "huggingface run."

# It is not working well by using command line. It only6 works with IDE like VSCode.
.PONY: test
test:
	@echo "Testing huggingface..."
	bash test.sh
	@echo "huggingface tested."