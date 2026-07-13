# SPDX-License-Identifier: MIT

LONGCAT_VIDEO_VERSION?=6b3f4b8582a8bc3f20f795735f5383716c4ba794
LONGCAT_VIDEO_REPO?=https://github.com/meituan-longcat/LongCat-Video
LONGCAT_SOURCE_STAMP=sources/LongCat-Video/.localai-$(LONGCAT_VIDEO_VERSION)

.PHONY: all
all: $(LONGCAT_SOURCE_STAMP)
	bash install.sh

$(LONGCAT_SOURCE_STAMP): patches/0001-sdpa-attention-fallback.patch
	rm -rf sources/LongCat-Video
	mkdir -p sources/LongCat-Video
	cd sources/LongCat-Video && git init -q && \
		git remote add origin $(LONGCAT_VIDEO_REPO) && \
		git fetch --depth 1 origin $(LONGCAT_VIDEO_VERSION) && \
		git checkout --detach FETCH_HEAD && \
		git apply ../../patches/0001-sdpa-attention-fallback.patch && \
		rm -rf .git && \
		touch .localai-$(LONGCAT_VIDEO_VERSION)

.PHONY: run
run: all
	bash run.sh

.PHONY: test
test: all
	bash test.sh

.PHONY: protogen-clean
protogen-clean:
	$(RM) backend_pb2.py backend_pb2_grpc.py

.PHONY: clean
clean: protogen-clean
	rm -rf __pycache__ lib python sources venv
