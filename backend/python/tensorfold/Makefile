# SPDX-License-Identifier: MIT

TENSORFOLD_VERSION?=9cd52ab4daba68ddd09be89be8f23ad43175e821
TENSORFOLD_REPO?=https://github.com/ashhart/TensorFold
TENSORFOLD_STAMP=sources/TensorFold/.localai-$(TENSORFOLD_VERSION)

.PHONY: all
all: $(TENSORFOLD_STAMP)
	bash install.sh

# TensorFold is git-only (no PyPI release), so the commit is fetched here and
# installed from the local checkout; install.sh never reaches for the network
# for it. The stamp file makes a changed pin re-fetch.
$(TENSORFOLD_STAMP):
	rm -rf sources/TensorFold
	mkdir -p sources/TensorFold
	cd sources/TensorFold && git init -q && \
		git remote add origin $(TENSORFOLD_REPO) && \
		git fetch --depth 1 origin $(TENSORFOLD_VERSION) && \
		git checkout --detach FETCH_HEAD && \
		rm -rf .git && \
		touch .localai-$(TENSORFOLD_VERSION)

.PHONY: run
run: all
	bash run.sh

.PHONY: test
test: all
	bash test.sh

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2_grpc.py backend_pb2.py

.PHONY: clean
clean: protogen-clean
	rm -rf __pycache__ sources venv
