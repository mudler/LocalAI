.PHONY: all run test protogen-clean clean

all:
	bash install.sh

run: all
	bash run.sh

test:
	bash test.sh

protogen-clean:
	$(RM) backend_pb2.py backend_pb2_grpc.py

clean: protogen-clean
	rm -rf __pycache__ venv
