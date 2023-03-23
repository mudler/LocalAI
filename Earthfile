VERSION 0.7

go-deps:
    ARG GO_VERSION=1.20
    FROM golang:$GO_VERSION
    WORKDIR /build
    COPY go.mod ./
    COPY go.sum ./
    RUN go mod download
    RUN apt-get update
    SAVE ARTIFACT go.mod AS LOCAL go.mod
    SAVE ARTIFACT go.sum AS LOCAL go.sum

model-image:
    ARG MODEL_IMAGE=quay.io/go-skynet/models:ggml2-alpaca-7b-v0.2
    FROM $MODEL_IMAGE
    SAVE ARTIFACT /models/model.bin

build:
    FROM +go-deps
    WORKDIR /build
    RUN git clone https://github.com/go-skynet/llama
    RUN cd llama && make libllama.a
    COPY . .
    RUN C_INCLUDE_PATH=/build/llama LIBRARY_PATH=/build/llama go build -o llama-cli ./
    SAVE ARTIFACT llama-cli AS LOCAL llama-cli

image:
    FROM +go-deps
    ARG IMAGE=alpaca-cli
    COPY +model-image/model.bin /model.bin
    COPY +build/llama-cli /llama-cli
    ENV MODEL_PATH=/model.bin
    ENTRYPOINT [ "/llama-cli" ]
    SAVE IMAGE --push $IMAGE

lite-image:
    FROM +go-deps
    ARG IMAGE=alpaca-cli-nomodel
    COPY +build/llama-cli /llama-cli
    ENV MODEL_PATH=/model.bin
    ENTRYPOINT [ "/llama-cli" ]
    SAVE IMAGE --push $IMAGE-lite

image-all:
    BUILD --platform=linux/amd64 --platform=linux/arm64 +image
    BUILD --platform=linux/amd64 --platform=linux/arm64 +lite-image