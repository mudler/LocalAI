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

alpaca-model:
    FROM alpine
    # This is the alpaca.cpp model https://github.com/antimatter15/alpaca.cpp
    ARG MODEL_URL=https://ipfs.io/ipfs/QmUp1UGeQFDqJKvtjbSYPBiZZKRjLp8shVP9hT8ZB9Ynv1
    RUN wget -O model.bin -c $MODEL_URL
    SAVE ARTIFACT model.bin AS LOCAL model.bin 

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
    COPY +alpaca-model/model.bin /model.bin
    COPY +build/llama-cli /llama-cli
    ENV MODEL_PATH=/model.bin
    ENTRYPOINT [ "/llama-cli" ]
    SAVE IMAGE --push $IMAGE

image-all:
    BUILD --platform=linux/amd64 --platform=linux/arm64 +image